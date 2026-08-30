package gatewaysync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// DynamicKubernetes implements reconciliation through the dynamic Kubernetes API.
type DynamicKubernetes struct {
	client       dynamic.Interface
	fieldManager string
}

// NewDynamicKubernetes constructs a namespaced, server-side-apply client.
func NewDynamicKubernetes(client dynamic.Interface, fieldManager string) (*DynamicKubernetes, error) {
	if client == nil {
		return nil, errors.New("dynamic Kubernetes client is required")
	}
	if fieldManager == "" {
		return nil, errors.New("field manager is required")
	}
	return &DynamicKubernetes{client: client, fieldManager: fieldManager}, nil
}

// Apply converges one object with server-side apply and explicit field ownership.
func (c *DynamicKubernetes) Apply(ctx context.Context, resource Resource) (*unstructured.Unstructured, error) {
	body, err := json.Marshal(resource.Object.Object)
	if err != nil {
		return nil, fmt.Errorf("encode apply object: %w", err)
	}
	force := true
	object, err := c.client.Resource(resource.GVR).Namespace(resource.Object.GetNamespace()).Patch(
		ctx,
		resource.Object.GetName(),
		types.ApplyPatchType,
		body,
		metav1.PatchOptions{FieldManager: c.fieldManager, Force: &force},
	)
	if err != nil {
		return nil, fmt.Errorf("server-side apply: %w", err)
	}
	return object, nil
}

// Get reads the live form of one desired resource.
func (c *DynamicKubernetes) Get(ctx context.Context, resource Resource) (*unstructured.Unstructured, error) {
	object, err := c.client.Resource(resource.GVR).Namespace(resource.Object.GetNamespace()).Get(
		ctx,
		resource.Object.GetName(),
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}
	return object, nil
}

// ListManaged returns only Registry-owned resources of one kind.
func (c *DynamicKubernetes) ListManaged(ctx context.Context, gvr schema.GroupVersionResource, namespace string) ([]*unstructured.Unstructured, error) {
	list, err := c.client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	})
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	objects := make([]*unstructured.Unstructured, 0, len(list.Items))
	for index := range list.Items {
		objects = append(objects, list.Items[index].DeepCopy())
	}
	return objects, nil
}

// Delete prunes one previously listed object using UID and resource-version preconditions.
func (c *DynamicKubernetes) Delete(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, uid types.UID, resourceVersion string) error {
	uidValue := uid
	resourceVersionValue := resourceVersion
	err := c.client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			UID:             &uidValue,
			ResourceVersion: &resourceVersionValue,
		},
	})
	if err != nil {
		return fmt.Errorf("delete resource: %w", err)
	}
	return nil
}

var _ KubernetesClient = (*DynamicKubernetes)(nil)
