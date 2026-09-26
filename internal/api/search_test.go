package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/internal/discovery"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func TestSearchStubFiltersKindsAndDoesNotReturnArtifactBodies(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	for _, obj := range []v1alpha1.Object{
		{
			Kind:     v1alpha1.KindTool,
			Metadata: v1alpha1.ObjectMeta{Name: "scanner", Namespace: "devai", Visibility: v1alpha1.VisibilityPublic},
			Spec: map[string]any{
				"description": "Static application security",
				"inputSchema": map[string]any{"properties": map[string]any{"repository": map[string]any{"type": "string"}}},
			},
		},
		{
			Kind:     v1alpha1.KindAgent,
			Metadata: v1alpha1.ObjectMeta{Name: "reviewer", Namespace: "devai", Visibility: v1alpha1.VisibilityPublic},
			Spec:     map[string]any{"description": "Static application security", "systemPrompt": "PRIVATE PROMPT"},
		},
	} {
		if _, _, err := st.Apply(ctx, obj); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v0/search?q=static+application&kinds=tools&view=stub&limit=5", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var hits []discovery.Stub
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(hits) != 1 || hits[0].Kind != v1alpha1.KindTool || hits[0].Name != "scanner" {
		t.Fatalf("unexpected hits: %#v", hits)
	}
	if hits[0].FetchPath == "" || hits[0].Attributes["inputSchema"] == nil {
		t.Errorf("stub lacks progressive fetch/interface metadata: %#v", hits[0])
	}
	if rec.Body.String() == "" || strings.Contains(rec.Body.String(), "PRIVATE PROMPT") {
		t.Fatalf("search returned unsafe artifact body: %s", rec.Body.String())
	}
}

func TestSearchRejectsUnknownKind(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v0/search?q=test&kinds=secrets&view=stub", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchStubDoesNotRevealPrivateArtifacts(t *testing.T) {
	srv, st := testServerWith(t, config.Config{
		StoreBackend:  "memory",
		AuthMode:      "anonymous",
		AnonymousRole: "read",
	})
	for _, obj := range []v1alpha1.Object{
		{
			Kind: v1alpha1.KindAgent,
			Metadata: v1alpha1.ObjectMeta{
				Name: "public-agent", Namespace: "tenant-a", Visibility: v1alpha1.VisibilityPublic,
			},
			Spec: map[string]any{"description": "confidential infrastructure automation"},
		},
		{
			Kind: v1alpha1.KindAgent,
			Metadata: v1alpha1.ObjectMeta{
				Name: "private-agent", Namespace: "tenant-b", Visibility: v1alpha1.VisibilityPrivate,
			},
			Spec: map[string]any{"description": "confidential infrastructure automation"},
		},
	} {
		if _, _, err := st.Apply(context.Background(), obj); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v0/search?q=confidential+infrastructure&view=stub", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var hits []discovery.Stub
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(hits) != 1 || hits[0].Name != "public-agent" {
		t.Fatalf("private artifact crossed the search authorization boundary: %#v", hits)
	}
}
