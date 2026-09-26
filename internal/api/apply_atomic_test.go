package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const atomicBundle = `apiVersion: registry.agentic.dev/v1alpha1
kind: Skill
metadata:
  name: shared-name
spec:
  description: first
---
apiVersion: registry.agentic.dev/v1alpha1
kind: Tool
metadata:
  name: shared-name
spec:
  description: second
`

func TestApplyBundleIsAtomicWhenAStoredConstraintFails(t *testing.T) {
	srv, st := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v0/apply", bytes.NewBufferString(atomicBundle))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := st.Get(context.Background(), v1alpha1.KindSkill, "default", "shared-name", ""); err != store.ErrNotFound {
		t.Fatalf("the first document escaped a failed batch: %v", err)
	}
}

func TestApplyDryRunValidatesWithoutWriting(t *testing.T) {
	srv, st := testServer(t)
	body := `apiVersion: registry.agentic.dev/v1alpha1
kind: Skill
metadata:
  name: dry-run-skill
spec:
  description: valid
`
	req := httptest.NewRequest(http.MethodPost, "/v0/apply?dryRun=true", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := st.Get(context.Background(), v1alpha1.KindSkill, "default", "dry-run-skill", ""); err != store.ErrNotFound {
		t.Fatalf("dry-run wrote an artifact: %v", err)
	}
}

func TestApplyIdempotencyReplaysAndRejectsKeyReuseForAnotherBundle(t *testing.T) {
	srv, st := testServer(t)
	first := `apiVersion: registry.agentic.dev/v1alpha1
kind: Skill
metadata:
  name: idempotent-skill
  tag: 1.0.0
spec:
  description: first
`
	apply := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v0/apply", bytes.NewBufferString(body))
		req.Header.Set("Idempotency-Key", "publish-run-42")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	one := apply(first)
	two := apply(first)
	if one.Code != http.StatusOK || two.Code != http.StatusOK || one.Body.String() != two.Body.String() {
		t.Fatalf("idempotent replay differs: first=%d %s second=%d %s", one.Code, one.Body.String(), two.Code, two.Body.String())
	}
	changed := apply(strings.Replace(first, "description: first", "description: second", 1))
	if changed.Code != http.StatusConflict {
		t.Fatalf("reused key status=%d body=%s", changed.Code, changed.Body.String())
	}
	revisions, err := st.ListRevisions(context.Background(), v1alpha1.KindSkill, "default", "idempotent-skill")
	if err != nil || len(revisions) != 1 {
		t.Fatalf("replay wrote another revision: revisions=%d err=%v", len(revisions), err)
	}
}
