package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateChecksEveryDocumentWithoutARegistry(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bundle.yaml")
	body := `apiVersion: registry.agentic.dev/v1alpha1
kind: Agent
metadata:
  name: support
spec:
  definitionVersion: v1
  framework: langgraph
  runtime:
    type: remote
    protocol: a2a
    url: http://insecure.example/a2a
`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cmdValidate([]string{"-f", file}); err == nil {
		t.Fatal("validate accepted an insecure portable runtime")
	}
}

func TestApplyDryRunCarriesAnIdempotencyKey(t *testing.T) {
	var gotDryRun, gotKey bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDryRun = r.URL.Query().Get("dryRun") == "true"
		gotKey = r.Header.Get("Idempotency-Key") != ""
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"dry_run":true}`))
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTIC_REGISTRY", server.URL)
	t.Setenv("AGENTIC_TOKEN", "")
	file := filepath.Join(t.TempDir(), "skill.yaml")
	if err := os.WriteFile(file, []byte(`apiVersion: registry.agentic.dev/v1alpha1
kind: Skill
metadata:
  name: triage
spec:
  description: Triage
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cmdApply([]string{"-f", file, "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if !gotDryRun || !gotKey {
		t.Fatalf("dryRun=%v idempotencyKey=%v", gotDryRun, gotKey)
	}
}
