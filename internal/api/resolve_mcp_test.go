package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// TestMCPServerResolvedRegistryTier verifies the registry tier of the MCP
// resolution chain: toolSelector resolves matching Tools, the explicit tools[]
// list is validated, and a declared-but-absent tool surfaces as a clear
// Unresolved ref + Resolved=False condition (NOTIFY) instead of silently
// vanishing.
func TestMCPServerResolvedRegistryTier(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	mkTool := func(name, wire string) v1alpha1.Object {
		return v1alpha1.Object{
			Kind: v1alpha1.KindTool,
			Metadata: v1alpha1.ObjectMeta{
				Name: name, Namespace: "devai",
				Labels:      map[string]string{"mcp.devai.io/server": "analyst-mcp"},
				Annotations: map[string]string{wireNameAnnotation: wire},
			},
			Spec: map[string]any{"displayName": name},
		}
	}
	for _, o := range []v1alpha1.Object{
		mkTool("analyst-sast", "security_scan_sast"),
		mkTool("analyst-lint", "validate_lint"),
	} {
		if _, _, err := st.Apply(ctx, o); err != nil {
			t.Fatalf("apply tool: %v", err)
		}
	}
	if _, _, err := st.Apply(ctx, v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: "analyst-mcp", Namespace: "devai"},
		Spec: map[string]any{
			"toolSelector": map[string]any{
				"matchLabels": map[string]any{"mcp.devai.io/server": "analyst-mcp"},
			},
			"tools": []any{"security_scan_sast", "validate_lint", "missing_tool"},
		},
	}); err != nil {
		t.Fatalf("apply mcpserver: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v0/mcpservers/analyst-mcp/resolved?namespace=devai", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("resolved: %d %s", rec.Code, rec.Body.String())
	}
	var got ResolvedMCPServer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ToolCount != 2 {
		t.Errorf("toolCount=%d want 2 (selector-resolved tools)", got.ToolCount)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0].Ref != "missing_tool" {
		t.Errorf("unresolved=%+v want exactly [missing_tool]", got.Unresolved)
	}
	if len(got.Conditions) == 0 || got.Conditions[0].Type != "Resolved" || got.Conditions[0].Status != "False" {
		t.Errorf("conditions=%+v want Resolved=False (NOTIFY)", got.Conditions)
	}
}
