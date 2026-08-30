package gatewaysync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	dynamicinformer "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// DriftWatcher observes Registry-owned AgentGateway objects for live drift.
type DriftWatcher struct {
	factory dynamicinformer.DynamicSharedInformerFactory
}

// NewDriftWatcher constructs namespace-scoped informers for the managed resource kinds.
func NewDriftWatcher(client dynamic.Interface, namespace string, resyncPeriod time.Duration, onDrift func()) (*DriftWatcher, error) {
	if client == nil {
		return nil, errors.New("dynamic Kubernetes client is required")
	}
	if namespace == "" {
		return nil, errors.New("watch namespace is required")
	}
	if resyncPeriod <= 0 {
		return nil, errors.New("watch resync period must be positive")
	}
	if onDrift == nil {
		return nil, errors.New("drift callback is required")
	}
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		client,
		resyncPeriod,
		namespace,
		func(options *metav1.ListOptions) {
			options.LabelSelector = managedByLabel + "=" + managedByValue
		},
	)
	for _, gvr := range managedGVRs {
		informer := factory.ForResource(gvr).Informer()
		if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(any) {
				onDrift()
			},
			UpdateFunc: func(oldObject, newObject any) {
				oldResource, oldOK := oldObject.(*unstructured.Unstructured)
				newResource, newOK := newObject.(*unstructured.Unstructured)
				if oldOK && newOK && driftRelevantChange(oldResource, newResource) {
					onDrift()
				}
			},
			DeleteFunc: func(any) {
				onDrift()
			},
		}); err != nil {
			return nil, fmt.Errorf("register %s drift handler: %w", gvr.Resource, err)
		}
	}
	return &DriftWatcher{factory: factory}, nil
}

// Run watches until cancellation and reports failure to establish every cache.
func (w *DriftWatcher) Run(ctx context.Context) error {
	w.factory.Start(ctx.Done())
	for gvr, synced := range w.factory.WaitForCacheSync(ctx.Done()) {
		if !synced && ctx.Err() == nil {
			return fmt.Errorf("synchronize %s informer cache", gvr.Resource)
		}
	}
	<-ctx.Done()
	return nil
}

func driftRelevantChange(oldObject, newObject *unstructured.Unstructured) bool {
	return oldObject.GetGeneration() != newObject.GetGeneration() ||
		!reflect.DeepEqual(oldObject.GetLabels(), newObject.GetLabels()) ||
		!reflect.DeepEqual(oldObject.GetAnnotations(), newObject.GetAnnotations())
}
