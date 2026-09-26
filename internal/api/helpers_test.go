package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicErrorsCarryStableCodeAndRequestID(t *testing.T) {
	srv, _ := testServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/not-a-kind", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != "not_found" || body.Message == "" || body.RequestID == "" {
		t.Fatalf("unexpected public error: %#v", body)
	}
	if body.RequestID != rec.Header().Get("X-Request-ID") {
		t.Fatalf("body request ID %q != header %q", body.RequestID, rec.Header().Get("X-Request-ID"))
	}
}
