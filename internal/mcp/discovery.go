// Package mcp implements a built-in MCP discovery server so agentic IDEs
// (Cursor, Claude Desktop, VS Code) can browse the registry's contents through
// MCP itself. It is a read-only catalog endpoint exposing list/get/search
// tools over JSON-RPC 2.0 (Streamable HTTP). It is NOT a proxy: it answers
// discovery queries and returns metadata, never routing or running anything.
package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/selector"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// parseSelectorArg parses a label-selector string from an MCP tool argument,
// degrading to the match-everything selector on error (discovery is lenient).
func parseSelectorArg(s string) selector.Selector {
	sel, err := selector.Parse(s)
	if err != nil {
		return selector.Selector{}
	}
	return sel
}

// DiscoveryServer answers MCP JSON-RPC requests against the registry store.
type DiscoveryServer struct {
	store store.Store
}

// NewDiscoveryServer returns an http.Handler for the /mcp endpoint.
func NewDiscoveryServer(st store.Store) http.Handler {
	return &DiscoveryServer{store: st}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (d *DiscoveryServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	var id interface{}
	if len(req.ID) > 0 {
		_ = json.Unmarshal(req.ID, &id)
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "agentic-registry", "version": "v0"},
		}
	case "tools/list":
		resp.Result = map[string]interface{}{"tools": toolDefs()}
	case "tools/call":
		result, err := d.callTool(r.Context(), r, req.Params)
		if err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
		} else {
			resp.Result = result
		}
	case "ping":
		resp.Result = map[string]interface{}{}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func toolDefs() []map[string]interface{} {
	listSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"namespace":     map[string]interface{}{"type": "string"},
			"labelSelector": map[string]interface{}{"type": "string"},
		},
	}
	getSchema := map[string]interface{}{
		"type":     "object",
		"required": []string{"name"},
		"properties": map[string]interface{}{
			"name":      map[string]interface{}{"type": "string"},
			"namespace": map[string]interface{}{"type": "string"},
			"tag":       map[string]interface{}{"type": "string"},
		},
	}
	searchSchema := map[string]interface{}{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string"},
		},
	}
	var tools []map[string]interface{}
	for _, k := range v1alpha1.AllKinds {
		plural := v1alpha1.Plural(k)
		tools = append(tools,
			tool("list_"+plural, "List "+plural+" in the registry (supports labelSelector).", listSchema),
			tool("get_"+singular(plural), "Get one "+singular(plural)+" by name.", getSchema),
		)
	}
	tools = append(tools, tool("search_registry", "Keyword search across all artifact kinds.", searchSchema))
	return tools
}

func tool(name, desc string, schema map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"name": name, "description": desc, "inputSchema": schema}
}

func singular(plural string) string {
	switch plural {
	case "skills":
		return "skill"
	case "tools":
		return "tool"
	case "mcpservers":
		return "mcpserver"
	case "prompts":
		return "prompt"
	case "workflows":
		return "workflow"
	case "blueprints":
		return "blueprint"
	case "agents":
		return "agent"
	}
	return plural
}

// identityFrom derives the caller identity from the MCP request's context
// (populated by the auth middleware), so discovery respects visibility/RBAC.
func (d *DiscoveryServer) callTool(ctx context.Context, r *http.Request, params json.RawMessage) (interface{}, error) {
	var p struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	id := auth.FromContext(r.Context())
	canRead := func(o v1alpha1.Object) bool { return auth.CanRead(id, o) }
	args := p.Arguments
	str := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}

	switch {
	case p.Name == "search_registry":
		res, err := d.store.List(ctx, store.ListOptions{Namespace: "all", Search: str("query"), LatestOnly: true, CanRead: canRead, Limit: 50})
		if err != nil {
			return nil, err
		}
		return toolResult(summaries(res.Items)), nil
	default:
		// list_<plural> / get_<singular>
		return d.kindTool(ctx, p.Name, args, str, canRead)
	}
}

func (d *DiscoveryServer) kindTool(ctx context.Context, name string, args map[string]interface{}, str func(string) string, canRead func(v1alpha1.Object) bool) (interface{}, error) {
	for _, k := range v1alpha1.AllKinds {
		plural := v1alpha1.Plural(k)
		switch name {
		case "list_" + plural:
			sel := parseSelectorArg(str("labelSelector"))
			ns := str("namespace")
			if ns == "" {
				ns = v1alpha1.DefaultNamespace
			}
			res, err := d.store.List(ctx, store.ListOptions{Kind: k, Namespace: ns, Selector: sel, LatestOnly: true, CanRead: canRead, Limit: 100})
			if err != nil {
				return nil, err
			}
			return toolResult(summaries(res.Items)), nil
		case "get_" + singular(plural):
			ns := str("namespace")
			if ns == "" {
				ns = v1alpha1.DefaultNamespace
			}
			o, err := d.store.Get(ctx, k, ns, str("name"), str("tag"))
			if err != nil {
				return nil, err
			}
			if !canRead(o) {
				return nil, store.ErrNotFound
			}
			return toolResult(o), nil
		}
	}
	return nil, store.ErrNotFound
}

func summaries(items []v1alpha1.Object) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, o := range items {
		desc, _ := o.Spec["description"].(string)
		out = append(out, map[string]interface{}{
			"kind":        o.Kind,
			"name":        o.Metadata.Name,
			"namespace":   o.Metadata.Namespace,
			"tag":         o.Metadata.Tag,
			"labels":      o.Metadata.Labels,
			"description": desc,
		})
	}
	return out
}

// toolResult wraps a payload in the MCP tools/call content envelope.
func toolResult(payload interface{}) map[string]interface{} {
	b, _ := json.MarshalIndent(payload, "", "  ")
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": string(b)}},
	}
}
