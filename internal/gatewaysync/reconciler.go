package gatewaysync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var managedGVRs = []schema.GroupVersionResource{
	{Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaybackends"},
	{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"},
	{Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaypolicies"},
}

// Mode controls whether reconciliation observes or mutates desired state.
type Mode string

const (
	ModeActive   Mode = "active"
	ModeShadow   Mode = "shadow"
	ModeDisabled Mode = "disabled"
)

// KubernetesClient is the narrow Kubernetes API surface used by reconciliation.
type KubernetesClient interface {
	Apply(context.Context, Resource) (*unstructured.Unstructured, error)
	Get(context.Context, Resource) (*unstructured.Unstructured, error)
	ListManaged(context.Context, schema.GroupVersionResource, string) ([]*unstructured.Unstructured, error)
	Delete(context.Context, schema.GroupVersionResource, string, string, types.UID, string) error
}

// ReconcilerOptions configures mutation and acceptance safety.
type ReconcilerOptions struct {
	TargetNamespace        string
	Mode                   Mode
	Prune                  bool
	AcceptanceTimeout      time.Duration
	AcceptancePollInterval time.Duration
}

// Result summarizes one complete reconciliation.
type Result struct {
	Desired int
	Actual  int
	Drift   int
	Applied int
	Pruned  int
}

// Reconciler converges a verified snapshot into the AgentGateway API.
type Reconciler struct {
	client                 KubernetesClient
	targetNamespace        string
	mode                   Mode
	prune                  bool
	acceptanceTimeout      time.Duration
	acceptancePollInterval time.Duration
}

// NewReconciler validates options and constructs a Reconciler.
func NewReconciler(client KubernetesClient, options ReconcilerOptions) (*Reconciler, error) {
	if client == nil {
		return nil, errors.New("Kubernetes client is required")
	}
	if options.TargetNamespace == "" {
		return nil, errors.New("target namespace is required")
	}
	if options.Mode != ModeActive && options.Mode != ModeShadow && options.Mode != ModeDisabled {
		return nil, fmt.Errorf("unsupported reconciliation mode %q", options.Mode)
	}
	if options.AcceptanceTimeout <= 0 {
		return nil, errors.New("acceptance timeout must be positive")
	}
	if options.AcceptancePollInterval <= 0 {
		return nil, errors.New("acceptance poll interval must be positive")
	}
	return &Reconciler{
		client:                 client,
		targetNamespace:        options.TargetNamespace,
		mode:                   options.Mode,
		prune:                  options.Prune,
		acceptanceTimeout:      options.AcceptanceTimeout,
		acceptancePollInterval: options.AcceptancePollInterval,
	}, nil
}

// Reconcile applies, verifies, and optionally prunes one verified snapshot.
func (r *Reconciler) Reconcile(ctx context.Context, snapshot Snapshot) (Result, error) {
	if err := r.validateSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	if r.mode != ModeActive {
		return r.inspect(ctx, snapshot)
	}

	result := Result{Desired: len(snapshot.Resources)}
	for _, resource := range snapshot.Resources {
		if _, err := r.client.Apply(ctx, resource); err != nil {
			return result, fmt.Errorf("apply %s/%s: %w", resource.Object.GetKind(), resource.Object.GetName(), err)
		}
		result.Applied++
	}
	if err := r.waitForAcceptance(ctx, snapshot.Resources); err != nil {
		return result, err
	}

	if r.prune {
		live, err := r.listManaged(ctx)
		if err != nil {
			return result, err
		}
		desired := desiredNames(snapshot.Resources)
		for _, resource := range live {
			if _, exists := desired[resource.GVR][resource.Object.GetName()]; exists {
				continue
			}
			object := resource.Object
			if err := r.client.Delete(ctx, resource.GVR, r.targetNamespace, object.GetName(), object.GetUID(), object.GetResourceVersion()); err != nil && !apierrors.IsNotFound(err) {
				return result, fmt.Errorf("prune %s/%s: %w", object.GetKind(), object.GetName(), err)
			}
			result.Pruned++
		}
	}

	observed, err := r.inspect(ctx, snapshot)
	if err != nil {
		return result, err
	}
	observed.Applied = result.Applied
	observed.Pruned = result.Pruned
	return observed, nil
}

func (r *Reconciler) validateSnapshot(snapshot Snapshot) error {
	if snapshot.ETag == "" || snapshot.Digest == "" {
		return errors.New("snapshot metadata is incomplete")
	}
	if snapshot.ResourceCount != len(snapshot.Resources) {
		return fmt.Errorf("snapshot has %d resources, metadata declares %d", len(snapshot.Resources), snapshot.ResourceCount)
	}
	for _, resource := range snapshot.Resources {
		object := resource.Object
		if object == nil {
			return errors.New("snapshot contains a nil resource")
		}
		if object.GetNamespace() != r.targetNamespace {
			return fmt.Errorf("snapshot resource %s/%s targets namespace %q", object.GetKind(), object.GetName(), object.GetNamespace())
		}
		if allowed, exists := allowedResources[object.GroupVersionKind()]; !exists || allowed != resource.GVR {
			return fmt.Errorf("snapshot resource %s/%s has an invalid mapping", object.GetKind(), object.GetName())
		}
		if object.GetLabels()[managedByLabel] != managedByValue {
			return fmt.Errorf("snapshot resource %s/%s lacks registry ownership", object.GetKind(), object.GetName())
		}
	}
	return nil
}

func (r *Reconciler) waitForAcceptance(ctx context.Context, resources []Resource) error {
	ctx, cancel := context.WithTimeout(ctx, r.acceptanceTimeout)
	defer cancel()
	ticker := time.NewTicker(r.acceptancePollInterval)
	defer ticker.Stop()

	for {
		pending := make([]string, 0)
		for _, resource := range resources {
			if resource.Object.GetKind() == "AgentgatewayPolicy" {
				continue
			}
			live, err := r.client.Get(ctx, resource)
			if err != nil {
				pending = append(pending, resource.Object.GetKind()+"/"+resource.Object.GetName())
				continue
			}
			if !accepted(live) {
				pending = append(pending, live.GetKind()+"/"+live.GetName())
			}
		}
		if len(pending) == 0 {
			return nil
		}
		sort.Strings(pending)
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for AgentGateway acceptance of %s: %w", strings.Join(pending, ", "), ctx.Err())
		case <-ticker.C:
		}
	}
}

func accepted(object *unstructured.Unstructured) bool {
	switch object.GetKind() {
	case "AgentgatewayBackend":
		conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
		return conditionsTrue(conditions, "Accepted")
	case "HTTPRoute":
		parents, _, _ := unstructured.NestedSlice(object.Object, "status", "parents")
		for _, item := range parents {
			parent, ok := item.(map[string]any)
			if !ok {
				continue
			}
			conditions, _, _ := unstructured.NestedSlice(parent, "conditions")
			if conditionsTrue(conditions, "Accepted", "ResolvedRefs") {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func conditionsTrue(conditions []any, required ...string) bool {
	found := make(map[string]bool, len(required))
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok || condition["status"] != "True" {
			continue
		}
		if conditionType, ok := condition["type"].(string); ok {
			found[conditionType] = true
		}
	}
	for _, conditionType := range required {
		if !found[conditionType] {
			return false
		}
	}
	return true
}

func (r *Reconciler) inspect(ctx context.Context, snapshot Snapshot) (Result, error) {
	live, err := r.listManaged(ctx)
	if err != nil {
		return Result{}, err
	}
	desired := make(map[schema.GroupVersionResource]map[string]*unstructured.Unstructured, len(managedGVRs))
	for _, resource := range snapshot.Resources {
		if desired[resource.GVR] == nil {
			desired[resource.GVR] = make(map[string]*unstructured.Unstructured)
		}
		desired[resource.GVR][resource.Object.GetName()] = resource.Object
	}
	result := Result{Desired: len(snapshot.Resources), Actual: len(live)}
	seen := make(map[schema.GroupVersionResource]map[string]bool, len(managedGVRs))
	for _, resource := range live {
		if seen[resource.GVR] == nil {
			seen[resource.GVR] = make(map[string]bool)
		}
		seen[resource.GVR][resource.Object.GetName()] = true
		want, exists := desired[resource.GVR][resource.Object.GetName()]
		if !exists || !containsDesiredState(resource.Object, want) {
			result.Drift++
		}
	}
	for gvr, resources := range desired {
		for name := range resources {
			if !seen[gvr][name] {
				result.Drift++
			}
		}
	}
	return result, nil
}

func (r *Reconciler) listManaged(ctx context.Context) ([]Resource, error) {
	var resources []Resource
	for _, gvr := range managedGVRs {
		objects, err := r.client.ListManaged(ctx, gvr, r.targetNamespace)
		if err != nil {
			return nil, fmt.Errorf("list managed %s: %w", gvr.Resource, err)
		}
		for _, object := range objects {
			resources = append(resources, Resource{Object: object, GVR: gvr})
		}
	}
	return resources, nil
}

func desiredNames(resources []Resource) map[schema.GroupVersionResource]map[string]struct{} {
	desired := make(map[schema.GroupVersionResource]map[string]struct{}, len(managedGVRs))
	for _, resource := range resources {
		if desired[resource.GVR] == nil {
			desired[resource.GVR] = make(map[string]struct{})
		}
		desired[resource.GVR][resource.Object.GetName()] = struct{}{}
	}
	return desired
}

func containsDesiredState(live, desired *unstructured.Unstructured) bool {
	if live.GetAPIVersion() != desired.GetAPIVersion() || live.GetKind() != desired.GetKind() ||
		live.GetNamespace() != desired.GetNamespace() || live.GetName() != desired.GetName() {
		return false
	}
	for key, value := range desired.GetLabels() {
		if live.GetLabels()[key] != value {
			return false
		}
	}
	for key, value := range desired.GetAnnotations() {
		if live.GetAnnotations()[key] != value {
			return false
		}
	}
	liveSpec, _, _ := unstructured.NestedFieldNoCopy(live.Object, "spec")
	desiredSpec, _, _ := unstructured.NestedFieldNoCopy(desired.Object, "spec")
	return containsValue(liveSpec, desiredSpec)
}

func containsValue(live, desired any) bool {
	switch wanted := desired.(type) {
	case map[string]any:
		current, ok := live.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range wanted {
			if !containsValue(current[key], value) {
				return false
			}
		}
		return true
	case []any:
		current, ok := live.([]any)
		if !ok || len(current) != len(wanted) {
			return false
		}
		for index := range wanted {
			if !containsValue(current[index], wanted[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(live, desired)
	}
}
