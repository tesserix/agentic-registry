package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func seedProbedServer(t *testing.T, st interface {
	Apply(context.Context, v1alpha1.Object) (v1alpha1.Object, bool, error)
}) {
	t.Helper()
	if _, _, err := st.Apply(context.Background(), v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: "homechef-mcp", Namespace: "devai"},
		Spec: map[string]any{
			"name":    "homechef-mcp",
			"version": "1.2.0",
			"tools":   []any{"get_order_status"},
			"remotes": []any{map[string]any{"type": "streamableHttp", "url": "https://hc.example/mcp"}},
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func putStatus(t *testing.T, srv http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/v0/mcpservers/homechef-mcp/status?namespace=devai", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestPutStatus_RecordsObservationAndReadyCondition(t *testing.T) {
	srv, st := testServer(t)
	seedProbedServer(t, st)

	rec := putStatus(t, srv, `{"reachable":true,"tools":["get_order_status"],"protocolVersion":"2026-07-28"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}

	got, err := st.Get(context.Background(), v1alpha1.KindMCPServer, "devai", "homechef-mcp", v1alpha1.DefaultTag)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status["observedHash"] != got.Status["declaredHash"] {
		t.Errorf("in-sync server: %v", got.Status)
	}
	if got.Status["protocolVersion"] != "2026-07-28" {
		t.Errorf("protocolVersion: %v", got.Status["protocolVersion"])
	}
	if got.Status["status"] != "active" {
		t.Errorf("lifecycle status clobbered: %v", got.Status["status"])
	}
}

// The prober reports what it saw; the registry decides what that means. A
// probe may not assert its own Ready condition.
func TestPutStatus_IgnoresCallerSuppliedConditions(t *testing.T) {
	srv, st := testServer(t)
	seedProbedServer(t, st)

	rec := putStatus(t, srv, `{"reachable":false,"error":"refused","conditions":[{"type":"Ready","status":"True"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}

	got, _ := st.Get(context.Background(), v1alpha1.KindMCPServer, "devai", "homechef-mcp", v1alpha1.DefaultTag)
	conditions := got.Status["conditions"].([]map[string]any)
	for _, c := range conditions {
		if c["type"] == "Ready" && c["status"] != "False" {
			t.Errorf("unreachable server reported Ready=%v", c["status"])
		}
	}
}

func TestPutStatus_DriftIsRecordedAgainstTheDeclaration(t *testing.T) {
	srv, st := testServer(t)
	seedProbedServer(t, st)

	rec := putStatus(t, srv, `{"reachable":true,"tools":["get_order_status","delete_order"],"protocolVersion":"2026-07-28"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", rec.Code, rec.Body.String())
	}

	got, _ := st.Get(context.Background(), v1alpha1.KindMCPServer, "devai", "homechef-mcp", v1alpha1.DefaultTag)
	if got.Status["proposedVersion"] != "1.3.0" {
		t.Errorf("proposedVersion: %v", got.Status["proposedVersion"])
	}
	tools := got.Spec["tools"].([]any)
	if len(tools) != 1 {
		t.Errorf("probe must not rewrite the declaration: %v", tools)
	}
}

func TestPutStatus_UnknownServer(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodPut,
		"/v0/mcpservers/ghost/status?namespace=devai", strings.NewReader(`{"reachable":true}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d", rec.Code)
	}
}

func TestPutStatus_OnlyMCPServersAreProbed(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodPut,
		"/v0/agents/reviewer/status?namespace=devai", strings.NewReader(`{"reachable":true}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestPutStatus_RejectsMalformedBody(t *testing.T) {
	srv, st := testServer(t)
	seedProbedServer(t, st)
	if rec := putStatus(t, srv, `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d", rec.Code)
	}
}
