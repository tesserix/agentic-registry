package v1alpha1

import "testing"

func TestNormalizeAPIVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"registry.solo.io/v1alpha1", GroupVersion, false}, // devai's seeds
		{"ar.dev/v1alpha1", GroupVersion, false},           // real solo aregistry
		{"registry.agentic.dev/v1alpha1", GroupVersion, false},
		{"", GroupVersion, false}, // tolerate empty
		{"example.com/v1", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeAPIVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeAPIVersion(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeAPIVersion(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("NormalizeAPIVersion(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizedDefaultsAndSystemLabels(t *testing.T) {
	o := Object{Kind: KindSkill, Metadata: ObjectMeta{Name: "x", Visibility: VisibilityPublic}}
	n := o.Normalized()
	if n.Metadata.Namespace != DefaultNamespace {
		t.Errorf("namespace=%q, want default", n.Metadata.Namespace)
	}
	if n.Metadata.Tag != DefaultTag {
		t.Errorf("tag=%q, want latest", n.Metadata.Tag)
	}
	if n.Metadata.TenantID != DefaultNamespace {
		t.Errorf("tenant should default to namespace, got %q", n.Metadata.TenantID)
	}
	// System labels stamped and cannot be omitted.
	if n.Metadata.Labels["registry.agentic.dev/tenant"] != DefaultNamespace {
		t.Error("system tenant label not stamped")
	}
	if n.Metadata.Labels["registry.agentic.dev/visibility"] != "public" {
		t.Error("system visibility label not stamped")
	}
}

func TestContentHashStableAndContentSensitive(t *testing.T) {
	a := Object{Kind: KindSkill, Metadata: ObjectMeta{Name: "x", Tag: "latest"}, Spec: map[string]interface{}{"k": "v"}}
	b := a // identical
	if a.ContentHash() != b.ContentHash() {
		t.Error("identical content must hash equal (idempotency)")
	}
	c := a
	c.Spec = map[string]interface{}{"k": "different"}
	if a.ContentHash() == c.ContentHash() {
		t.Error("different spec must hash differently")
	}
}

func TestValidateRejectsUnknownKind(t *testing.T) {
	o := Object{Kind: "Nope", Metadata: ObjectMeta{Name: "x"}}
	if err := o.Validate(); err == nil {
		t.Error("expected error for unknown kind")
	}
	ok := Object{Kind: KindProject, Metadata: ObjectMeta{Name: "x"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("Project (devai alias) should be accepted, got %v", err)
	}
}
