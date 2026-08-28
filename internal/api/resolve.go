package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// ResolvedAgent is an Agent with its composition references (skills/tools/
// mcpServers/prompts) fetched from the catalog. It is what runtimes and
// adapters (kagent / agentgateway) consume — they should never re-implement
// reference resolution.
type ResolvedAgent struct {
	Agent      v1alpha1.Object              `json:"agent"`
	Resolved   map[string][]v1alpha1.Object `json:"resolved"`
	Unresolved []UnresolvedRef              `json:"unresolved,omitempty"`
}

// UnresolvedRef records a reference that didn't resolve (missing or not
// readable by the caller) so consumers can fail loudly instead of silently
// running a half-composed agent.
type UnresolvedRef struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
}

// refField maps an Agent spec field to the registry Kind it references.
var refFields = []struct {
	Field string
	Kind  v1alpha1.Kind
}{
	{"skills", v1alpha1.KindSkill},
	{"tools", v1alpha1.KindTool},
	{"mcpServers", v1alpha1.KindMCPServer},
	{"prompts", v1alpha1.KindPrompt},
}

// resolveAgentRefs walks an Agent's spec reference lists and fetches each one
// from the store in the agent's namespace, honouring the caller's read
// visibility. Inline (object) entries pass through as-is. This is the single
// source of truth for "what does this agent actually depend on", shared by the
// /resolved endpoint and the export adapters.
func (s *Server) resolveAgentRefs(ctx context.Context, id auth.Identity, agent v1alpha1.Object) (map[string][]v1alpha1.Object, []UnresolvedRef) {
	ns := agent.Metadata.Namespace
	resolved := map[string][]v1alpha1.Object{}
	var unresolved []UnresolvedRef

	for _, rf := range refFields {
		entries, _ := agent.Spec[rf.Field].([]interface{})
		out := make([]v1alpha1.Object, 0, len(entries))
		for _, e := range entries {
			switch v := e.(type) {
			case string:
				obj, err := s.store.Get(ctx, rf.Kind, ns, v, "")
				if err != nil || !auth.CanRead(id, obj) {
					unresolved = append(unresolved, UnresolvedRef{
						Kind: string(rf.Kind), Ref: v, Reason: "not found or not readable",
					})
					continue
				}
				out = append(out, s.withIdentity(obj))
			case map[string]interface{}:
				ref, _ := v["ref"].(string)
				if ref == "" {
					// Inline definition — wrap it as an Object so consumers see a
					// uniform shape, but don't fetch anything.
					out = append(out, v1alpha1.Object{Kind: rf.Kind, Spec: v})
					continue
				}
				version, _ := v["version"].(string)
				obj, err := s.store.Get(ctx, rf.Kind, ns, ref, version)
				if err != nil || !auth.CanRead(id, obj) {
					unresolved = append(unresolved, UnresolvedRef{
						Kind: string(rf.Kind), Ref: ref, Reason: "not found or not readable",
					})
					continue
				}
				out = append(out, s.withIdentity(obj))
			}
		}
		if len(out) > 0 {
			resolved[rf.Field] = out
		}
	}
	return resolved, unresolved
}

// resolveSystemPrompt returns the agent's effective system prompt for runtimes
// that need it inline (kagent's systemMessage must be non-empty). Prefers the
// agent's own spec.systemPrompt; otherwise follows the scalar spec.promptRef to
// the Prompt artifact and returns its spec.systemPrompt. Most registry agents
// keep their prompt in a referenced Prompt (not inline), so without this they'd
// export an empty systemMessage and the kagent controller would reject the CR.
// Returns "" when nothing resolves (Build then falls back to the description).
func (s *Server) resolveSystemPrompt(ctx context.Context, id auth.Identity, agent v1alpha1.Object) string {
	if sp, ok := agent.Spec["systemPrompt"].(string); ok && sp != "" {
		return sp
	}
	ref, _ := agent.Spec["promptRef"].(string)
	if ref == "" {
		return ""
	}
	obj, err := s.store.Get(ctx, v1alpha1.KindPrompt, agent.Metadata.Namespace, ref, "")
	if err != nil || !auth.CanRead(id, obj) {
		return ""
	}
	if sp, ok := obj.Spec["systemPrompt"].(string); ok {
		return sp
	}
	return ""
}

// v0AgentResolved serves an Agent with its references resolved.
func (s *Server) v0AgentResolved(w http.ResponseWriter, r *http.Request) {
	s.agentResolved(w, r, "")
}

func (s *Server) v0AgentResolvedTag(w http.ResponseWriter, r *http.Request) {
	s.agentResolved(w, r, chi.URLParam(r, "tag"))
}

func (s *Server) agentResolved(w http.ResponseWriter, r *http.Request, tag string) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	// Composition resolution is defined for Agents (skills/tools/mcpServers/
	// prompts) and MCPServers (their tool set — registry tier of the resolution
	// chain). Other kinds have nothing to resolve.
	if kind != v1alpha1.KindAgent && kind != v1alpha1.KindMCPServer {
		writeErr(w, http.StatusBadRequest, "resolution is only defined for agents and mcpservers")
		return
	}
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid artifact name")
		return
	}
	ns := s.resolveNamespace(r, kind, name) // resolve across readable namespaces

	obj, err := s.store.Get(r.Context(), kind, ns, name, tag)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := identity(r)
	if !auth.CanRead(id, obj) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	obj = s.withIdentity(obj)

	if kind == v1alpha1.KindMCPServer {
		writeJSON(w, http.StatusOK, s.mcpResolved(r.Context(), id, obj))
		return
	}
	resolved, unresolved := s.resolveAgentRefs(r.Context(), id, obj)
	writeJSON(w, http.StatusOK, ResolvedAgent{
		Agent:      obj,
		Resolved:   resolved,
		Unresolved: unresolved,
	})
}
