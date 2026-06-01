package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func testServer(t *testing.T) (http.Handler, store.Store) {
	t.Helper()
	return testServerWith(t, config.Config{StoreBackend: "memory", AuthMode: "anonymous"})
}

func testServerWith(t *testing.T, cfg config.Config) (http.Handler, store.Store) {
	t.Helper()
	st := store.NewMemory()
	authn, err := auth.New(cfg)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return New(st, authn, cfg), st
}

// cardProvenance digs the registry-provenance extension params out of a
// rendered card, or fails the test if it's absent/malformed.
func cardProvenance(t *testing.T, card map[string]any) map[string]any {
	t.Helper()
	caps, _ := card["capabilities"].(map[string]any)
	exts, _ := caps["extensions"].([]any)
	for _, e := range exts {
		ext, _ := e.(map[string]any)
		if uri, _ := ext["uri"].(string); uri == "https://registry.agentic.dev/ext/provenance" {
			params, _ := ext["params"].(map[string]any)
			return params
		}
	}
	t.Fatalf("card has no provenance extension: %v", card)
	return nil
}

func seedAgentAndSkill(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := st.Apply(ctx, v1alpha1.Object{
		Kind:     v1alpha1.KindSkill,
		Metadata: v1alpha1.ObjectMeta{Name: "k8s-troubleshooter", Namespace: "sre", Labels: map[string]string{"domain": "sre"}},
		Spec:     map[string]any{"title": "K8s Troubleshooter", "description": "debugs clusters"},
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if _, _, err := st.Apply(ctx, v1alpha1.Object{
		Kind:     v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{Name: "oncall", Namespace: "sre", Tag: "1.0.0"},
		Spec: map[string]any{
			"title":       "On-Call Responder",
			"description": "watches alerts",
			"a2a": map[string]any{
				"url":                "https://oncall.sre.svc/a2a/v1",
				"preferredTransport": "JSONRPC",
			},
			"skills": []any{"k8s-troubleshooter"},
		},
	}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func TestAgentCardEndpoint(t *testing.T) {
	srv, st := testServer(t)
	seedAgentAndSkill(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/agents/oncall/card?namespace=sre", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("card endpoint status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("card not JSON: %v", err)
	}
	if card["protocolVersion"] == nil || card["url"] != "https://oncall.sre.svc/a2a/v1" {
		t.Fatalf("card missing core fields: %v", card)
	}
	skills, _ := card["skills"].([]any)
	if len(skills) != 1 {
		t.Fatalf("expected the linked skill resolved into the card, got %v", skills)
	}
}

func TestAgentCardWellKnownAlias(t *testing.T) {
	srv, st := testServer(t)
	seedAgentAndSkill(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/agents/oncall/.well-known/agent-card.json?namespace=sre", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(".well-known alias status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgentCardRejectsNonAgentKind(t *testing.T) {
	srv, st := testServer(t)
	seedAgentAndSkill(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/skills/k8s-troubleshooter/card?namespace=sre", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("card on a non-agent kind should be 400, got %d", rec.Code)
	}
}

func TestAgentCardNotFound(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v0/agents/ghost/card?namespace=sre", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing agent card should be 404, got %d", rec.Code)
	}
}

// With signing enabled the served card must carry a registry attestation
// (signature + signedBy) in its provenance — that's what a secure-by-default
// A2A consumer verifies before trusting the card's service url.
func TestAgentCardCarriesSignatureWhenSigningEnabled(t *testing.T) {
	srv, st := testServerWith(t, config.Config{StoreBackend: "memory", AuthMode: "anonymous", SigningDev: true})
	seedAgentAndSkill(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/agents/oncall/card?namespace=sre", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("card endpoint status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("card not JSON: %v", err)
	}
	prov := cardProvenance(t, card)
	if sig, _ := prov["signature"].(string); sig == "" {
		t.Fatalf("signing enabled but provenance carries no signature: %v", prov)
	}
	if by, _ := prov["signedBy"].(string); by == "" {
		t.Fatalf("signing enabled but provenance carries no signedBy key id: %v", prov)
	}
	if dig, _ := prov["digest"].(string); dig == "" {
		t.Fatalf("provenance carries no digest to verify the signature against: %v", prov)
	}
}

// With signing OFF, the card still renders (identity present) but carries no
// signature — verifying the renderer degrades gracefully, not that it errors.
func TestAgentCardOmitsSignatureWhenSigningDisabled(t *testing.T) {
	srv, st := testServer(t)
	seedAgentAndSkill(t, st)

	req := httptest.NewRequest(http.MethodGet, "/v0/agents/oncall/card?namespace=sre", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("card endpoint status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("card not JSON: %v", err)
	}
	prov := cardProvenance(t, card)
	if _, ok := prov["signature"]; ok {
		t.Fatalf("signing disabled but provenance carries a signature: %v", prov)
	}
	if arn, _ := prov["arn"].(string); arn == "" {
		t.Fatalf("provenance should still pin the artifact arn even unsigned: %v", prov)
	}
}
