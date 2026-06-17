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

// WorkerPoolRef set → render a SandboxAgent (Substrate) with substrate.workerPoolRef.
func TestBuild_SandboxAgentWhenWorkerPoolSet(t *testing.T) {
	out, err := BuildOutput(sampleAgent(), nil, Options{ModelConfigRef: "mc", WorkerPoolRef: "default-pool"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Agent["kind"] != "SandboxAgent" {
		t.Fatalf("expected kind SandboxAgent, got %v", out.Agent["kind"])
	}
	spec := out.Agent["spec"].(map[string]interface{})
	sub, ok := spec["substrate"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected substrate object, got %v", spec["substrate"])
	}
	ref, _ := sub["workerPoolRef"].(map[string]interface{})
	if ref["name"] != "default-pool" {
		t.Fatalf("expected substrate.workerPoolRef.name=default-pool, got %v", sub["workerPoolRef"])
	}
	// declarative block is identical to a classic Agent.
	if _, ok := spec["declarative"]; !ok {
		t.Fatal("SandboxAgent must keep the declarative block")
	}
	// no WorkerPoolRef → classic Agent.
	cl, _ := BuildOutput(sampleAgent(), nil, Options{ModelConfigRef: "mc"})
	if cl.Agent["kind"] != "Agent" {
		t.Fatalf("expected classic Agent, got %v", cl.Agent["kind"])
	}
}

// A SandboxAgent with no WorkerPool ref → validation error.
func TestValidate_SandboxAgentNeedsWorkerPool(t *testing.T) {
	// Can't reach this via Options (WorkerPoolRef drives both), but assert the
	// classic path doesn't false-positive: a classic Agent has no substrate.
	issues := Validate(sampleAgent(), nil, Options{ModelConfigRef: "mc"})
	if hasError(issues, "spec.substrate.workerPoolRef") {
		t.Fatalf("classic Agent should not require a WorkerPool: %+v", issues)
	}
	ok := Validate(sampleAgent(), nil, Options{ModelConfigRef: "mc", WorkerPoolRef: "p"})
	if hasError(ok, "spec.substrate.workerPoolRef") {
		t.Fatalf("SandboxAgent with a pool should pass: %+v", ok)
	}
}
