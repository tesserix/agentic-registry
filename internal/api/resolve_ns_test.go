package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// A detail-page link (or a shared/bookmarked URL) may omit ?namespace=. The
// artifact still lives in a non-default namespace (e.g. devai), so a
// namespace-less read must resolve by name across readable namespaces rather
// than 404 against "default". Covers get/tags/revisions.
func TestNamespacelessReadResolvesAcrossNamespaces(t *testing.T) {
	srv, st := testServer(t)
	if _, _, err := st.Apply(context.Background(), v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: "analyst-mcp", Namespace: "devai"},
		Spec: map[string]any{
			"name":    "analyst-mcp",
			"remotes": []any{map[string]any{"type": "streamableHttp", "url": "https://analyst.example/mcp"}},
		},
	}); err != nil {
		t.Fatalf("seed mcp: %v", err)
	}

	// GET latest with no namespace param resolves to devai.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/mcpservers/analyst-mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get without namespace: got %d, body %s", rec.Code, rec.Body.String())
	}
	var got v1alpha1.Object
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Metadata.Namespace != "devai" || got.Metadata.Name != "analyst-mcp" {
		t.Fatalf("resolved to wrong artifact: ns=%q name=%q", got.Metadata.Namespace, got.Metadata.Name)
	}

	// tags and revisions must resolve the same way.
	for _, path := range []string{"/v0/mcpservers/analyst-mcp/tags", "/v0/mcpservers/analyst-mcp/revisions"} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s without namespace: got %d, body %s", path, rec.Code, rec.Body.String())
		}
	}

	// An explicit (wrong) namespace is still honored verbatim — no silent
	// cross-namespace fallback that would mask a genuine miss.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/mcpservers/analyst-mcp?namespace=default", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("explicit wrong namespace should 404: got %d", rec.Code)
	}
}
