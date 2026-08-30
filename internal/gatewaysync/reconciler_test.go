package gatewaysync

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestReconcilerAppliesAcceptedSnapshotBeforePruning(t *testing.T) {
	t.Parallel()

	desired := testBackend("desired", false)
	stale := testBackend("stale", true)
	client := newFakeKubernetes(stale)
	client.acceptApplied = true
	reconciler, err := NewReconciler(client, ReconcilerOptions{
		TargetNamespace:        "agentgateway-system",
		Mode:                   ModeActive,
		Prune:                  true,
		AcceptanceTimeout:      50 * time.Millisecond,
		AcceptancePollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := reconciler.Reconcile(context.Background(), testSnapshot(desired))
	if err != nil {
		t.Fatal(err)
	}
	if result.Desired != 1 || result.Actual != 1 || result.Applied != 1 || result.Pruned != 1 || result.Drift != 0 {
		t.Fatalf("result: got %#v", result)
	}
	if _, exists := client.objects[objectKey(stale)]; exists {
		t.Fatal("stale Registry-owned backend was not pruned")
	}
	applyIndex := operationIndex(client.operations, "apply/")
	deleteIndex := operationIndex(client.operations, "delete/")
	if applyIndex < 0 || deleteIndex < 0 || applyIndex >= deleteIndex {
		t.Fatalf("operations not ordered apply-before-delete: %v", client.operations)
	}
}

func TestReconcilerDoesNotPruneBeforeAcceptance(t *testing.T) {
	t.Parallel()

	desired := testBackend("desired", false)
	stale := testBackend("stale", true)
	client := newFakeKubernetes(stale)
	reconciler, err := NewReconciler(client, ReconcilerOptions{
		TargetNamespace:        "agentgateway-system",
		Mode:                   ModeActive,
		Prune:                  true,
		AcceptanceTimeout:      5 * time.Millisecond,
		AcceptancePollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := reconciler.Reconcile(context.Background(), testSnapshot(desired))
	if err == nil || !strings.Contains(err.Error(), "wait for AgentGateway acceptance") {
		t.Fatalf("error: got %v, want acceptance timeout", err)
	}
	if result.Pruned != 0 {
		t.Fatalf("pruned before acceptance: %#v", result)
	}
	if _, exists := client.objects[objectKey(stale)]; !exists {
		t.Fatal("stale backend was deleted before desired state was accepted")
	}
	if operationIndex(client.operations, "delete/") >= 0 {
		t.Fatalf("delete operation occurred before acceptance: %v", client.operations)
	}
}

func TestReconcilerShadowModeReportsDriftWithoutMutation(t *testing.T) {
	t.Parallel()

	desired := testBackend("desired", false)
	stale := testBackend("stale", true)
	client := newFakeKubernetes(stale)
	reconciler, err := NewReconciler(client, ReconcilerOptions{
		TargetNamespace:        "agentgateway-system",
		Mode:                   ModeShadow,
		Prune:                  true,
		AcceptanceTimeout:      time.Second,
		AcceptancePollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := reconciler.Reconcile(context.Background(), testSnapshot(desired))
	if err != nil {
		t.Fatal(err)
	}
	if result.Desired != 1 || result.Actual != 1 || result.Drift != 2 || result.Applied != 0 || result.Pruned != 0 {
		t.Fatalf("result: got %#v", result)
	}
	if operationIndex(client.operations, "apply/") >= 0 || operationIndex(client.operations, "delete/") >= 0 {
		t.Fatalf("shadow mode mutated Kubernetes: %v", client.operations)
	}
}

type fakeKubernetes struct {
	objects       map[string]*unstructured.Unstructured
	operations    []string
	acceptApplied bool
}

func newFakeKubernetes(objects ...*unstructured.Unstructured) *fakeKubernetes {
	client := &fakeKubernetes{objects: make(map[string]*unstructured.Unstructured)}
	for _, object := range objects {
		client.objects[objectKey(object)] = object.DeepCopy()
	}
	return client
}

func (f *fakeKubernetes) Apply(_ context.Context, resource Resource) (*unstructured.Unstructured, error) {
	object := resource.Object.DeepCopy()
	if f.acceptApplied && object.GetKind() == "AgentgatewayBackend" {
		object.Object["status"] = map[string]any{
			"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}},
		}
	}
	f.objects[objectKey(object)] = object
	f.operations = append(f.operations, "apply/"+object.GetName())
	return object.DeepCopy(), nil
}

func (f *fakeKubernetes) Get(_ context.Context, resource Resource) (*unstructured.Unstructured, error) {
	f.operations = append(f.operations, "get/"+resource.Object.GetName())
	object, exists := f.objects[objectKey(resource.Object)]
	if !exists {
		return nil, fmt.Errorf("not found")
	}
	return object.DeepCopy(), nil
}

func (f *fakeKubernetes) ListManaged(_ context.Context, gvr schema.GroupVersionResource, namespace string) ([]*unstructured.Unstructured, error) {
	f.operations = append(f.operations, "list/"+gvr.Resource)
	var objects []*unstructured.Unstructured
	for _, object := range f.objects {
		if allowedResources[object.GroupVersionKind()] != gvr || object.GetNamespace() != namespace {
			continue
		}
		if object.GetLabels()[managedByLabel] == managedByValue {
			objects = append(objects, object.DeepCopy())
		}
	}
	return objects, nil
}

func (f *fakeKubernetes) Delete(_ context.Context, gvr schema.GroupVersionResource, namespace, name string, _ types.UID, _ string) error {
	f.operations = append(f.operations, "delete/"+name)
	for key, object := range f.objects {
		if allowedResources[object.GroupVersionKind()] == gvr && object.GetNamespace() == namespace && object.GetName() == name {
			delete(f.objects, key)
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func testBackend(name string, accepted bool) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "agentgateway.dev/v1alpha1",
		"kind":       "AgentgatewayBackend",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "agentgateway-system",
			"labels": map[string]any{
				managedByLabel: managedByValue,
			},
		},
		"spec": map[string]any{"a2a": map[string]any{"host": name + ".example", "port": int64(8080)}},
	}}
	object.SetUID(types.UID(name + "-uid"))
	object.SetResourceVersion("1")
	if accepted {
		object.Object["status"] = map[string]any{
			"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}},
		}
	}
	return object
}

func testSnapshot(objects ...*unstructured.Unstructured) Snapshot {
	resources := make([]Resource, 0, len(objects))
	for _, object := range objects {
		resources = append(resources, Resource{
			Object: object.DeepCopy(),
			GVR:    allowedResources[object.GroupVersionKind()],
		})
	}
	return Snapshot{ETag: `"sha256:test"`, Digest: "sha256:test", ResourceCount: len(resources), Resources: resources}
}

func objectKey(object *unstructured.Unstructured) string {
	return object.GroupVersionKind().String() + "/" + object.GetNamespace() + "/" + object.GetName()
}

func operationIndex(operations []string, prefix string) int {
	for index, operation := range operations {
		if strings.HasPrefix(operation, prefix) {
			return index
		}
	}
	return -1
}

var _ KubernetesClient = (*fakeKubernetes)(nil)
