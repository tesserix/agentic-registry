package store

import (
	"context"
	"errors"
	"strings"
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

func mkOfKind(kind v1alpha1.Kind, ns, name string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     kind,
		Metadata: v1alpha1.ObjectMeta{Name: name, Namespace: ns},
		Spec:     map[string]interface{}{"description": "test " + name},
	}
}

func TestNameUniqueAcrossKindsInNamespace(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	// A Skill claims "payments" in the civica namespace.
	if _, created, err := m.Apply(ctx, mkOfKind(v1alpha1.KindSkill, "civica", "payments")); err != nil || !created {
		t.Fatalf("first claim should succeed: created=%v err=%v", created, err)
	}

	// A different kind (MCPServer) trying the same name in the same namespace
	// is rejected with a typed, meaningful conflict.
	_, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindMCPServer, "civica", "payments"))
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("cross-kind same-name publish must conflict, got %v", err)
	}
	var nce *NameConflictError
	if !errors.As(err, &nce) {
		t.Fatalf("expected *NameConflictError, got %T", err)
	}
	if nce.OwnerKind != v1alpha1.KindSkill || nce.WantKind != v1alpha1.KindMCPServer {
		t.Fatalf("conflict should name owner=Skill want=MCPServer, got owner=%s want=%s", nce.OwnerKind, nce.WantKind)
	}
	if nce.OwnerARN == "" || !strings.Contains(err.Error(), "payments") {
		t.Fatalf("conflict message must carry name + owner ARN, got %q", err.Error())
	}
}

func TestNameReusableInDifferentNamespace(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	// Same name, different orgs/teams (namespaces) — both allowed.
	if _, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindSkill, "civica", "payments")); err != nil {
		t.Fatalf("civica claim: %v", err)
	}
	if _, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindMCPServer, "zendesk", "payments")); err != nil {
		t.Fatalf("zendesk reuse in another namespace should be allowed, got %v", err)
	}
	if _, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindTool, "civica-teamb", "payments")); err != nil {
		t.Fatalf("team namespace reuse should be allowed, got %v", err)
	}
}

func TestSameKindRepublishIsVersioningNotConflict(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	if _, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindSkill, "civica", "payments")); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	// Re-publishing the SAME kind/name is the owner versioning — never a conflict.
	if _, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindSkill, "civica", "payments")); err != nil {
		t.Fatalf("same-kind re-publish must not conflict, got %v", err)
	}
}

func TestNameFreedAfterSoftDelete(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	if _, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindSkill, "civica", "payments")); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := m.Delete(ctx, v1alpha1.KindSkill, "civica", "payments", "latest"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Once the Skill is soft-deleted, another kind may reclaim the name.
	if _, _, err := m.Apply(ctx, mkOfKind(v1alpha1.KindMCPServer, "civica", "payments")); err != nil {
		t.Fatalf("name should be reclaimable after delete, got %v", err)
	}
}

func mkSkillTenant(ns, name, tenant string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindSkill,
		Metadata: v1alpha1.ObjectMeta{Name: name, Namespace: ns, TenantID: tenant},
		Spec:     map[string]interface{}{"description": "test " + name},
	}
}

func TestApplyRejectsCrossTenantOverwrite(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	// Tenant A publishes payments-skill in a shared namespace.
	if _, _, err := m.Apply(ctx, mkSkillTenant("shared", "payments-skill", "tenant-a")); err != nil {
		t.Fatalf("tenant-a publish: %v", err)
	}
	// Tenant B attempts to overwrite the SAME (kind,namespace,name,tag): must be
	// rejected so it can't reassign ownership/visibility.
	_, _, err := m.Apply(ctx, mkSkillTenant("shared", "payments-skill", "tenant-b"))
	if !errors.Is(err, ErrTenantConflict) {
		t.Fatalf("cross-tenant overwrite must conflict, got %v", err)
	}
	// The artifact must still belong to tenant A.
	got, gerr := m.Get(ctx, v1alpha1.KindSkill, "shared", "payments-skill", "latest")
	if gerr != nil {
		t.Fatalf("get after conflict: %v", gerr)
	}
	if got.Metadata.TenantID != "tenant-a" {
		t.Fatalf("ownership must be unchanged, got tenant %q", got.Metadata.TenantID)
	}
	// Tenant A re-applying to its own artifact is normal versioning, not a conflict.
	if _, _, err := m.Apply(ctx, mkSkillTenant("shared", "payments-skill", "tenant-a")); err != nil {
		t.Fatalf("owner re-apply must succeed, got %v", err)
	}
}

func names(items []v1alpha1.Object) []string {
	out := make([]string, len(items))
	for i, o := range items {
		out[i] = o.Metadata.Name
	}
	return out
}

func mkSkillSpec(name, desc string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindSkill,
		Metadata: v1alpha1.ObjectMeta{Name: name, Visibility: v1alpha1.VisibilityPublic},
		Spec:     map[string]interface{}{"description": desc},
	}
}

func TestAutoVersionReusesTagWhenContentUnchanged(t *testing.T) {
	m := NewMemory()
	m.autoVersion = true
	ctx := context.Background()

	first, _, err := m.Apply(ctx, mkSkillSpec("seeded", "v1"))
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// A seed re-applied unchanged (bootstrap Job re-runs on every sync) must not
	// mint a version — that grew the catalogue to 91 copies of every artifact.
	for i := 0; i < 5; i++ {
		again, created, err := m.Apply(ctx, mkSkillSpec("seeded", "v1"))
		if err != nil {
			t.Fatalf("re-apply %d: %v", i, err)
		}
		if created {
			t.Fatalf("re-apply %d of identical content reported created", i)
		}
		if again.Metadata.Tag != first.Metadata.Tag {
			t.Fatalf("re-apply %d bumped %q -> %q", i, first.Metadata.Tag, again.Metadata.Tag)
		}
	}
	tags, err := m.ListTags(ctx, v1alpha1.KindSkill, v1alpha1.DefaultNamespace, "seeded")
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("want 1 tag after 6 identical applies, got %d: %v", len(tags), tags)
	}
}

func TestAutoVersionBumpsWhenContentChanges(t *testing.T) {
	m := NewMemory()
	m.autoVersion = true
	ctx := context.Background()

	first, _, err := m.Apply(ctx, mkSkillSpec("evolving", "v1"))
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, _, err := m.Apply(ctx, mkSkillSpec("evolving", "v2"))
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second.Metadata.Tag == first.Metadata.Tag {
		t.Fatalf("changed content must publish a new version, both %q", first.Metadata.Tag)
	}
	tags, _ := m.ListTags(ctx, v1alpha1.KindSkill, v1alpha1.DefaultNamespace, "evolving")
	if len(tags) != 2 {
		t.Fatalf("want 2 tags, got %d: %v", len(tags), tags)
	}
}
