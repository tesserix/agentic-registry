package api

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/adapters/agentgateway"
	"github.com/tesserix/agentic-registry/adapters/kagent"
	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/selector"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// Export endpoints render registry artifacts into runtime control-plane config
// server-side (reusing the adapters/ packages), so an in-cluster sync Job can
// `curl … | kubectl apply` without shipping the agentic CLI or re-implementing
// the rendering. They are plain GETs under /v0, covered by the catalog read
// authz the mesh already enforces.

// v0ExportAgentgateway renders every MCP server in a namespace as agentgateway
// routing config (AgentgatewayBackend + HTTPRoute) as one multi-doc YAML
// stream. Query params:
//
//	namespace        registry namespace to read MCP servers from (default: DefaultNamespace)
//	targetNamespace  namespace the rendered objects are created in (default: agentgateway-system)
//	gateway          HTTPRoute parentRef gateway name (default: agentgateway)
//	sandboxNamespace namespace image/package MCP servers run in (default: targetNamespace)
func (s *Server) v0ExportAgentgateway(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = v1alpha1.DefaultNamespace
	}
	res, err := s.store.List(r.Context(), store.ListOptions{
		Kind:       v1alpha1.KindMCPServer,
		Namespace:  ns,
		LatestOnly: true,
		CanRead:    readPredicate(r),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	servers := make([]v1alpha1.Object, 0, len(res.Items))
	for _, o := range res.Items {
		servers = append(servers, s.withIdentity(o))
	}
	out, err := agentgateway.Build(servers, agentgateway.Options{
		Namespace:        r.URL.Query().Get("targetNamespace"),
		GatewayName:      r.URL.Query().Get("gateway"),
		GatewayNamespace: r.URL.Query().Get("gatewayNamespace"),
		SandboxNamespace: r.URL.Query().Get("sandboxNamespace"),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeYAML(w, out)
}

// v0ExportKagent renders one Agent as a kagent.dev Agent CR plus a ToolServer
// per resolved MCP dependency. Mounted under the collection route; only valid
// for the agents collection. Query params:
//
//	namespace      registry namespace the agent lives in (default: DefaultNamespace)
//	targetNamespace namespace the kagent CRs are created in (default: kagent)
//	modelConfig    kagent ModelConfig CR name the Agent references
//	gatewayUrl     agentgateway base URL MCP tools are reached through
func (s *Server) v0ExportKagent(w http.ResponseWriter, r *http.Request) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	if kind != v1alpha1.KindAgent {
		writeErr(w, http.StatusBadRequest, "kagent export is only defined for agents")
		return
	}
	ns := s.namespace(r)
	name := chi.URLParam(r, "name")

	agent, err := s.store.Get(r.Context(), kind, ns, name, "")
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := identity(r)
	if !auth.CanRead(id, agent) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	agent = s.withIdentity(agent)
	resolved, _ := s.resolveAgentRefs(r.Context(), id, agent)
	opts := kagent.Options{
		Namespace:      r.URL.Query().Get("targetNamespace"),
		ModelConfigRef: r.URL.Query().Get("modelConfig"),
		GatewayURL:     r.URL.Query().Get("gatewayUrl"),
		SystemPrompt:   s.resolveSystemPrompt(r.Context(), id, agent),
	}

	// Cross-validation: ?validate=true returns a JSON {ok, issues} report of
	// whether this agent renders a kagent CR the controller ACCEPTS (vs the YAML)
	// — so DevAI authoring can check schema alignment before publish/deploy
	// instead of discovering a rejection at reconcile time.
	if r.URL.Query().Get("validate") == "true" {
		issues := kagent.Validate(agent, resolved["mcpServers"], opts)
		ok := true
		for _, is := range issues {
			if is.Severity == "error" {
				ok = false
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": ok, "issues": issues})
		return
	}

	out, err := kagent.Build(agent, resolved["mcpServers"], opts)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeYAML(w, out)
}

// v0ExportKagentAll renders every Agent matching an optional labelSelector
// (e.g. devai.io/runtime=kagent) into kagent.dev Agent + ToolServer YAML, as
// one multi-doc stream. This is what the kagent agent-sync Job applies so
// long-lived agents become controller-managed. Query params mirror the
// per-agent endpoint, plus:
//
//	namespace      registry namespace to read agents from (default: DefaultNamespace)
//	labelSelector  filter agents (default: all in the namespace)
func (s *Server) v0ExportKagentAll(w http.ResponseWriter, r *http.Request) {
	sel, err := selector.Parse(r.URL.Query().Get("labelSelector"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "labelSelector: "+err.Error())
		return
	}
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = v1alpha1.DefaultNamespace
	}
	res, err := s.store.List(r.Context(), store.ListOptions{
		Kind:       v1alpha1.KindAgent,
		Namespace:  ns,
		Selector:   sel,
		LatestOnly: true,
		CanRead:    readPredicate(r),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	baseOpts := kagent.Options{
		Namespace:  r.URL.Query().Get("targetNamespace"),
		GatewayURL: r.URL.Query().Get("gatewayUrl"),
	}
	// `variants`: comma-separated suffix:modelConfig pairs (e.g.
	// "anthropic:kagent-mc-anthropic,openai:kagent-mc-openai"). One Agent CR is
	// rendered per (agent, variant), named "<agent>-<suffix>", so a per-user
	// dispatch can target the provider/model variant the user chose. Absent →
	// a single variant from `modelConfig` with no suffix (back-compatible).
	type variant struct{ suffix, modelConfig string }
	var variants []variant
	for _, part := range strings.Split(r.URL.Query().Get("variants"), ",") {
		kv := strings.SplitN(strings.TrimSpace(part), ":", 2)
		if len(kv) == 2 && kv[0] != "" && kv[1] != "" {
			variants = append(variants, variant{suffix: kv[0], modelConfig: kv[1]})
		}
	}
	if len(variants) == 0 {
		variants = []variant{{modelConfig: r.URL.Query().Get("modelConfig")}}
	}
	id := identity(r)
	var buf bytes.Buffer
	for _, agent := range res.Items {
		agent = s.withIdentity(agent)
		resolved, _ := s.resolveAgentRefs(r.Context(), id, agent)
		systemPrompt := s.resolveSystemPrompt(r.Context(), id, agent)
		for _, vv := range variants {
			opts := baseOpts
			opts.NameSuffix = vv.suffix
			opts.ModelConfigRef = vv.modelConfig
			opts.SystemPrompt = systemPrompt
			out, err := kagent.Build(agent, resolved["mcpServers"], opts)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			// Separate every rendered doc with `---`; without it, concatenated
			// Agent docs parse as ONE YAML document (last keys win) and only the
			// final variant/agent is applied.
			if buf.Len() > 0 {
				buf.WriteString("---\n")
			}
			buf.Write(out)
		}
	}
	writeYAML(w, buf.Bytes())
}

func writeYAML(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
