package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func seedMCPAndAgent(t *testing.T, st interface {
	Apply(context.Context, v1alpha1.Object) (v1alpha1.Object, bool, error)
}) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := st.Apply(ctx, v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: "github", Namespace: "devai", Labels: map[string]string{"mcp.tesserix.app/class": "platform"}},
		Spec: map[string]any{
			"name":            "github",
			"protocolVersion": "2026-07-28",
			"remotes":         []any{map[string]any{"type": "streamableHttp", "url": "https://gh.example/mcp"}},
			"tools":           []any{"get_pr", "create_pr"},
		},
	}); err != nil {
		t.Fatalf("seed mcp: %v", err)
	}
	if _, _, err := st.Apply(ctx, v1alpha1.Object{
		Kind:     v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{Name: "reviewer", Namespace: "devai"},
		Spec: map[string]any{
			"title":        "Reviewer",
			"systemPrompt": "review code",
			"mcpServers":   []any{"github"},
			"model":        map[string]any{"provider": "claude", "name": "claude-opus"},
		},
	}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func TestExportAgentgateway(t *testing.T) {
	srv, st := testServer(t)
	seedMCPAndAgent(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/export/agentgateway?namespace=devai", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"kind: AgentgatewayBackend",
		"kind: HTTPRoute",
		"name: github",
		"host: gh.example",
		"value: /mcp/github",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("agentgateway export missing %q\n%s", want, body)
		}
	}
}

func TestExportAgentgatewaySupportsConditionalGet(t *testing.T) {
	srv, st := testServer(t)
	seedMCPAndAgent(t, st)

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, httptest.NewRequest(
		http.MethodGet,
		"/v0/export/agentgateway?namespace=devai",
		nil,
	))

	if first.Code != http.StatusOK {
		t.Fatalf("first status: got %d, body %s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response has no ETag")
	}
	if got := first.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("cache control: got %q", got)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/v0/export/agentgateway?namespace=devai",
		nil,
	)
	request.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	srv.ServeHTTP(second, request)

	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional status: got %d, body %s", second.Code, second.Body.String())
	}
	if second.Body.Len() != 0 {
		t.Fatalf("conditional response body: got %q", second.Body.String())
	}
	if second.Header().Get("ETag") != etag {
		t.Fatalf("conditional ETag: got %q, want %q", second.Header().Get("ETag"), etag)
	}
	if second.Header().Get("X-Agentgateway-Resource-Count") == "" {
		t.Fatal("conditional response has no resource count")
	}
	if second.Header().Get("X-Agentgateway-Resource-Digest") == "" {
		t.Fatal("conditional response has no resource digest")
	}
}

func TestExportKagent(t *testing.T) {
	srv, st := testServer(t)
	seedMCPAndAgent(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/agents/reviewer/export/kagent?namespace=devai&modelConfig=opus&gatewayUrl=http://gateway:8080", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"apiVersion: kagent.dev/v1alpha2",
		"kind: Agent",
		"kind: RemoteMCPServer",
		"name: reviewer",
		"modelConfig: opus",
		"toolNames:",
		"url: http://gateway:8080/mcp/github",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("kagent export missing %q\n%s", want, body)
		}
	}
}

func TestExportKagentAll(t *testing.T) {
	srv, st := testServer(t)
	seedMCPAndAgent(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/export/kagent?namespace=devai", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"kind: Agent", "kind: RemoteMCPServer", "name: reviewer"} {
		if !strings.Contains(body, want) {
			t.Errorf("kagent-all export missing %q\n%s", want, body)
		}
	}
}

func TestExportKagentAllVariants(t *testing.T) {
	srv, st := testServer(t)
	seedMCPAndAgent(t, st)

	req := httptest.NewRequest(http.MethodGet,
		"/v0/export/kagent?namespace=devai&variants=anthropic:kagent-mc-anthropic,openai:kagent-mc-openai", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// One Agent variant per provider, each named <agent>-<suffix>, on its own
	// ModelConfig — and separated by `---` so they parse as distinct docs.
	for _, want := range []string{"name: reviewer-anthropic", "name: reviewer-openai", "kagent-mc-anthropic", "kagent-mc-openai", "\n---\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("variant export missing %q\n%s", want, body)
		}
	}
}

func TestExportKagentAllLabelSelector(t *testing.T) {
	srv, st := testServer(t)
	seedMCPAndAgent(t, st) // "reviewer" has no labels

	// A selector that matches nothing → empty output, still 200.
	req := httptest.NewRequest(http.MethodGet, "/v0/export/kagent?namespace=devai&labelSelector=devai.io/runtime%3Dkagent", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "kind: Agent") {
		t.Errorf("selector should have excluded the unlabelled agent:\n%s", rec.Body.String())
	}
}

func TestExportKagentRejectsNonAgent(t *testing.T) {
	srv, st := testServer(t)
	seedMCPAndAgent(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/skills/github/export/kagent?namespace=devai", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for non-agent kagent export, got %d", rec.Code)
	}
}

func seedTenantMCP(t *testing.T, st interface {
	Apply(context.Context, v1alpha1.Object) (v1alpha1.Object, bool, error)
}, namespace, name string) {
	t.Helper()
	if _, _, err := st.Apply(context.Background(), v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"mcp.tesserix.app/class": "platform"}},
		Spec: map[string]any{
			"name":            name,
			"protocolVersion": "2026-07-28",
			"remotes":         []any{map[string]any{"type": "streamableHttp", "url": "https://" + name + ".example/mcp"}},
		},
	}); err != nil {
		t.Fatalf("seed %s/%s: %v", namespace, name, err)
	}
}

func TestExportAgentgateway_MultipleTenantNamespaces(t *testing.T) {
	srv, st := testServer(t)
	seedTenantMCP(t, st, "devai", "devai-mcp")
	seedTenantMCP(t, st, "homechef", "homechef-mcp")

	req := httptest.NewRequest(http.MethodGet, "/v0/export/agentgateway?namespace=devai,homechef", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"value: /mcp/devai/devai-mcp",
		"value: /mcp/homechef/homechef-mcp",
		"name: devai-devai-mcp",
		"name: homechef-homechef-mcp",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("multi-tenant export missing %q\n%s", want, body)
		}
	}
}

func TestExportAgentgateway_PerServerScopePolicies(t *testing.T) {
	srv, st := testServer(t)
	seedTenantMCP(t, st, "homechef", "homechef-mcp")

	req := httptest.NewRequest(http.MethodGet,
		"/v0/export/agentgateway?namespace=homechef&requireServerScope=true&scopeClaim=roles", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"kind: AgentgatewayPolicy",
		`- '"mcp:homechef:homechef-mcp" in jwt["roles"]'`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scope policy missing %q\n%s", want, body)
		}
	}
}

func TestExportAgentgateway_UnknownNamespaceIsNotAnError(t *testing.T) {
	srv, st := testServer(t)
	seedTenantMCP(t, st, "devai", "devai-mcp")

	req := httptest.NewRequest(http.MethodGet, "/v0/export/agentgateway?namespace=devai,nosuchtenant", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "value: /mcp/devai/devai-mcp") {
		t.Error("a tenant with no servers must not suppress the others")
	}
}
