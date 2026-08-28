package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/internal/config"
)

func TestCLIAuthConfigPublishesOnlyPublicOAuthMetadata(t *testing.T) {
	srv, _ := testServerWith(t, config.Config{
		StoreBackend: "memory",
		AuthMode:     "anonymous",
		AuthIssuer:   "https://auth.tesserix.app",
		AuthAudience: "agentic-registry",
		CLIClientID:  "agentic-cli-public",
	})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/auth/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["issuer"] != "https://auth.tesserix.app" || body["client_id"] != "agentic-cli-public" {
		t.Fatalf("unexpected config: %#v", body)
	}
	if _, exists := body["client_secret"]; exists {
		t.Fatal("public OAuth metadata must not expose a secret")
	}
}
