package resolve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

type fakeApplier struct{ applied []v1alpha1.Object }

func (f *fakeApplier) Apply(_ context.Context, o v1alpha1.Object) (v1alpha1.Object, bool, error) {
	f.applied = append(f.applied, o)
	return o, true, nil
}

type fakeSource struct {
	name string
	has  map[string]*v1alpha1.Object
}

func (s fakeSource) Name() string { return s.name }
func (s fakeSource) Resolve(_ context.Context, ref Ref) (*v1alpha1.Object, error) {
	if o, ok := s.has[ref.Name]; ok {
		return o, nil
	}
	return nil, ErrNotFound
}

func TestResolverWriteThroughCache(t *testing.T) {
	app := &fakeApplier{}
	src := fakeSource{name: "upstream", has: map[string]*v1alpha1.Object{
		"trivy_scan": {Kind: v1alpha1.KindTool, Spec: map[string]any{
			"description": "Trivy scan", "inputSchema": map[string]any{"type": "object"},
		}},
	}}
	r := New(app, []Source{src}, nil)

	obj, err := r.Resolve(context.Background(), Ref{Name: "trivy_scan", Namespace: "devai", ServerName: "secops-mcp"})
	if err != nil || obj == nil {
		t.Fatalf("resolve: obj=%v err=%v", obj, err)
	}
	if len(app.applied) != 1 {
		t.Fatalf("write-through: applied=%d want 1", len(app.applied))
	}
	got := app.applied[0]
	if got.Kind != v1alpha1.KindTool {
		t.Errorf("kind=%s want Tool", got.Kind)
	}
	if got.Metadata.Labels["mcp.devai.io/server"] != "secops-mcp" {
		t.Errorf("server label=%q want secops-mcp", got.Metadata.Labels["mcp.devai.io/server"])
	}
	if got.Metadata.Labels["devai.io/source"] != "cache" {
		t.Errorf("source label=%q want cache", got.Metadata.Labels["devai.io/source"])
	}
	if got.Metadata.Annotations["mcp.devai.io/wire-name"] != "trivy_scan" {
		t.Errorf("wire-name=%q", got.Metadata.Annotations["mcp.devai.io/wire-name"])
	}
	if got.Metadata.Annotations["resolve.devai.io/cached-from"] != "upstream" {
		t.Errorf("cached-from=%q want upstream", got.Metadata.Annotations["resolve.devai.io/cached-from"])
	}
	if got.Spec["description"] != "Trivy scan" {
		t.Errorf("description not preserved: %v", got.Spec["description"])
	}
}

func TestResolverChainOrderAndMiss(t *testing.T) {
	app := &fakeApplier{}
	s1 := fakeSource{name: "a", has: map[string]*v1alpha1.Object{}}
	s2 := fakeSource{name: "b", has: map[string]*v1alpha1.Object{"x": {Kind: v1alpha1.KindTool}}}
	r := New(app, []Source{s1, s2}, nil)

	if _, err := r.Resolve(context.Background(), Ref{Name: "x", ServerName: "m"}); err != nil {
		t.Fatalf("expected hit from second source: %v", err)
	}
	if app.applied[0].Metadata.Annotations["resolve.devai.io/cached-from"] != "b" {
		t.Errorf("expected provenance from source b")
	}
	if _, err := r.Resolve(context.Background(), Ref{Name: "nope", ServerName: "m"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("miss should be ErrNotFound, got %v", err)
	}
}

func TestHTTPSource(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tools/known" {
			_, _ = w.Write([]byte(`{"description":"Known tool","parameters":{"type":"object"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	src := NewHTTPSource("test", ts.URL+"/tools/{name}")
	obj, err := src.Resolve(context.Background(), Ref{Name: "known"})
	if err != nil || obj == nil {
		t.Fatalf("resolve known: obj=%v err=%v", obj, err)
	}
	if obj.Spec["description"] != "Known tool" {
		t.Errorf("description=%v", obj.Spec["description"])
	}
	if obj.Spec["inputSchema"] == nil {
		t.Errorf("inputSchema should be mapped from `parameters`")
	}
	if _, err := src.Resolve(context.Background(), Ref{Name: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 should be ErrNotFound, got %v", err)
	}
}

func TestDisabledResolverMisses(t *testing.T) {
	r := New(&fakeApplier{}, nil, nil)
	if r.Enabled() {
		t.Error("no sources should be disabled")
	}
	if _, err := r.Resolve(context.Background(), Ref{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("disabled resolver should miss, got %v", err)
	}
}
