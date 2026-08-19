package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/config"
)

func TestCORSAllowsDedicatedDeployKeyHeader(t *testing.T) {
	t.Parallel()

	server := &Server{cfg: config.Config{CORSOrigins: []string{"https://publisher.example"}}}
	handler := server.cors(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodOptions, "/v0/apply", nil)
	req.Header.Set("Origin", "https://publisher.example")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	allowedHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowedHeaders, auth.DeployKeyHeader) {
		t.Fatalf("Access-Control-Allow-Headers = %q, want %q", allowedHeaders, auth.DeployKeyHeader)
	}
}
