package store

import (
	"context"
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const defaultLimit = 50

// Memory is an in-memory Store. It is the zero-dependency default so the server
// runs with `make run` and no Postgres. Not durable; for dev/test/demos.
type Memory struct {
	mu            sync.RWMutex
	objs          map[string]v1alpha1.Object // key: kind|ns|name|tag
	revs          map[string][]Revision      // key: kind|ns|name (all tags)
	now           func() time.Time
	immutableTags bool
	autoVersion   bool
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{objs: map[string]v1alpha1.Object{}, revs: map[string][]Revision{}, now: time.Now}
}

func revKey(kind v1alpha1.Kind, ns, name string) string {
	return string(kind) + "|" + ns + "|" + name
}

func key(kind v1alpha1.Kind, ns, name, tag string) string {
	return string(kind) + "|" + ns + "|" + name + "|" + tag
}

func (m *Memory) Apply(_ context.Context, obj v1alpha1.Object) (v1alpha1.Object, bool, error) {
	obj = obj.Normalized()

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()

	// Namespace-scoped name uniqueness across all kinds: a live object of a
	// DIFFERENT kind already holding this (namespace, name) blocks the publish.
	// Re-publishing the same kind falls through (versioning). Soft-deleted rows
	// release the name, so it can be reclaimed by any kind.
	for _, o := range m.objs {
		if o.Metadata.Namespace == obj.Metadata.Namespace &&
			o.Metadata.Name == obj.Metadata.Name &&
			o.Kind != obj.Kind &&
			o.Metadata.DeletionTimestamp == nil {
			return v1alpha1.Object{}, false, newNameConflict(obj, o.Kind, o.Metadata.TenantID)
		}
	}

	// Auto-assign the next semver when no explicit version was given, so each
	// publish is a unique immutable release (v0.0.1, v0.0.2, …) — unless the
	// content is byte-identical to the newest one, which is a no-op re-apply.
	if m.autoVersion && autoVersionRequested(obj.Metadata.Tag) {
		var tags []string
		var latest v1alpha1.Object
		for _, o := range m.objs {
			if o.Kind != obj.Kind || o.Metadata.Namespace != obj.Metadata.Namespace || o.Metadata.Name != obj.Metadata.Name {
				continue
			}
			tags = append(tags, o.Metadata.Tag)
			if o.Metadata.DeletionTimestamp != nil {
				continue
			}
			if latest.Metadata.Tag == "" || o.Metadata.UpdatedAt.After(*latest.Metadata.UpdatedAt) {
				latest = o
			}
		}
		if reusesVersion(obj, latest.Metadata.Tag, latest.Metadata.ContentHash) {
			obj.Metadata.Tag = latest.Metadata.Tag
		} else {
			obj.Metadata.Tag = nextVersion(tags)
		}
	}

	hash := obj.ContentHash()
	k := key(obj.Kind, obj.Metadata.Namespace, obj.Metadata.Name, obj.Metadata.Tag)
	prev, existed := m.objs[k]

	// Cross-tenant overwrite guard (mirrors postgres.go): an upsert onto an
	// artifact owned by a DIFFERENT tenant would let a writer on tenant B hijack
	// tenant A's same-(kind,namespace,name,tag) artifact. Re-applying to your own
	// tenant's artifact is normal versioning and proceeds.
	if existed && prev.Metadata.TenantID != obj.Metadata.TenantID {
		return v1alpha1.Object{}, false, ErrTenantConflict
	}

	// Immutable version tags: a published version may not change content
	// (the floating "latest" tag is exempt).
	if m.immutableTags && existed && obj.Metadata.Tag != v1alpha1.DefaultTag &&
		prev.Metadata.DeletionTimestamp == nil && prev.Metadata.ContentHash != hash {
		return v1alpha1.Object{}, false, ErrImmutableTag
	}

	obj.Metadata.ContentHash = hash
	obj.Metadata.UpdatedAt = &now
	if existed {
		obj.Metadata.UID = prev.Metadata.UID
		obj.Metadata.CreatedAt = prev.Metadata.CreatedAt
		// Re-apply of identical content: refresh updatedAt only.
	} else {
		obj.Metadata.UID = uuid.NewString()
		obj.Metadata.CreatedAt = &now
	}
	obj.Metadata.DeletionTimestamp = nil
	if obj.Status == nil {
		obj.Status = map[string]interface{}{}
	}
	obj.Status["status"] = "active"

	m.objs[k] = obj

	// Append an immutable revision on every content change (new or changed
	// hash). Identical re-applies don't add a revision.
	if !existed || prev.Metadata.ContentHash != hash {
		rk := revKey(obj.Kind, obj.Metadata.Namespace, obj.Metadata.Name)
		var maxForTag int64
		for _, r := range m.revs[rk] {
			if r.Tag == obj.Metadata.Tag && r.Revision > maxForTag {
				maxForTag = r.Revision
			}
		}
		m.revs[rk] = append(m.revs[rk], Revision{
			Tag:        obj.Metadata.Tag,
			Revision:   maxForTag + 1,
			Digest:     "sha256:" + hash,
			Visibility: string(obj.Metadata.Visibility),
			CreatedAt:  now,
		})
	}
	return obj, !existed, nil
}

func (m *Memory) ListRevisions(_ context.Context, kind v1alpha1.Kind, ns, name string) ([]Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.revs[revKey(kind, ns, name)]
	out := make([]Revision, len(src))
	copy(out, src)
	// Newest first.
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) Get(_ context.Context, kind v1alpha1.Kind, ns, name, tag string) (v1alpha1.Object, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tag == "" || tag == v1alpha1.DefaultTag {
		return m.latestLocked(kind, ns, name)
	}
	o, ok := m.objs[key(kind, ns, name, tag)]
	if !ok || o.Metadata.DeletionTimestamp != nil {
		return v1alpha1.Object{}, ErrNotFound
	}
	return o, nil
}

func (m *Memory) latestLocked(kind v1alpha1.Kind, ns, name string) (v1alpha1.Object, error) {
	var best v1alpha1.Object
	found := false
	for _, o := range m.objs {
		if o.Kind != kind || o.Metadata.Namespace != ns || o.Metadata.Name != name {
			continue
		}
		if o.Metadata.DeletionTimestamp != nil {
			continue
		}
		if !found || o.Metadata.UpdatedAt.After(*best.Metadata.UpdatedAt) {
			best, found = o, true
		}
	}
	if !found {
		return v1alpha1.Object{}, ErrNotFound
	}
	return best, nil
}

func (m *Memory) List(_ context.Context, opts ListOptions) (ListResult, error) {
	m.mu.RLock()
	candidates := make([]v1alpha1.Object, 0, len(m.objs))
	for _, o := range m.objs {
		candidates = append(candidates, o)
	}
	m.mu.RUnlock()
	return page(candidates, opts), nil
}

// page is the shared discovery pipeline used by every Store backend: stable
// sort → filter (visibility/RBAC pre-filter THEN selector) → optional collapse
// to latest tag → cursor pagination. Centralizing it guarantees identical
// semantics across the in-memory and Postgres stores.
func page(candidates []v1alpha1.Object, opts ListOptions) ListResult {
	// PreRanked input already arrives in relevance order (e.g. pgvector cosine)
	// and must not be re-sorted; otherwise apply the stable name sort.
	if !opts.PreRanked {
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i].Metadata, candidates[j].Metadata
			if a.Namespace != b.Namespace {
				return a.Namespace < b.Namespace
			}
			if a.Name != b.Name {
				return a.Name < b.Name
			}
			return a.Tag < b.Tag
		})
	}

	matched := filter(candidates, opts)
	if opts.LatestOnly {
		if opts.PreRanked {
			matched = collapseLatestPreserveOrder(matched)
		} else {
			matched = collapseLatest(matched)
		}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	offset := decodeCursor(opts.Cursor)
	if offset > len(matched) {
		offset = len(matched)
	}
	end := offset + limit
	next := ""
	if end < len(matched) {
		next = encodeCursor(end)
	} else {
		end = len(matched)
	}
	return ListResult{Items: matched[offset:end], NextCursor: next}
}

// filter applies, in order: kind, namespace, deletion, updated-since, the
// visibility/RBAC pre-filter (CanRead), then the label selector, then search.
func filter(in []v1alpha1.Object, opts ListOptions) []v1alpha1.Object {
	out := in[:0:0]
	allNS := opts.Namespace == "" || opts.Namespace == "all"
	for _, o := range in {
		if opts.Kind != "" && o.Kind != opts.Kind {
			continue
		}
		if !allNS && o.Metadata.Namespace != opts.Namespace {
			continue
		}
		if o.Metadata.DeletionTimestamp != nil && !opts.IncludeDeleted {
			continue
		}
		if opts.UpdatedSince != nil && o.Metadata.UpdatedAt != nil && o.Metadata.UpdatedAt.Before(*opts.UpdatedSince) {
			continue
		}
		// SECURITY: visibility/RBAC pre-filter BEFORE the selector.
		if opts.CanRead != nil && !opts.CanRead(o) {
			continue
		}
		if !opts.Selector.Empty() && !opts.Selector.Matches(o.Metadata.Labels) {
			continue
		}
		// PreRanked results were already ranked by relevance upstream; applying
		// the substring filter here would discard semantically-near matches.
		if opts.Search != "" && !opts.PreRanked && !matchesSearch(o, opts.Search) {
			continue
		}
		out = append(out, o)
	}
	return out
}

func matchesSearch(o v1alpha1.Object, q string) bool {
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(o.Metadata.Name), q) {
		return true
	}
	for _, f := range []string{"title", "description"} {
		if v, ok := o.Spec[f].(string); ok && strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	return false
}

// collapseLatest keeps the newest tag per (namespace,name).
func collapseLatest(in []v1alpha1.Object) []v1alpha1.Object {
	best := map[string]v1alpha1.Object{}
	for _, o := range in {
		k := o.Metadata.Namespace + "|" + o.Metadata.Name
		cur, ok := best[k]
		if !ok || (o.Metadata.UpdatedAt != nil && cur.Metadata.UpdatedAt != nil && o.Metadata.UpdatedAt.After(*cur.Metadata.UpdatedAt)) {
			best[k] = o
		}
	}
	out := make([]v1alpha1.Object, 0, len(best))
	for _, o := range best {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Metadata.Namespace != out[j].Metadata.Namespace {
			return out[i].Metadata.Namespace < out[j].Metadata.Namespace
		}
		return out[i].Metadata.Name < out[j].Metadata.Name
	})
	return out
}

// collapseLatestPreserveOrder keeps the first-seen row per (namespace,name)
// without re-sorting. The input is in relevance order (closest first), so the
// first occurrence is the best match for that artifact and the overall ranking
// is preserved.
func collapseLatestPreserveOrder(in []v1alpha1.Object) []v1alpha1.Object {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, o := range in {
		k := o.Metadata.Namespace + "|" + o.Metadata.Name
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, o)
	}
	return out
}

func (m *Memory) ListTags(_ context.Context, kind v1alpha1.Kind, ns, name string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	type tagged struct {
		tag string
		at  time.Time
	}
	var tags []tagged
	for _, o := range m.objs {
		if o.Kind == kind && o.Metadata.Namespace == ns && o.Metadata.Name == name && o.Metadata.DeletionTimestamp == nil {
			at := time.Time{}
			if o.Metadata.UpdatedAt != nil {
				at = *o.Metadata.UpdatedAt
			}
			tags = append(tags, tagged{o.Metadata.Tag, at})
		}
	}
	if len(tags) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].at.After(tags[j].at) })
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.tag
	}
	return out, nil
}

func (m *Memory) Delete(_ context.Context, kind v1alpha1.Kind, ns, name, tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(kind, ns, name, tag)
	o, ok := m.objs[k]
	if !ok {
		return ErrNotFound
	}
	now := m.now().UTC()
	o.Metadata.DeletionTimestamp = &now
	if o.Status == nil {
		o.Status = map[string]interface{}{}
	}
	o.Status["status"] = "deleted"
	m.objs[k] = o
	return nil
}

func (m *Memory) MergeStatus(_ context.Context, kind v1alpha1.Kind, ns, name, tag string, patch map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objs[key(kind, ns, name, tag)]
	if !ok {
		return ErrNotFound
	}
	merged := map[string]interface{}{}
	for k, v := range o.Status {
		merged[k] = v
	}
	for k, v := range patch {
		merged[k] = v
	}
	o.Status = merged
	m.objs[key(kind, ns, name, tag)] = o
	return nil
}

func (m *Memory) SetStatus(_ context.Context, kind v1alpha1.Kind, ns, name, tag, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(kind, ns, name, tag)
	o, ok := m.objs[k]
	if !ok {
		return ErrNotFound
	}
	if o.Status == nil {
		o.Status = map[string]interface{}{}
	}
	o.Status["status"] = status
	if status == "deleted" {
		now := m.now().UTC()
		o.Metadata.DeletionTimestamp = &now
	}
	m.objs[k] = o
	return nil
}

func (m *Memory) Counts(ctx context.Context, ns string, canRead func(v1alpha1.Object) bool) (map[v1alpha1.Kind]int, error) {
	res := ListResult{}
	counts := map[v1alpha1.Kind]int{}
	for _, k := range v1alpha1.AllKinds {
		r, err := m.List(ctx, ListOptions{Kind: k, Namespace: ns, LatestOnly: true, CanRead: canRead, Limit: maxScanRows})
		if err != nil {
			return nil, err
		}
		counts[k] = len(r.Items)
		res = r
	}
	_ = res
	return counts, nil
}

func (m *Memory) Health(context.Context) error { return nil }
func (m *Memory) Close() error                 { return nil }

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(c string) int {
	if c == "" {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
