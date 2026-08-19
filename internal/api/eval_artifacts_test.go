package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func TestEvalArtifactsPublishAndReadThroughV0Collections(t *testing.T) {
	t.Parallel()
	srv, _ := testServer(t)

	cases := []struct {
		kind   v1alpha1.Kind
		plural string
		name   string
		spec   map[string]any
	}{
		{v1alpha1.KindDataset, "datasets", "release-smoke", map[string]any{"cases": []any{}}},
		{v1alpha1.KindEvalSuite, "evalsuites", "release-gate", map[string]any{
			"datasetRef": map[string]any{"ref": "release-smoke", "version": "1"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.plural, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(v1alpha1.Object{
				APIVersion: v1alpha1.GroupVersion,
				Kind:       tc.kind,
				Metadata:   v1alpha1.ObjectMeta{Name: tc.name, Namespace: "devai", Tag: "1"},
				Spec:       tc.spec,
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			created := httptest.NewRecorder()
			srv.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/v0/"+tc.plural, bytes.NewReader(body)))
			if created.Code != http.StatusCreated {
				t.Fatalf("publish: got %d, body %s", created.Code, created.Body.String())
			}

			read := httptest.NewRecorder()
			srv.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/v0/"+tc.plural+"/"+tc.name+"?namespace=devai", nil))
			if read.Code != http.StatusOK {
				t.Fatalf("read: got %d, body %s", read.Code, read.Body.String())
			}
			var got v1alpha1.Object
			if err := json.Unmarshal(read.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Kind != tc.kind || got.Metadata.TenantID != "devai" {
				t.Fatalf("got kind=%q tenant=%q", got.Kind, got.Metadata.TenantID)
			}
		})
	}
}

func TestEvalArtifactsAreNotReadableAcrossTenants(t *testing.T) {
	t.Parallel()
	srv, _ := testServerWith(t, config.Config{
		StoreBackend: "memory",
		AuthMode:     "trusted-header",
		TrustedProxy: true,
	})
	body, err := json.Marshal(v1alpha1.Object{
		APIVersion: v1alpha1.GroupVersion,
		Kind:       v1alpha1.KindDataset,
		Metadata: v1alpha1.ObjectMeta{
			Name:       "private-smoke",
			Namespace:  "tenant-a",
			Tag:        "1",
			Visibility: v1alpha1.VisibilityPrivate,
		},
		Spec: map[string]any{"cases": []any{}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	publish := httptest.NewRequest(http.MethodPost, "/v0/datasets", bytes.NewReader(body))
	publish.Header.Set("X-Forwarded-User", "alice")
	publish.Header.Set("X-Forwarded-Tenant", "tenant-a")
	publish.Header.Set("X-Forwarded-Groups", "tenant-a:writer")
	created := httptest.NewRecorder()
	srv.ServeHTTP(created, publish)
	if created.Code != http.StatusCreated {
		t.Fatalf("publish: got %d, body %s", created.Code, created.Body.String())
	}

	read := httptest.NewRequest(http.MethodGet, "/v0/datasets/private-smoke?namespace=tenant-a", nil)
	read.Header.Set("X-Forwarded-User", "mallory")
	read.Header.Set("X-Forwarded-Tenant", "tenant-b")
	read.Header.Set("X-Forwarded-Groups", "tenant-b:reader")
	notFound := httptest.NewRecorder()
	srv.ServeHTTP(notFound, read)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant read: got %d, body %s", notFound.Code, notFound.Body.String())
	}
}
