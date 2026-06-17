package kagent

import (
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func hasError(issues []Issue, field string) bool {
	for _, i := range issues {
		if i.Severity == "error" && i.Field == field {
			return true
		}
	}
	return false
}

// An agent with an inline systemPrompt + a ModelConfig renders a valid CR.
func TestValidate_OK(t *testing.T) {
	issues := Validate(sampleAgent(), nil, Options{ModelConfigRef: "kagent-mc-anthropic"})
	for _, i := range issues {
		if i.Severity == "error" {
			t.Fatalf("expected no errors, got %+v", issues)
		}
	}
}

// A prompt-by-reference agent (no inline systemPrompt, no description) → empty
// systemMessage → ERROR, unless the export resolved promptRef into SystemPrompt.
func TestValidate_EmptySystemMessageErrors(t *testing.T) {
	a := v1alpha1.Object{
		Kind:     v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{Name: "needs-prompt"},
		Spec:     map[string]interface{}{"promptRef": "needs-prompt-v1"},
	}
	issues := Validate(a, nil, Options{ModelConfigRef: "kagent-mc-anthropic"})
	if !hasError(issues, "spec.declarative.systemMessage") {
		t.Fatalf("expected systemMessage error, got %+v", issues)
	}
	// With the resolved prompt injected, the error goes away.
	ok := Validate(a, nil, Options{ModelConfigRef: "kagent-mc-anthropic", SystemPrompt: "You are X."})
	if hasError(ok, "spec.declarative.systemMessage") {
		t.Fatalf("resolved prompt should clear the systemMessage error, got %+v", ok)
	}
}

// ModelConfig is defaulted by withDefaults(), so a render always has one — the
// validation never errors on it in practice (documented here).
func TestValidate_ModelConfigDefaulted(t *testing.T) {
	issues := Validate(sampleAgent(), nil, Options{})
	if hasError(issues, "spec.declarative.modelConfig") {
		t.Fatalf("modelConfig is defaulted, should not error: %+v", issues)
	}
}

// Tools render as v1alpha1 ToolServer refs → WARNING (not an error).
func TestValidate_ToolsWarn(t *testing.T) {
	issues := Validate(sampleAgent(), []v1alpha1.Object{mcp("github", "https://gh/mcp")}, Options{ModelConfigRef: "mc"})
	warned := false
	for _, i := range issues {
		if i.Severity == "error" {
			t.Fatalf("tools should warn, not error: %+v", issues)
		}
		if i.Field == "spec.declarative.tools" && i.Severity == "warning" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected a tools warning, got %+v", issues)
	}
}
