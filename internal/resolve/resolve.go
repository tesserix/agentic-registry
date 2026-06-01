// Package resolve implements the upstream tiers of the capability resolution
// chain (docs/agentic/MCP-HUB.md §5.5): when a tool an MCP server declares is
// not in the registry, try a chain of internet Sources; on a hit, write the
// tool through into the registry (pull-through cache) so the next resolve is a
// local hit. The registry tier itself is handled by the caller before invoking
// this — the Resolver only covers upstream + cache.
package resolve

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// ErrNotFound means a Source does not have the requested ref (try the next one).
var ErrNotFound = errors.New("resolve: not found")

// Ref is a request to resolve one tool by its wire name, on behalf of an MCP
// server (whose name is stamped on the cached Tool so the server's toolSelector
// picks it up immediately).
type Ref struct {
	Name       string // wire/tool name to resolve
	Namespace  string
	ServerName string // the requesting MCPServer (becomes mcp.devai.io/server)
}

// Source is one upstream provider of tool definitions (officialskills, the MCP
// registry, GitHub, a generic HTTP catalog…). Adapter rule: one file per source,
// lazy/config-driven; a miss returns ErrNotFound so the chain continues.
type Source interface {
	Name() string
	Resolve(ctx context.Context, ref Ref) (*v1alpha1.Object, error)
}

// applier is the slice of the store the Resolver needs (write-through). The real
// store.Store satisfies it; tests pass a fake.
type applier interface {
	Apply(ctx context.Context, obj v1alpha1.Object) (v1alpha1.Object, bool, error)
}

// Clock lets tests pin cached-at; production uses time.Now.
type Clock func() time.Time

// Resolver runs the ordered upstream Source chain and pull-through-caches hits.
type Resolver struct {
	store   applier
	sources []Source
	now     Clock
	log     *slog.Logger
}

// New builds a Resolver. With no sources it is a no-op (Resolve always misses) —
// the registry degrades, it doesn't fail.
func New(store applier, sources []Source, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	return &Resolver{store: store, sources: sources, now: time.Now, log: log}
}

// Enabled reports whether any upstream source is configured.
func (r *Resolver) Enabled() bool { return r != nil && len(r.sources) > 0 }

// Resolve walks the source chain. On the first hit it normalizes the result into
// a cache Tool, writes it through to the registry, and returns the persisted
// object so the caller can bind it immediately. Returns ErrNotFound when no
// source has the ref. A cache-write failure still returns the resolved object
// (serve now, cache later) — never fail a resolve because the cache write did.
func (r *Resolver) Resolve(ctx context.Context, ref Ref) (*v1alpha1.Object, error) {
	if !r.Enabled() {
		return nil, ErrNotFound
	}
	for _, src := range r.sources {
		obj, err := src.Resolve(ctx, ref)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			r.log.Warn("resolve: source error", "source", src.Name(), "ref", ref.Name, "err", err)
			continue
		}
		cached := r.cacheTool(src.Name(), ref, obj)
		applied, _, aerr := r.store.Apply(ctx, cached)
		if aerr != nil {
			r.log.Warn("resolve: cache write failed", "ref", ref.Name, "err", aerr)
			return &cached, nil
		}
		r.log.Info("resolve: pulled + cached tool", "ref", ref.Name, "source", src.Name())
		return &applied, nil
	}
	return nil, ErrNotFound
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(v string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(v), "-"), "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s
}

// cacheTool normalizes an upstream result into a registry Tool with cache
// provenance, stamped for the requesting server so its toolSelector binds it.
func (r *Resolver) cacheTool(source string, ref Ref, found *v1alpha1.Object) v1alpha1.Object {
	spec := map[string]interface{}{}
	if found != nil && found.Spec != nil {
		for k, v := range found.Spec {
			spec[k] = v
		}
	}
	spec["server"] = ref.ServerName
	if _, ok := spec["displayName"]; !ok {
		spec["displayName"] = ref.Name
	}
	if _, ok := spec["inputSchema"]; !ok {
		spec["inputSchema"] = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	}
	return v1alpha1.Object{
		APIVersion: "registry.agentic.dev/v1alpha1",
		Kind:       v1alpha1.KindTool,
		Metadata: v1alpha1.ObjectMeta{
			Name:       slug(ref.ServerName + "-" + ref.Name),
			Namespace:  ref.Namespace,
			Visibility: v1alpha1.VisibilityPublic,
			Labels: map[string]string{
				"mcp.devai.io/server": ref.ServerName,
				"devai.io/source":     "cache",
				"devai.io/tier":       "extended",
			},
			Annotations: map[string]string{
				"mcp.devai.io/wire-name":       ref.Name,
				"resolve.devai.io/cached-from": source,
				"resolve.devai.io/cached-at":   r.now().UTC().Format(time.RFC3339),
			},
		},
		Spec: spec,
	}
}
