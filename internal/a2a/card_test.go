package a2a

import (
	"maps"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func agent(spec map[string]any) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{Name: "oncall", Namespace: "sre", Tag: "1.2.0"},
		Spec:     spec,
	}
}

func skill(name, title, desc string, labels map[string]string, spec map[string]any) v1alpha1.Object {
	s := map[string]any{"title": title, "description": desc}
	maps.Copy(s, spec)
	return v1alpha1.Object{
		Kind:     v1alpha1.KindSkill,
		Metadata: v1alpha1.ObjectMeta{Name: name, Namespace: "sre", Tag: "latest", Labels: labels},
		Spec:     s,
	}
}

func TestCard_RejectsNonAgent(t *testing.T) {
	_, err := Card(v1alpha1.Object{Kind: v1alpha1.KindSkill}, nil, Options{})
	var nae *NotAnAgentError
	if err == nil {
		t.Fatal("expected NotAnAgentError for a non-Agent kind")
	}
	if !asErr(err, &nae) {
		t.Fatalf("expected *NotAnAgentError, got %T", err)
	}
}

func TestCard_MinimalAgentStillValid(t *testing.T) {
	c, err := Card(agent(map[string]any{"title": "On-Call", "description": "watches alerts"}), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion default wrong: %v", c["protocolVersion"])
	}
	if c["name"] != "On-Call" || c["description"] != "watches alerts" {
		t.Fatalf("name/description not mapped: %v", c)
	}
	if c["version"] != "1.2.0" {
		t.Fatalf("version should come from the tag, got %v", c["version"])
	}
	if c["preferredTransport"] != "JSONRPC" {
		t.Fatalf("default transport wrong: %v", c["preferredTransport"])
	}
	// Empty agent => empty (but present) skills list.
	if sk, ok := c["skills"].([]any); !ok || len(sk) != 0 {
		t.Fatalf("expected empty skills slice, got %v", c["skills"])
	}
}

func TestCard_PassthroughAndDerivedFields(t *testing.T) {
	c, err := Card(agent(map[string]any{
		"title": "On-Call",
		"a2a": map[string]any{
			"url":                "https://oncall.svc/a2a/v1",
			"preferredTransport": "GRPC",
			"capabilities":       map[string]any{"streaming": true},
			"futureField":        "kept", // unknown field must pass through
			"version":            "9.9.9", // must be IGNORED (derived from tag)
		},
	}), nil, Options{RegistryURL: "https://reg.example"})
	if err != nil {
		t.Fatal(err)
	}
	if c["url"] != "https://oncall.svc/a2a/v1" || c["preferredTransport"] != "GRPC" {
		t.Fatalf("a2a block not surfaced: %v", c)
	}
	if c["futureField"] != "kept" {
		t.Fatal("unknown a2a fields must pass through (forward-compat)")
	}
	if c["version"] != "1.2.0" {
		t.Fatalf("publisher version must not override the tag-derived version, got %v", c["version"])
	}
	caps, _ := c["capabilities"].(map[string]any)
	if caps["streaming"] != true {
		t.Fatalf("capabilities flags lost: %v", caps)
	}
	// Provenance extension present and pins the artifact.
	exts, _ := caps["extensions"].([]any)
	if len(exts) != 1 {
		t.Fatalf("expected one provenance extension, got %v", exts)
	}
	ext, _ := exts[0].(map[string]any)
	if ext["uri"] != ProvenanceExtensionURI {
		t.Fatalf("provenance uri wrong: %v", ext["uri"])
	}
	params, _ := ext["params"].(map[string]any)
	if params["registry"] != "https://reg.example" || params["namespace"] != "sre" {
		t.Fatalf("provenance params wrong: %v", params)
	}
	if arn, _ := params["arn"].(string); arn == "" {
		t.Fatal("provenance must carry the agent ARN")
	}
}

func TestCard_LinkedSkillResolvedFromRegistry(t *testing.T) {
	k8s := skill("kubernetes-troubleshooter", "K8s Troubleshooter", "debugs clusters",
		map[string]string{"domain": "sre", "registry.agentic.dev/tenant": "sre"},
		map[string]any{"examples": []any{"pod won't start"}})
	resolve := func(name string) (v1alpha1.Object, bool) {
		if name == "kubernetes-troubleshooter" {
			return k8s, true
		}
		return v1alpha1.Object{}, false
	}
	c, err := Card(agent(map[string]any{
		"skills": []any{
			"kubernetes-troubleshooter",   // linked
			"does-not-exist",              // dangling ref -> skipped
			map[string]any{"name": "Inline Skill", "description": "one-off"}, // inline
		},
	}), resolve, Options{})
	if err != nil {
		t.Fatal(err)
	}
	skills, _ := c["skills"].([]any)
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills (linked + inline, dangling skipped), got %d: %v", len(skills), skills)
	}
	first, _ := skills[0].(map[string]any)
	if first["id"] != "kubernetes-troubleshooter" || first["name"] != "K8s Troubleshooter" {
		t.Fatalf("linked skill mapped wrong: %v", first)
	}
	// Tags derived from labels, excluding registry system labels.
	tags, _ := first["tags"].([]any)
	if len(tags) != 1 || tags[0] != "sre" {
		t.Fatalf("linked skill tags should derive from labels (system labels excluded): %v", tags)
	}
	second, _ := skills[1].(map[string]any)
	if second["id"] != "Inline Skill" { // id defaults to name
		t.Fatalf("inline skill id should default to name: %v", second)
	}
}

// --- tiny errors.As shim so the test file needs no extra import churn ---

func asErr(err error, target **NotAnAgentError) bool {
	e, ok := err.(*NotAnAgentError)
	if ok {
		*target = e
	}
	return ok
}
