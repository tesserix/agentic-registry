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
	"fmt"
	"time"

	"github.com/tesserix/agentic-registry/internal/selector"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// ErrNotFound is returned when an artifact (or tag) does not exist.
var ErrNotFound = errors.New("not found")

// ErrImmutableTag is returned when a publish would change the content of an
// already-published version tag (every tag except the floating "latest").
// This gives artifact-repository semantics: a released version is permanent.
var ErrImmutableTag = errors.New("immutable tag: this version is already published with different content")

// ErrNameConflict is the sentinel for a namespace-scoped name collision: a
// name is already owned by a DIFFERENT kind within the same namespace. Object
// names are unique across all kinds within a namespace (a namespace maps to an
// org or an org-team), so a Skill named "foo" forbids a Tool/MCPServer/Prompt
// named "foo" in that same namespace. Re-publishing the SAME kind is allowed —
// that is versioning, not a collision. Match it with errors.Is(err,
// ErrNameConflict); the concrete *NameConflictError carries the details.
var ErrNameConflict = errors.New("name already in use by another kind in this namespace")

// NameConflictError describes a rejected name claim with enough context for a
// human-meaningful API message: which name/namespace collided, the kind the
// publisher tried to use, and the kind + ARN that already owns the name.
type NameConflictError struct {
	Namespace string
	Name      string
	WantKind  v1alpha1.Kind // the kind the caller tried to publish
	OwnerKind v1alpha1.Kind // the kind that already owns the name
	OwnerARN  string        // canonical ARN of the current owner
}

func (e *NameConflictError) Error() string {
	return fmt.Sprintf(
		"name %q is already in use in namespace %q by a %s (%s). "+
			"Object names must be unique across all kinds within a namespace, so this "+
			"%s cannot be created. Choose a different metadata.name, publish to another "+
			"namespace, or publish a new version of the existing %s instead.",
		e.Name, e.Namespace, e.OwnerKind, e.OwnerARN, e.WantKind, e.OwnerKind,
	)
}

// Is lets errors.Is(err, ErrNameConflict) match a *NameConflictError.
func (e *NameConflictError) Is(target error) bool { return target == ErrNameConflict }

// newNameConflict builds the typed error from the incoming object and the
// owning kind/tenant, computing the owner's canonical ARN.
func newNameConflict(want v1alpha1.Object, ownerKind v1alpha1.Kind, ownerTenant string) *NameConflictError {
	owner := v1alpha1.Object{
		Kind: ownerKind,
		Metadata: v1alpha1.ObjectMeta{
			Namespace: want.Metadata.Namespace,
			Name:      want.Metadata.Name,
			TenantID:  ownerTenant,
		},
	}
	return &NameConflictError{
		Namespace: want.Metadata.Namespace,
		Name:      want.Metadata.Name,
		WantKind:  want.Kind,
		OwnerKind: ownerKind,
		OwnerARN:  owner.ARN(),
	}
}

// ListOptions parameterizes a List query.
type ListOptions struct {
	Kind      v1alpha1.Kind
	Namespace string // "" or "all" => across all namespaces
	Selector  selector.Selector
	Search    string // case-insensitive substring on name/title/description
	// PreRanked signals that candidates already arrive in relevance order
	// (e.g. a pgvector cosine sort): the pipeline then preserves that order and
	// skips the substring filter, so semantic recall isn't clipped to literal
	// substrings. Set by the Postgres store when vector search handles Search.
	PreRanked bool
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

// Revision is one immutable, append-only entry in an artifact's audit timeline.
// A new revision is recorded on every content change — including overwrites of
// the floating "latest" tag — so the full history is preserved even where the
// live row is mutable. Revision numbers are monotonic per (kind,ns,name,tag).
type Revision struct {
	Tag        string    `json:"tag"`
	Revision   int64     `json:"revision"`
	Digest     string    `json:"digest"` // "sha256:<hex>"
	Visibility string    `json:"visibility,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
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

	// ListRevisions returns the artifact's append-only audit timeline across all
	// tags, newest first. Empty (not ErrNotFound) when there is no history.
	ListRevisions(ctx context.Context, kind v1alpha1.Kind, namespace, name string) ([]Revision, error)

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
