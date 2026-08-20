package probe

import (
	"testing"
	"time"
)

var probedAt = time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

func declared(tools ...string) map[string]interface{} {
	list := make([]interface{}, 0, len(tools))
	for _, t := range tools {
		list = append(list, t)
	}
	return map[string]interface{}{"name": "homechef-mcp", "tools": list}
}

func TestDeclaredHash_OrderIndependent(t *testing.T) {
	if a, b := DeclaredHash(declared("b", "a")), DeclaredHash(declared("a", "b")); a != b {
		t.Errorf("declaration order must not change the hash: %s != %s", a, b)
	}
}

func TestDeclaredHash_DistinguishesToolSets(t *testing.T) {
	if a, b := DeclaredHash(declared("a")), DeclaredHash(declared("a", "delete_everything")); a == b {
		t.Error("an added tool must change the hash")
	}
}

// spec.tools may carry objects (name + schema) rather than bare strings.
func TestDeclaredHash_AcceptsToolObjects(t *testing.T) {
	spec := map[string]interface{}{"tools": []interface{}{
		map[string]interface{}{"name": "get_order_status"},
	}}
	if DeclaredHash(spec) != DeclaredHash(declared("get_order_status")) {
		t.Error("tool objects and tool names must hash alike")
	}
}

func TestStatusFor_ReadyWhenObservationMatchesDeclaration(t *testing.T) {
	status := StatusFor(declared("a", "b"), Observation{
		Reachable:       true,
		Tools:           []string{"b", "a"},
		ProtocolVersion: "2025-06-18",
		ProbedAt:        probedAt,
	})

	if status["observedHash"] != status["declaredHash"] {
		t.Errorf("hashes must match: %v vs %v", status["observedHash"], status["declaredHash"])
	}
	if got := status["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion: got %v", got)
	}
	if got := status["lastProbedAt"]; got != "2026-08-20T10:00:00Z" {
		t.Errorf("lastProbedAt: got %v", got)
	}
	if got := status["observedTools"].([]string); len(got) != 2 || got[0] != "a" {
		t.Errorf("observedTools must be sorted: got %v", got)
	}
	assertCondition(t, status, "Ready", "True", "Probed")
	assertCondition(t, status, "Drifted", "False", "InSync")
	assertCondition(t, status, "Unreachable", "False", "Probed")
}

func TestStatusFor_DriftedWhenServerGrewATool(t *testing.T) {
	status := StatusFor(declared("get_order_status"), Observation{
		Reachable: true,
		Tools:     []string{"get_order_status", "delete_order"},
		ProbedAt:  probedAt,
	})

	// It answers, so it is Ready; the declaration is what is stale.
	assertCondition(t, status, "Ready", "True", "Probed")
	cond := assertCondition(t, status, "Drifted", "True", "CapabilityDrift")
	if msg, _ := cond["message"].(string); msg != "undeclared tools: delete_order" {
		t.Errorf("message must name the undeclared tool: got %q", msg)
	}
}

func TestStatusFor_DriftedWhenDeclaredToolIsMissing(t *testing.T) {
	status := StatusFor(declared("get_order_status", "track_delivery"), Observation{
		Reachable: true,
		Tools:     []string{"get_order_status"},
		ProbedAt:  probedAt,
	})
	cond := assertCondition(t, status, "Drifted", "True", "CapabilityDrift")
	if msg, _ := cond["message"].(string); msg != "missing tools: track_delivery" {
		t.Errorf("message: got %q", msg)
	}
}

func TestStatusFor_UnreachableKeepsNoObservedTools(t *testing.T) {
	status := StatusFor(declared("a"), Observation{
		Reachable: false,
		Error:     "dial tcp: connection refused",
		ProbedAt:  probedAt,
	})

	assertCondition(t, status, "Ready", "False", "Unreachable")
	cond := assertCondition(t, status, "Unreachable", "True", "ProbeFailed")
	if msg, _ := cond["message"].(string); msg != "dial tcp: connection refused" {
		t.Errorf("probe error must reach the condition: got %q", msg)
	}
	if _, ok := status["observedTools"]; ok {
		t.Error("a failed probe must not publish an empty tool set as fact")
	}
	if _, ok := status["observedHash"]; ok {
		t.Error("a failed probe must not publish an observed hash")
	}
}

// A probe is an observation; it may never rewrite the operator's declaration.
func TestStatusFor_LeavesSpecUntouched(t *testing.T) {
	spec := declared("a")
	StatusFor(spec, Observation{Reachable: true, Tools: []string{"b"}, ProbedAt: probedAt})
	tools := spec["tools"].([]interface{})
	if len(tools) != 1 || tools[0] != "a" {
		t.Errorf("spec mutated: %v", tools)
	}
}

func TestStatusFor_ProposesNextVersion(t *testing.T) {
	added := StatusFor(map[string]interface{}{
		"version": "1.2.0",
		"tools":   []interface{}{"a"},
	}, Observation{Reachable: true, Tools: []string{"a", "b"}, ProbedAt: probedAt})
	if got := added["proposedVersion"]; got != "1.3.0" {
		t.Errorf("added tools are a minor bump: got %v", got)
	}

	removed := StatusFor(map[string]interface{}{
		"version": "1.2.0",
		"tools":   []interface{}{"a", "b"},
	}, Observation{Reachable: true, Tools: []string{"a"}, ProbedAt: probedAt})
	if got := removed["proposedVersion"]; got != "2.0.0" {
		t.Errorf("removed tools are a major bump: got %v", got)
	}

	inSync := StatusFor(map[string]interface{}{
		"version": "1.2.0",
		"tools":   []interface{}{"a"},
	}, Observation{Reachable: true, Tools: []string{"a"}, ProbedAt: probedAt})
	if _, ok := inSync["proposedVersion"]; ok {
		t.Error("no drift must propose no version")
	}
}

func assertCondition(t *testing.T, status map[string]interface{}, condType, want, reason string) map[string]interface{} {
	t.Helper()
	conditions, ok := status["conditions"].([]map[string]interface{})
	if !ok {
		t.Fatalf("no conditions in %v", status)
	}
	for _, c := range conditions {
		if c["type"] != condType {
			continue
		}
		if c["status"] != want {
			t.Errorf("%s: got status %v, want %s", condType, c["status"], want)
		}
		if c["reason"] != reason {
			t.Errorf("%s: got reason %v, want %s", condType, c["reason"], reason)
		}
		return c
	}
	t.Fatalf("condition %s missing from %v", condType, conditions)
	return nil
}
