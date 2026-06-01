package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/resolve"
	"github.com/tesserix/agentic-registry/internal/selector"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// wireNameAnnotation is the annotation a Tool carries with the raw name its
// MCP server exposes on the wire (the Tool artifact name is a slug, which may
// differ). An MCPServer's spec.tools list references these wire names.
const wireNameAnnotation = "mcp.devai.io/wire-name"

// ResolvedMCPServer is an MCPServer with its tool set resolved from the
// registry — the Phase-2 registry tier of the resolution chain
// (docs/agentic/MCP-HUB.md §5.5). `Unresolved` are declared tools the registry
// doesn't (yet) hold; in Phase 3 those are what the upstream/pull-through
// resolver fetches and caches. Until then they surface as a clear NOTIFY
// condition instead of silently vanishing.
type ResolvedMCPServer struct {
	MCPServer  v1alpha1.Object   `json:"mcpServer"`
	Tools      []v1alpha1.Object `json:"tools"`
	ToolCount  int               `json:"toolCount"`
	Unresolved []UnresolvedRef   `json:"unresolved,omitempty"`
	Conditions []Condition       `json:"conditions"`
}

// Condition is a Kubernetes-style status condition (subset) so consumers and
// the UI can render an actionable state without parsing free text.
type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"` // "True" | "False"
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// selectorFromMatchLabels turns a spec.toolSelector.matchLabels map into a
// label selector ("k1=v1,k2=v2"). Returns ok=false when there are no labels, so
// the caller can avoid an unintended match-all bind.
func selectorFromMatchLabels(spec map[string]interface{}) (selector.Selector, bool) {
	ts, ok := spec["toolSelector"].(map[string]interface{})
	if !ok {
		return selector.Selector{}, false
	}
	ml, ok := ts["matchLabels"].(map[string]interface{})
	if !ok || len(ml) == 0 {
		return selector.Selector{}, false
	}
	keys := make([]string, 0, len(ml))
	for k := range ml {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic
	parts := make([]string, 0, len(ml))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, ml[k]))
	}
	sel, err := selector.Parse(strings.Join(parts, ","))
	if err != nil {
		return selector.Selector{}, false
	}
	return sel, true
}

// resolveMCPServerTools is the single source of truth for "which tools does this
// MCP server actually serve" — the registry tier of the resolution chain. It
// unions tools matched by spec.toolSelector (label-based, dynamic) and validates
// the explicit spec.tools list, reporting any declared tool the registry lacks.
func (s *Server) resolveMCPServerTools(ctx context.Context, id auth.Identity, server v1alpha1.Object) ([]v1alpha1.Object, []UnresolvedRef) {
	ns := server.Metadata.Namespace
	resolved := []v1alpha1.Object{}
	byName := map[string]bool{}
	byWire := map[string]bool{}

	if sel, ok := selectorFromMatchLabels(server.Spec); ok {
		res, err := s.store.List(ctx, store.ListOptions{
			Kind:       v1alpha1.KindTool,
			Namespace:  ns,
			Selector:   sel,
			LatestOnly: true,
			Limit:      1 << 30,
			CanRead:    func(o v1alpha1.Object) bool { return auth.CanRead(id, o) },
		})
		if err == nil {
			for _, o := range res.Items {
				resolved = append(resolved, s.withIdentity(o))
				byName[o.Metadata.Name] = true
				if wn := o.Metadata.Annotations[wireNameAnnotation]; wn != "" {
					byWire[wn] = true
				}
			}
		}
	}

	// Validate the declared (explicit) tool list against what resolved. A ref is
	// satisfied if it matches a resolved Tool by wire-name or by artifact name.
	var unresolved []UnresolvedRef
	if tools, ok := server.Spec["tools"].([]interface{}); ok {
		for _, t := range tools {
			ref, _ := t.(string)
			if ref == "" || byWire[ref] || byName[ref] {
				continue
			}
			// Last chance in the registry tier: a directly-named Tool artifact.
			if obj, err := s.store.Get(ctx, v1alpha1.KindTool, ns, ref, ""); err == nil && auth.CanRead(id, obj) {
				resolved = append(resolved, s.withIdentity(obj))
				byName[ref] = true
				continue
			}
			// Upstream tier: pull from a configured Source and pull-through-cache
			// it into the registry, then bind — so the next resolve is a local hit.
			if s.resolver.Enabled() {
				if obj, rerr := s.resolver.Resolve(ctx, resolve.Ref{
					Name: ref, Namespace: ns, ServerName: server.Metadata.Name,
				}); rerr == nil && obj != nil {
					resolved = append(resolved, s.withIdentity(*obj))
					byName[obj.Metadata.Name] = true
					continue
				}
			}
			// NOTIFY tier: not anywhere — clear, actionable, never silent.
			unresolved = append(unresolved, UnresolvedRef{
				Kind: string(v1alpha1.KindTool), Ref: ref,
				Reason: "not found in registry or any upstream source",
			})
		}
	}
	return resolved, unresolved
}

// mcpResolved builds the ResolvedMCPServer envelope + status conditions.
func (s *Server) mcpResolved(ctx context.Context, id auth.Identity, server v1alpha1.Object) ResolvedMCPServer {
	tools, unresolved := s.resolveMCPServerTools(ctx, id, server)
	cond := Condition{Type: "Resolved", Status: "True", Reason: "AllToolsResolved", Message: fmt.Sprintf("%d tools resolved", len(tools))}
	if len(unresolved) > 0 {
		refs := make([]string, 0, len(unresolved))
		for _, u := range unresolved {
			refs = append(refs, u.Ref)
		}
		cond = Condition{
			Type: "Resolved", Status: "False", Reason: "ToolsUnresolved",
			Message: fmt.Sprintf("%d tool(s) not in registry: %s — publish them or add a source",
				len(unresolved), strings.Join(refs, ", ")),
		}
	}
	return ResolvedMCPServer{
		MCPServer:  server,
		Tools:      tools,
		ToolCount:  len(tools),
		Unresolved: unresolved,
		Conditions: []Condition{cond},
	}
}
