package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPControlPlaneForwardsActorTokenAndNeverPersistsSecrets(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer actor-access-token" {
			t.Fatalf("actor token was not forwarded")
		}
		if r.Header.Get("X-Tesserix-Expected-Tenant") != "tenant-42" {
			t.Fatalf("expected tenant guard missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"credentials": []map[string]any{{
				"id": "cred-1", "name": "ci", "client_id": "client-1", "status": "active",
			}}})
		case r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"credential":    map[string]any{"id": "cred-1", "name": "ci", "client_id": "client-1", "status": "active"},
				"client_secret": "shown-once",
			})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	client, err := NewHTTPControlPlane(server.URL, server.Client())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()
	listed, err := client.ListCredentials(ctx, "actor-access-token", "tenant-42")
	if err != nil || len(listed) != 1 || listed[0].ClientID != "client-1" {
		t.Fatalf("list = %#v, err=%v", listed, err)
	}
	created, err := client.CreateCredential(ctx, "actor-access-token", "tenant-42", "create-42", CreateCredentialRequest{
		Name: "ci", Scopes: []string{"registry:read", "registry:publish"}, LifetimeDays: 30,
		Namespaces: []string{"agents-team"}, Kinds: []string{"Agent", "Tool"},
	})
	if err != nil || created.ClientSecret != "shown-once" {
		t.Fatalf("create = %#v, err=%v", created, err)
	}
	rotated, err := client.RotateCredential(ctx, "actor-access-token", "tenant-42", "cred-1", "rotate-42")
	if err != nil || rotated.ClientSecret != "shown-once" {
		t.Fatalf("rotate = %#v, err=%v", rotated, err)
	}
	if err := client.RevokeCredential(ctx, "actor-access-token", "tenant-42", "cred-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	want := []string{
		"GET /v1/registry/credentials",
		"POST /v1/registry/credentials",
		"POST /v1/registry/credentials/cred-1/rotate",
		"DELETE /v1/registry/credentials/cred-1",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestHTTPControlPlaneOnboardsIdempotently(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "onboard-42" {
			t.Fatalf("idempotency key missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "tenant-42", "slug": "acme-ai", "namespace": "acme-ai", "state": "ready",
		})
	}))
	defer server.Close()

	client, err := NewHTTPControlPlane(server.URL, server.Client())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	tenant, err := client.Onboard(context.Background(), "actor-token", "onboard-42", OnboardingRequest{
		Slug: "acme-ai", DisplayName: "Acme AI",
	})
	if err != nil || tenant.ID != "tenant-42" || tenant.State != "ready" {
		t.Fatalf("tenant = %#v, err=%v", tenant, err)
	}
}

func TestCredentialWireShapeContainsMetadataOnly(t *testing.T) {
	credential := Credential{
		ID: "cred-1", Name: "ci", ClientID: "client-1", Status: "active",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || json.Valid(raw) == false {
		t.Fatal("credential metadata must marshal")
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if _, ok := fields["client_secret"]; ok {
		t.Fatal("credential metadata must never contain a secret")
	}
}

func TestHTTPControlPlaneAllowsMeshInternalServiceOrigin(t *testing.T) {
	if _, err := NewHTTPControlPlane("http://onboarding-api.onboarding.svc.cluster.local:8080", nil); err != nil {
		t.Fatalf("mesh-internal service URL must be accepted: %v", err)
	}
	if _, err := NewHTTPControlPlane("http://onboarding.example.com", nil); err == nil {
		t.Fatal("external plaintext HTTP must be rejected")
	}
}

func TestHTTPControlPlanePreservesRFC7807PublicDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":  "https://onboard.tesserix.app/problems/Conflict",
			"title": "Conflict", "status": 409,
			"detail": "this identity already owns a different Registry workspace",
		})
	}))
	defer server.Close()

	client, err := NewHTTPControlPlane(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Onboard(context.Background(), "actor-token", "onboard-42", OnboardingRequest{
		Slug: "acme-ai", DisplayName: "Acme AI",
	})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusConflict ||
		apiErr.Message != "this identity already owns a different Registry workspace" {
		t.Fatalf("public upstream problem was lost: %#v", err)
	}
}
