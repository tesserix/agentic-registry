package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/internal/config"
)

func TestSessionReportsOnlyAllowlistedRoleHolderAsAdmin(t *testing.T) {
	srv, _ := testServerWith(t, config.Config{
		StoreBackend: "memory",
		AuthMode:     "trusted-header",
		TrustedProxy: true,
		AdminEmails:  []string{"samyak.rout@gmail.com", "mahesh.sangawar@gmail.com"},
		AdminRole:    "agentregistry.admin",
	})

	for name, tc := range map[string]struct {
		email     string
		wantAdmin bool
	}{
		"allowlisted":     {email: "samyak.rout@gmail.com", wantAdmin: true},
		"not allowlisted": {email: "attacker@example.com", wantAdmin: false},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v0/session", nil)
			req.Header.Set("X-Forwarded-User", "zitadel-user")
			req.Header.Set("X-Forwarded-Email", tc.email)
			req.Header.Set("X-Forwarded-Groups", "agentregistry.admin")
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("session status = %d, body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Authenticated bool   `json:"authenticated"`
				Email         string `json:"email"`
				Admin         bool   `json:"admin"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode session: %v", err)
			}
			if !body.Authenticated || body.Email != tc.email || body.Admin != tc.wantAdmin {
				t.Fatalf("unexpected session: %#v", body)
			}
		})
	}
}

func TestSessionIsAnonymousWithoutTrustedIdentity(t *testing.T) {
	srv, _ := testServerWith(t, config.Config{
		StoreBackend: "memory",
		AuthMode:     "trusted-header",
		TrustedProxy: true,
		AdminEmails:  []string{"samyak.rout@gmail.com"},
		AdminRole:    "agentregistry.admin",
	})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/session", nil))

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if body["authenticated"] != false || body["admin"] != false {
		t.Fatalf("unexpected anonymous session: %#v", body)
	}
}
