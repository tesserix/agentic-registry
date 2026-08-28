package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func TestResolvedAgentPinsObjectReferenceVersion(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	for _, obj := range []v1alpha1.Object{
		{
			Kind:     v1alpha1.KindSkill,
			Metadata: v1alpha1.ObjectMeta{Name: "triage", Namespace: "acme", Tag: "1.0.0"},
			Spec:     map[string]any{"description": "first"},
		},
		{
			Kind:     v1alpha1.KindSkill,
			Metadata: v1alpha1.ObjectMeta{Name: "triage", Namespace: "acme", Tag: "2.0.0"},
			Spec:     map[string]any{"description": "second"},
		},
		{
			Kind:     v1alpha1.KindAgent,
			Metadata: v1alpha1.ObjectMeta{Name: "support", Namespace: "acme", Tag: "1.0.0"},
			Spec: map[string]any{
				"skills": []any{map[string]any{"ref": "triage", "version": "1.0.0"}},
			},
		},
	} {
		if _, _, err := st.Apply(ctx, obj); err != nil {
			t.Fatalf("seed %s: %v", obj.Metadata.Tag, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v0/agents/support/resolved?namespace=acme", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolved status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got ResolvedAgent
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Unresolved) != 0 || len(got.Resolved["skills"]) != 1 {
		t.Fatalf("unexpected resolution: %#v", got)
	}
	resolved := got.Resolved["skills"][0]
	if resolved.Metadata.Tag != "1.0.0" || resolved.Spec["description"] != "first" {
		t.Fatalf("resolved a moving/latest dependency: %#v", resolved)
	}
}
