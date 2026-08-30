package gatewaysync

import (
	"sync"
	"time"
)

// State is the concurrency-safe controller status exposed to operators.
type State struct {
	Mode                    Mode      `json:"mode"`
	Leader                  bool      `json:"leader"`
	Ready                   bool      `json:"ready"`
	ETag                    string    `json:"etag,omitempty"`
	Digest                  string    `json:"digest,omitempty"`
	ResourceCount           int       `json:"resourceCount"`
	DesiredResources        int       `json:"desiredResources"`
	ActualResources         int       `json:"actualResources"`
	DriftResources          int       `json:"driftResources"`
	LastAttempt             time.Time `json:"lastAttempt,omitempty"`
	LastRegistrySuccess     time.Time `json:"lastRegistrySuccess,omitempty"`
	LastReconcileSuccess    time.Time `json:"lastReconcileSuccess,omitempty"`
	LastRegistryError       string    `json:"lastRegistryError,omitempty"`
	LastReconcileError      string    `json:"lastReconcileError,omitempty"`
	RegistryErrorsTotal     uint64    `json:"registryErrorsTotal"`
	ReconcileErrorsTotal    uint64    `json:"reconcileErrorsTotal"`
	ReconcileSuccessesTotal uint64    `json:"reconcileSuccessesTotal"`
}

// Status owns the observable state shared by leader election, reconciliation, and HTTP.
type Status struct {
	mu    sync.RWMutex
	state State
}

// NewStatus initializes controller status for one deployment mode.
func NewStatus(mode Mode) *Status {
	return &Status{state: State{Mode: mode}}
}

// Snapshot returns an immutable point-in-time status copy.
func (s *Status) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Status) setReady(ready bool) {
	s.SetReady(ready)
}

// SetReady records whether this replica is initialized and able to assume leadership.
func (s *Status) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Ready = ready
}

// SetLeader records whether this replica currently owns the reconciliation lease.
func (s *Status) SetLeader(leader bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Leader = leader
}

func (s *Status) recordAttempt(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastAttempt = now
}

func (s *Status) recordRegistrySuccess(now time.Time, snapshot Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastRegistrySuccess = now
	s.state.LastRegistryError = ""
	s.state.ETag = snapshot.ETag
	s.state.Digest = snapshot.Digest
	s.state.ResourceCount = snapshot.ResourceCount
}

func (s *Status) recordRegistryError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastRegistryError = err.Error()
	s.state.RegistryErrorsTotal++
}

func (s *Status) recordReconcileSuccess(now time.Time, result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastReconcileSuccess = now
	s.state.LastReconcileError = ""
	s.state.DesiredResources = result.Desired
	s.state.ActualResources = result.Actual
	s.state.DriftResources = result.Drift
	s.state.ReconcileSuccessesTotal++
}

func (s *Status) recordReconcileError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastReconcileError = err.Error()
	s.state.ReconcileErrorsTotal++
}
