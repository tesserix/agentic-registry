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

func TestValidateTenantNamespaceInvariant(t *testing.T) {
	// Decoupled tenantId != namespace is rejected (would let an artifact claim a
	// tenant other than the namespace it lives in).
	bad := Object{Kind: KindSkill, Metadata: ObjectMeta{Name: "x", Namespace: "team-a", TenantID: "team-b"}}
	if err := bad.Validate(); err == nil {
		t.Error("expected error when tenantId != namespace")
	}
	// Empty tenantId is fine (Normalized defaults it to namespace).
	empty := Object{Kind: KindSkill, Metadata: ObjectMeta{Name: "x", Namespace: "team-a"}}
	if err := empty.Validate(); err != nil {
		t.Errorf("empty tenantId should be accepted, got %v", err)
	}
	// Explicit tenantId equal to namespace is fine.
	matched := Object{Kind: KindSkill, Metadata: ObjectMeta{Name: "x", Namespace: "team-a", TenantID: "team-a"}}
	if err := matched.Validate(); err != nil {
		t.Errorf("tenantId == namespace should be accepted, got %v", err)
	}
	// tenantId set with empty namespace must equal the default namespace.
	defaulted := Object{Kind: KindSkill, Metadata: ObjectMeta{Name: "x", TenantID: DefaultNamespace}}
	if err := defaulted.Validate(); err != nil {
		t.Errorf("tenantId == default namespace should be accepted, got %v", err)
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

func TestDatasetAndEvalSuiteKindsHaveStableCollections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind   Kind
		plural string
	}{
		{KindDataset, "datasets"},
		{KindEvalSuite, "evalsuites"},
	}
	for _, tc := range cases {
		t.Run(tc.plural, func(t *testing.T) {
			t.Parallel()
			if got := Plural(tc.kind); got != tc.plural {
				t.Fatalf("Plural(%q) = %q, want %q", tc.kind, got, tc.plural)
			}
			if got, ok := KindForPlural(tc.plural); !ok || got != tc.kind {
				t.Fatalf("KindForPlural(%q) = %q, %v", tc.plural, got, ok)
			}
		})
	}
}

func TestGatewayPluralAliases(t *testing.T) {
	for _, tc := range []struct {
		plural string
		kind   Kind
	}{
		{plural: "mcp-servers", kind: KindMCPServer},
		{plural: "eval-suites", kind: KindEvalSuite},
	} {
		if got, ok := KindForPlural(tc.plural); !ok || got != tc.kind {
			t.Errorf("KindForPlural(%q) = %q, %v", tc.plural, got, ok)
		}
	}
}
