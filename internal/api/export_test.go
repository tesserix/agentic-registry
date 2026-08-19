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
		Metadata: v1alpha1.ObjectMeta{Name: "github", Namespace: "devai"},
		Spec: map[string]any{
			"name":    "github",
			"remotes": []any{map[string]any{"type": "streamableHttp", "url": "https://gh.example/mcp"}},
			"tools":   []any{"get_pr", "create_pr"},
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
