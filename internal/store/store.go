// Package store persists and queries registry artifacts. Two implementations
// exist: an in-memory store (the zero-dependency default, used for local dev
// and tests) and a Postgres store (production).
//
// The store enforces ordering of the discovery pipeline: a visibility/RBAC
// pre-filter (supplied by the caller as CanRead) is applied to every candidate
// BEFORE the label selector and pagination. This guarantees the selector can
// never widen a caller's visibility — it is discovery convenience, not a
// security boundary.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/tesserix/agentic-registry/internal/selector"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// ErrNotFound is returned when an artifact (or tag) does not exist.
var ErrNotFound = errors.New("not found")

// ListOptions parameterizes a List query.
type ListOptions struct {
	Kind      v1alpha1.Kind
	Namespace string // "" or "all" => across all namespaces
	Selector  selector.Selector
	Search    string // case-insensitive substring on name/title/description
	// LatestOnly collapses to the newest tag per (namespace,name). Collections
	// (devai /v0/* lists) want this true; tag listings want it false.
	LatestOnly     bool
	IncludeDeleted bool
	UpdatedSince   *time.Time
	Limit          int    // 0 => server default
	Cursor         string // opaque; empty => first page
	// CanRead is the visibility/RBAC pre-filter. nil means allow everything
	// (e.g. internal callers). Applied before Selector and pagination.
	CanRead func(v1alpha1.Object) bool
}

// ListResult is a page of artifacts plus an opaque cursor for the next page.
type ListResult struct {
	Items      []v1alpha1.Object
	NextCursor string
}

// Store is the persistence contract.
type Store interface {
	// Apply upserts an artifact by (kind, namespace, name, tag). It stamps
	// server-managed metadata (uid, timestamps, contentHash). created reports
	// whether a new (name,tag) row was inserted. Re-applying identical content
	// is a no-op (same contentHash) but refreshes updatedAt.
	Apply(ctx context.Context, obj v1alpha1.Object) (result v1alpha1.Object, created bool, err error)

	// Get returns one artifact. tag "" or "latest" resolves the newest tag.
	Get(ctx context.Context, kind v1alpha1.Kind, namespace, name, tag string) (v1alpha1.Object, error)

	// List returns a filtered, paginated page of artifacts.
	List(ctx context.Context, opts ListOptions) (ListResult, error)

	// ListTags returns all tags for an artifact, newest first.
	ListTags(ctx context.Context, kind v1alpha1.Kind, namespace, name string) ([]string, error)

	// Delete soft-deletes one tag (sets deletionTimestamp / status=deleted).
	Delete(ctx context.Context, kind v1alpha1.Kind, namespace, name, tag string) error

	// SetStatus transitions an artifact's lifecycle status (active|deprecated|
	// deleted) — used by the MCP /v0.1 status endpoints.
	SetStatus(ctx context.Context, kind v1alpha1.Kind, namespace, name, tag, status string) error

	// Counts returns the number of readable artifacts per kind in a namespace.
	Counts(ctx context.Context, namespace string, canRead func(v1alpha1.Object) bool) (map[v1alpha1.Kind]int, error)

	// Health pings the backing store.
	Health(ctx context.Context) error

	// Close releases resources.
	Close() error
}
