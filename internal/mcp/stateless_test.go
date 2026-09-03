package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/internal/store"
)

func modernRequest(method, version string, capabilities any) []byte {
	params := map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    version,
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "1"},
			"io.modelcontextprotocol/clientCapabilities": capabilities,
		},
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	return body
}

func modernCall(t *testing.T, handler http.Handler, method, headerVersion, bodyVersion string, capabilities any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(modernRequest(method, bodyVersion, capabilities)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", headerVersion)
	req.Header.Set("MCP-Method", method)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func rpcCode(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var response struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error == nil {
		t.Fatalf("response has no error: %s", rec.Body.String())
	}
	return response.Error.Code
}

func TestDiscoveryServer_ModernRequestsAreStatelessAndDiscoverable(t *testing.T) {
	handler := NewDiscoveryServer(store.NewMemory(), nil)

	discover := modernCall(t, handler, "server/discover", "2026-07-28", "2026-07-28", map[string]any{})
	if discover.Code != http.StatusOK {
		t.Fatalf("discover status=%d body=%s", discover.Code, discover.Body.String())
	}
	var response struct {
		Result struct {
			SupportedVersions []string `json:"supportedVersions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(discover.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.SupportedVersions) == 0 || response.Result.SupportedVersions[0] != "2026-07-28" {
		t.Fatalf("supportedVersions=%v", response.Result.SupportedVersions)
	}

	list := modernCall(t, handler, "tools/list", "2026-07-28", "2026-07-28", map[string]any{})
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"tools"`)) {
		t.Fatalf("self-contained tools/list status=%d body=%s", list.Code, list.Body.String())
	}
}

func TestDiscoveryServer_ModernRequestValidationFailsClosed(t *testing.T) {
	handler := NewDiscoveryServer(store.NewMemory(), nil)

	tests := []struct {
		name         string
		method       string
		header       string
		body         string
		capabilities any
		wantStatus   int
		wantCode     int
	}{
		{"unsupported version", "server/discover", "1900-01-01", "1900-01-01", map[string]any{}, 400, -32022},
		{"version mismatch", "server/discover", "2026-07-28", "2025-11-25", map[string]any{}, 400, -32020},
		{"missing capabilities", "server/discover", "2026-07-28", "2026-07-28", nil, 400, -32602},
		{"unknown method", "subscriptions/listen", "2026-07-28", "2026-07-28", map[string]any{}, 404, -32601},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := modernCall(t, handler, tt.method, tt.header, tt.body, tt.capabilities)
			if rec.Code != tt.wantStatus || rpcCode(t, rec) != tt.wantCode {
				t.Fatalf("status=%d code=%d body=%s", rec.Code, rpcCode(t, rec), rec.Body.String())
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(modernRequest("ping", "2026-07-28", map[string]any{})))
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("MCP-Method", "ping")
	req.Header.Set("Mcp-Session-Id", "forged")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("session-bearing modern request status=%d", rec.Code)
	}

	mismatch := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(modernRequest("ping", "2026-07-28", map[string]any{})))
	mismatch.Header.Set("MCP-Protocol-Version", "2026-07-28")
	mismatch.Header.Set("MCP-Method", "tools/list")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, mismatch)
	if rec.Code != http.StatusBadRequest || rpcCode(t, rec) != -32020 {
		t.Fatalf("method mismatch status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDiscoveryServer_BoundsRequestBody(t *testing.T) {
	handler := NewDiscoveryServer(store.NewMemory(), nil)
	body := append([]byte(`{"padding":"`), bytes.Repeat([]byte("a"), maxRequestBody)...)
	body = append(body, []byte(`"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status=%d", rec.Code)
	}
}
