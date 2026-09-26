package gatewaysync

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// RegistryFetcher obtains conditional, verified Registry snapshots.
type RegistryFetcher interface {
	Fetch(context.Context, string) (Snapshot, bool, error)
}

// SnapshotReconciler converges a verified snapshot into Kubernetes.
type SnapshotReconciler interface {
	Reconcile(context.Context, Snapshot) (Result, error)
}

// ControllerOptions configures periodic and full-safety reconciliation.
type ControllerOptions struct {
	PollInterval   time.Duration
	SafetyInterval time.Duration
}

// Controller coalesces Registry polling and Kubernetes drift events.
type Controller struct {
	registry       RegistryFetcher
	reconciler     SnapshotReconciler
	status         *Status
	pollInterval   time.Duration
	safetyInterval time.Duration
	events         chan struct{}
	fetchRequested atomic.Bool
	forceRequested atomic.Bool
	snapshot       Snapshot
	hasSnapshot    bool
}

// NewController validates options and constructs the reconciliation loop.
func NewController(registry RegistryFetcher, reconciler SnapshotReconciler, status *Status, options ControllerOptions) (*Controller, error) {
	if registry == nil {
		return nil, errors.New("registry fetcher is required")
	}
	if reconciler == nil {
		return nil, errors.New("snapshot reconciler is required")
	}
	if status == nil {
		return nil, errors.New("controller status is required")
	}
	if options.PollInterval <= 0 || options.SafetyInterval <= 0 {
		return nil, errors.New("controller intervals must be positive")
	}
	if options.SafetyInterval < options.PollInterval {
		return nil, errors.New("safety interval must not be shorter than poll interval")
	}
	return &Controller{
		registry:       registry,
		reconciler:     reconciler,
		status:         status,
		pollInterval:   options.PollInterval,
		safetyInterval: options.SafetyInterval,
		events:         make(chan struct{}, 1),
	}, nil
}

// TriggerFetch requests an immediate Registry fetch and reconciliation.
func (c *Controller) TriggerFetch() {
	c.fetchRequested.Store(true)
	c.forceRequested.Store(true)
	c.trigger()
}

// TriggerDrift requests immediate reconciliation from the last-known-good snapshot.
func (c *Controller) TriggerDrift() {
	c.forceRequested.Store(true)
	c.trigger()
}

func (c *Controller) trigger() {
	select {
	case c.events <- struct{}{}:
	default:
	}
}

// Run reconciles until the context is cancelled. Transient failures are recorded and retried.
func (c *Controller) Run(ctx context.Context) error {
	c.status.setReady(true)
	defer c.status.setReady(false)
	c.sync(ctx, true, true)

	poll := time.NewTicker(c.pollInterval)
	defer poll.Stop()
	safety := time.NewTicker(c.safetyInterval)
	defer safety.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
			c.sync(ctx, true, false)
		case <-safety.C:
			c.sync(ctx, true, true)
		case <-c.events:
			c.sync(ctx, c.fetchRequested.Swap(false), c.forceRequested.Swap(false))
		}
	}
}

func (c *Controller) sync(ctx context.Context, fetch, force bool) {
	now := time.Now().UTC()
	c.status.recordAttempt(now)
	modified := false
	if fetch {
		etag := ""
		if c.hasSnapshot {
			etag = c.snapshot.ETag
		}
		var fetched Snapshot
		var err error
		fetched, modified, err = c.registry.Fetch(ctx, etag)
		if err != nil {
			c.status.recordRegistryError(err)
		} else if modified {
			c.snapshot = fetched
			c.hasSnapshot = true
			c.status.recordRegistrySuccess(now, fetched)
		} else if !c.hasSnapshot {
			c.status.recordRegistryError(errors.New("registry returned not-modified without a cached snapshot"))
		} else if fetched.ETag != c.snapshot.ETag || fetched.Digest != c.snapshot.Digest || fetched.ResourceCount != c.snapshot.ResourceCount {
			c.status.recordRegistryError(errors.New("registry not-modified metadata differs from cached snapshot"))
		} else {
			c.status.recordRegistrySuccess(now, fetched)
		}
	}
	if !c.hasSnapshot || (!modified && !force) {
		return
	}
	result, err := c.reconciler.Reconcile(ctx, c.snapshot)
	if err != nil {
		c.status.recordReconcileError(err)
		return
	}
	c.status.recordReconcileSuccess(time.Now().UTC(), result)
}

var _ RegistryFetcher = (*RegistryClient)(nil)
var _ SnapshotReconciler = (*Reconciler)(nil)
