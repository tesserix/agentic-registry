package v1alpha1

import (
	"strings"
	"testing"
)

func TestValidateSpec_Agent(t *testing.T) {
	cases := []struct {
		name    string
		spec    map[string]interface{}
		wantErr bool
		field   string
	}{
		{"empty ok", map[string]interface{}{}, false, ""},
		{"title only ok", map[string]interface{}{"title": "X", "description": "y"}, false, ""},
		{"string skill refs ok", map[string]interface{}{
			"skills": []interface{}{"code-review", "git-flow"},
		}, false, ""},
		{"inline skill ok", map[string]interface{}{
			"skills": []interface{}{map[string]interface{}{"id": "inline", "name": "Inline"}},
		}, false, ""},
		{"good model ok", map[string]interface{}{
			"model": map[string]interface{}{"provider": "anthropic", "name": "claude", "temperature": 0.3},
		}, false, ""},
		{"model missing name", map[string]interface{}{
			"model": map[string]interface{}{"provider": "anthropic"},
		}, true, "spec.model.name"},
		{"temperature out of range", map[string]interface{}{
			"model": map[string]interface{}{"provider": "a", "name": "b", "temperature": 5},
		}, true, "spec.model.temperature"},
		{"skills wrong type", map[string]interface{}{"skills": "not-a-list"}, true, "spec.skills"},
		{"unknown field passes through", map[string]interface{}{
			"title": "X", "handoverSchema": map[string]interface{}{"foo": "bar"}, "riskLevel": "low",
		}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSpec(KindAgent, tc.spec)
			if tc.wantErr != (err != nil) {
				t.Fatalf("ValidateSpec err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.field != "" {
				se, ok := err.(*SpecError)
				if !ok {
					t.Fatalf("want *SpecError, got %T", err)
				}
				found := false
				for _, fe := range se.Fields() {
					if fe.Field == tc.field {
						found = true
					}
				}
				if !found {
					t.Fatalf("want field %q in errors %+v", tc.field, se.Fields())
				}
			}
		})
	}
}

func TestValidateSpec_Tool(t *testing.T) {
	if err := ValidateSpec(KindTool, map[string]interface{}{
		"inputs": []interface{}{map[string]interface{}{"name": "repo", "type": "string"}},
	}); err != nil {
		t.Fatalf("good tool: %v", err)
	}
	if err := ValidateSpec(KindTool, map[string]interface{}{
		"inputs": []interface{}{map[string]interface{}{"type": "string"}},
	}); err == nil {
		t.Fatal("tool input missing name should fail")
	}
}

func TestValidateSpec_Graph(t *testing.T) {
	if err := ValidateSpec(KindWorkflow, map[string]interface{}{
		"nodes": []interface{}{
			map[string]interface{}{"id": "a"}, map[string]interface{}{"id": "b", "kind": "Agent", "ref": "x"},
		},
		"edges": []interface{}{map[string]interface{}{"from": "a", "to": "b"}},
	}); err != nil {
		t.Fatalf("good graph: %v", err)
	}
	if err := ValidateSpec(KindBlueprint, map[string]interface{}{
		"nodes": []interface{}{map[string]interface{}{"id": "a"}},
		"edges": []interface{}{map[string]interface{}{"from": "a", "to": "missing"}},
	}); err == nil {
		t.Fatal("edge to unknown node should fail")
	}
	if err := ValidateSpec(KindWorkflow, map[string]interface{}{
		"nodes": []interface{}{map[string]interface{}{"id": "a", "kind": "Bogus", "ref": "x"}},
	}); err == nil {
		t.Fatal("node with unknown kind should fail")
	}
}

func TestGatewayResourceValidationRejectsUnsafeKubernetesObjects(t *testing.T) {
	tests := map[string]map[string]interface{}{
		"unknown kind": {
			"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]interface{}{"name": "provider-key"},
			"spec":     map[string]interface{}{"token": "not-allowed"},
		},
		"wrong namespace": {
			"apiVersion": "agentgateway.dev/v1alpha1", "kind": "AgentgatewayBackend",
			"metadata": map[string]interface{}{"name": "openai", "namespace": "kube-system"},
			"spec":     map[string]interface{}{"ai": map[string]interface{}{}},
		},
		"secret material": {
			"apiVersion": "agentgateway.dev/v1alpha1", "kind": "AgentgatewayBackend",
			"metadata": map[string]interface{}{"name": "openai"},
			"spec":     map[string]interface{}{"apiKey": "plaintext"},
		},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSpec(Kind("GatewayResource"), spec); err == nil {
				t.Fatalf("expected unsafe GatewayResource to be rejected: %#v", spec)
			}
		})
	}
}

func TestValidateSpec_MCPCredentialRefMustReferenceASecret(t *testing.T) {
	err := ValidateSpec(KindMCPServer, map[string]interface{}{
		"name":          "jira-mcp",
		"credentialRef": map[string]interface{}{"key": "token"},
	})
	if err == nil {
		t.Fatal("a credentialRef naming no Secret must be rejected at publish time")
	}
}

func TestValidateSpec_MCPCredentialMaterialIsRejectedAtPublish(t *testing.T) {
	err := ValidateSpec(KindMCPServer, map[string]interface{}{
		"name": "jira-mcp",
		"credentialRef": map[string]interface{}{
			"secretName": "jira-upstream",
			"token":      "ghp_live_value",
		},
	})
	if err == nil {
		t.Fatal("the registry must never accept credential material in a manifest")
	}
	if !strings.Contains(err.Error(), "credentialRef.token") {
		t.Errorf("error must name the offending field: %v", err)
	}
}

func TestValidateSpec_MCPCredentialRefIsOptional(t *testing.T) {
	if err := ValidateSpec(KindMCPServer, map[string]interface{}{
		"name":          "jira-mcp",
		"credentialRef": map[string]interface{}{"secretName": "jira-upstream", "key": "token"},
	}); err != nil {
		t.Fatalf("a pure reference must be accepted: %v", err)
	}
	if err := ValidateSpec(KindMCPServer, map[string]interface{}{"name": "jira-mcp"}); err != nil {
		t.Fatalf("credentialRef is optional: %v", err)
	}
}
