package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

type fixedIdentity struct{ value auth.Identity }

func (f fixedIdentity) Identify(*http.Request) auth.Identity { return f.value }

func TestActivationConditionAcceptsOnlyTheVerifiedConditionOwner(t *testing.T) {
	st := store.NewMemory()
	seedActivationMCPServer(t, st)
	server := New(st, fixedIdentity{value: auth.Identity{
		Authenticated: true, TenantID: "devai", Scopes: []string{auth.ScopeWrite},
		Groups: []string{"devai:writer", "registry:gateway-reconciler"},
	}}, config.Config{StoreBackend: "memory"})
	rec := putActivationCondition(t, server, st, activationPayload(t, st, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestActivationConditionRejectsUntrustedOrStaleObservations(t *testing.T) {
	tests := []struct {
		name     string
		identity auth.Identity
		payload  string
		wantCode int
	}{
		{
			name: "wrong verified actor", wantCode: http.StatusForbidden,
			identity: activationIdentity("devai", "registry:protocol-prober"),
		},
		{
			name: "other tenant is concealed", wantCode: http.StatusNotFound,
			identity: activationIdentity("other", "registry:gateway-reconciler"),
		},
		{
			name: "untrusted actor field", wantCode: http.StatusBadRequest,
			identity: activationIdentity("devai", "registry:gateway-reconciler"),
			payload:  `{"type":"DeploymentReady","status":"True","reason":"Accepted","actor":"gateway-reconciler","observedGeneration":1}`,
		},
		{
			name: "stale digest", wantCode: http.StatusUnprocessableEntity,
			identity: activationIdentity("devai", "registry:gateway-reconciler"),
			payload:  `{"type":"DeploymentReady","status":"True","reason":"Accepted","observedGeneration":1,"registryDigest":"sha256:stale","artifactDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := store.NewMemory()
			seedActivationMCPServer(t, st)
			server := New(st, fixedIdentity{value: test.identity}, config.Config{StoreBackend: "memory"})
			payload := test.payload
			if payload == "" {
				payload = activationPayload(t, st, "")
			}
			rec := putActivationCondition(t, server, st, payload)
			if rec.Code != test.wantCode {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if test.wantCode != http.StatusUnprocessableEntity {
				return
			}
			object, err := st.Get(context.Background(), v1alpha1.KindMCPServer, "devai", "orders", "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := object.Status["activation"]; ok {
				t.Fatal("stale observation changed activation status")
			}
		})
	}
}

func activationIdentity(tenant, actorGroup string) auth.Identity {
	return auth.Identity{Authenticated: true, TenantID: tenant, Scopes: []string{auth.ScopeWrite}, Groups: []string{tenant + ":writer", actorGroup}}
}

func activationPayload(t *testing.T, st store.Store, suffix string) string {
	t.Helper()
	object, err := st.Get(context.Background(), v1alpha1.KindMCPServer, "devai", "orders", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"type":"DeploymentReady","status":"True","reason":"Accepted%s","observedGeneration":1,"registryDigest":"%s","artifactDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`, suffix, object.Digest())
}

func putActivationCondition(t *testing.T, server http.Handler, st store.Store, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/v0/mcpservers/orders/1.0.0/activation/conditions?namespace=devai", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}

func seedActivationMCPServer(t *testing.T, st store.Store) {
	t.Helper()
	if _, _, err := st.Apply(context.Background(), v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: "orders", Namespace: "devai", Tag: "1.0.0"},
		Spec: map[string]any{
			"name": "orders",
			"x-tesserix": map[string]any{"publication": map[string]any{"artifact": map[string]any{
				"digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}
