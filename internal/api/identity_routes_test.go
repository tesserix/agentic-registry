package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/config"
	identityplane "github.com/tesserix/agentic-registry/internal/identity"
	"github.com/tesserix/agentic-registry/internal/store"
)

type fakeIdentityControlPlane struct {
	token  string
	tenant string
}

func (f *fakeIdentityControlPlane) Onboard(_ context.Context, token, _ string, req identityplane.OnboardingRequest) (identityplane.Tenant, error) {
	f.token = token
	return identityplane.Tenant{ID: "tenant-42", Slug: req.Slug, Namespace: req.Slug, State: "ready"}, nil
}

func (f *fakeIdentityControlPlane) ListCredentials(_ context.Context, token, tenant string) ([]identityplane.Credential, error) {
	f.token, f.tenant = token, tenant
	return []identityplane.Credential{{ID: "cred-1", Name: "ci", ClientID: "client-1", Status: "active"}}, nil
}

func (f *fakeIdentityControlPlane) CreateCredential(_ context.Context, token, tenant, _ string, req identityplane.CreateCredentialRequest) (identityplane.CredentialSecret, error) {
	f.token, f.tenant = token, tenant
	return identityplane.CredentialSecret{
		Credential: identityplane.Credential{
			ID: "cred-1", Name: req.Name, ClientID: "client-1", Scopes: req.Scopes, Status: "active",
			ExpiresAt: time.Now().Add(time.Duration(req.LifetimeDays) * 24 * time.Hour),
		},
		ClientSecret: "shown-once",
	}, nil
}

func (f *fakeIdentityControlPlane) RotateCredential(_ context.Context, token, tenant, id, _ string) (identityplane.CredentialSecret, error) {
	f.token, f.tenant = token, tenant
	return identityplane.CredentialSecret{
		Credential:   identityplane.Credential{ID: id, ClientID: "client-1", Status: "active"},
		ClientSecret: "rotated-once",
	}, nil
}

func (f *fakeIdentityControlPlane) RevokeCredential(_ context.Context, token, tenant, _ string) error {
	f.token, f.tenant = token, tenant
	return nil
}

func identityTestServer(t *testing.T, plane identityplane.ControlPlane) http.Handler {
	t.Helper()
	authn, err := auth.New(config.Config{AuthMode: "trusted-header", TrustedProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	return NewWithIdentityControlPlane(store.NewMemory(), authn, config.Config{StoreBackend: "memory"}, plane)
}

func actorRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-User", "user-1")
	req.Header.Set("X-Forwarded-Email", "alice@example.com")
	req.Header.Set("X-Forwarded-Tenant", "tenant-42")
	req.Header.Set("X-Forwarded-Access-Token", "actor-access-token")
	return req
}

func TestAPICredentialCreateReturnsSecretOnceWithoutCaching(t *testing.T) {
	plane := &fakeIdentityControlPlane{}
	rec := httptest.NewRecorder()
	req := actorRequest(
		http.MethodPost,
		"/v0/settings/api-credentials",
		`{"name":"ci","scopes":["registry:read","registry:publish"],"lifetime_days":30,"namespaces":["agents-team"],"kinds":["Agent","Tool"]}`,
	)
	req.Header.Set("Idempotency-Key", "create-42")
	identityTestServer(t, plane).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"client_secret":"shown-once"`) {
		t.Fatalf("one-time secret missing: %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("secret response must be no-store")
	}
	if plane.token != "actor-access-token" || plane.tenant != "tenant-42" {
		t.Fatalf("identity context not forwarded: %#v", plane)
	}
}

func TestAPICredentialCreateRejectsAdministrativeScope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := actorRequest(
		http.MethodPost,
		"/v0/settings/api-credentials",
		`{"name":"ci","scopes":["registry:admin"],"lifetime_days":30,"namespaces":["agents-team"],"kinds":["Agent"]}`,
	)
	req.Header.Set("Idempotency-Key", "create-42")
	identityTestServer(t, &fakeIdentityControlPlane{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "scope") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOnboardingUsesAuthenticatedSubjectAndIdempotency(t *testing.T) {
	plane := &fakeIdentityControlPlane{}
	req := actorRequest(http.MethodPost, "/v0/onboarding", `{"slug":"acme-ai","display_name":"Acme AI"}`)
	req.Header.Del("X-Forwarded-Tenant")
	req.Header.Set("Idempotency-Key", "onboard-42")
	rec := httptest.NewRecorder()
	identityTestServer(t, plane).ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"namespace":"acme-ai"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if plane.token != "actor-access-token" {
		t.Fatal("actor token was not delegated")
	}
}

func TestAPICredentialRoutesRequireAuthenticatedTenant(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v0/settings/api-credentials", nil)
	rec := httptest.NewRecorder()
	identityTestServer(t, &fakeIdentityControlPlane{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
