package gatewaysync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestControllerReconcilesLastKnownGoodSnapshotWhenRegistryFails(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot(testBackend("desired", false))
	registry := &fakeRegistryFetcher{responses: []fetchResponse{
		{snapshot: snapshot, modified: true},
		{err: errors.New("registry unavailable")},
	}}
	reconciler := &fakeSnapshotReconciler{calls: make(chan Snapshot, 2)}
	status := NewStatus(ModeActive)
	controller, err := NewController(registry, reconciler, status, ControllerOptions{
		PollInterval:   time.Hour,
		SafetyInterval: 2 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()

	first := receiveSnapshot(t, reconciler.calls)
	if first.Digest != snapshot.Digest {
		t.Fatalf("first digest: got %q", first.Digest)
	}
	controller.TriggerFetch()
	second := receiveSnapshot(t, reconciler.calls)
	if second.Digest != snapshot.Digest || len(second.Resources) != 1 {
		t.Fatalf("cached snapshot: got %#v", second)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller did not stop")
	}
	if registry.callCount() != 2 {
		t.Fatalf("registry calls: got %d", registry.callCount())
	}
	state := status.Snapshot()
	if state.RegistryErrorsTotal != 1 || state.ReconcileSuccessesTotal != 2 {
		t.Fatalf("status: got %#v", state)
	}
}

func TestControllerConditionalPollSkipsUnchangedKubernetesApply(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot(testBackend("desired", false))
	registry := &fakeRegistryFetcher{responses: []fetchResponse{
		{snapshot: snapshot, modified: true},
		{
			snapshot: Snapshot{
				ETag:          snapshot.ETag,
				Digest:        snapshot.Digest,
				ResourceCount: snapshot.ResourceCount,
			},
			modified: false,
		},
	}}
	reconciler := &fakeSnapshotReconciler{calls: make(chan Snapshot, 2)}
	controller, err := NewController(registry, reconciler, NewStatus(ModeActive), ControllerOptions{
		PollInterval:   time.Minute,
		SafetyInterval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	controller.sync(context.Background(), true, true)
	receiveSnapshot(t, reconciler.calls)
	controller.sync(context.Background(), true, false)
	select {
	case unexpected := <-reconciler.calls:
		t.Fatalf("unchanged conditional poll reconciled %#v", unexpected)
	default:
	}
}

type fetchResponse struct {
	snapshot Snapshot
	modified bool
	err      error
}

type fakeRegistryFetcher struct {
	mu        sync.Mutex
	responses []fetchResponse
	calls     int
}

func (f *fakeRegistryFetcher) Fetch(_ context.Context, _ string) (Snapshot, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := f.calls
	f.calls++
	if index >= len(f.responses) {
		return Snapshot{}, false, errors.New("unexpected fetch")
	}
	response := f.responses[index]
	return response.snapshot, response.modified, response.err
}

func (f *fakeRegistryFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeSnapshotReconciler struct {
	calls chan Snapshot
}

func (f *fakeSnapshotReconciler) Reconcile(_ context.Context, snapshot Snapshot) (Result, error) {
	f.calls <- snapshot
	return Result{Desired: len(snapshot.Resources), Actual: len(snapshot.Resources)}, nil
}

func receiveSnapshot(t *testing.T, calls <-chan Snapshot) Snapshot {
	t.Helper()
	select {
	case snapshot := <-calls:
		return snapshot
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconciliation")
		return Snapshot{}
	}
}

var _ RegistryFetcher = (*fakeRegistryFetcher)(nil)
var _ SnapshotReconciler = (*fakeSnapshotReconciler)(nil)
