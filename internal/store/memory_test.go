package store

import (
	"context"
	"testing"

	"github.com/tesserix/agentic-registry/internal/selector"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func mkSkill(name, vis string, labels map[string]string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind: v1alpha1.KindSkill,
		Metadata: v1alpha1.ObjectMeta{
			Name:       name,
			Visibility: v1alpha1.Visibility(vis),
			Labels:     labels,
		},
		Spec: map[string]interface{}{"description": "test " + name},
	}
}

func TestApplyIdempotentAndCreatedFlag(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	_, created, _ := m.Apply(ctx, mkSkill("a", "public", nil))
	if !created {
		t.Fatal("first apply should report created")
	}
	r2, created2, _ := m.Apply(ctx, mkSkill("a", "public", nil))
	if created2 {
		t.Fatal("re-apply of same name/tag should not report created")
	}
	if r2.Metadata.ContentHash == "" {
		t.Fatal("content hash should be set")
	}
}

func TestListVisibilityPreFilterBeforeSelector(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	// Two skills with the same label; one public, one private.
	_, _, _ = m.Apply(ctx, mkSkill("pub", "public", map[string]string{"team": "x"}))
	_, _, _ = m.Apply(ctx, mkSkill("priv", "private", map[string]string{"team": "x"}))

	sel, _ := selector.Parse("team=x")
	// Anonymous reader: can only see public, even though both match the selector.
	canRead := func(o v1alpha1.Object) bool { return o.Metadata.Visibility == v1alpha1.VisibilityPublic }
	res, _ := m.List(ctx, ListOptions{Kind: v1alpha1.KindSkill, Namespace: "default", Selector: sel, LatestOnly: true, CanRead: canRead})
	if len(res.Items) != 1 || res.Items[0].Metadata.Name != "pub" {
		t.Fatalf("selector must not widen visibility: got %d items %v", len(res.Items), names(res.Items))
	}
}

func TestSoftDeleteHidesFromList(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	_, _, _ = m.Apply(ctx, mkSkill("gone", "public", nil))
	if err := m.Delete(ctx, v1alpha1.KindSkill, "default", "gone", "latest"); err != nil {
		t.Fatal(err)
	}
	res, _ := m.List(ctx, ListOptions{Kind: v1alpha1.KindSkill, Namespace: "default", LatestOnly: true})
	if len(res.Items) != 0 {
		t.Fatalf("soft-deleted artifact should be hidden, got %v", names(res.Items))
	}
	if _, err := m.Get(ctx, v1alpha1.KindSkill, "default", "gone", "latest"); err != ErrNotFound {
		t.Fatalf("Get of deleted should be ErrNotFound, got %v", err)
	}
}

func names(items []v1alpha1.Object) []string {
	out := make([]string, len(items))
	for i, o := range items {
		out[i] = o.Metadata.Name
	}
	return out
}
