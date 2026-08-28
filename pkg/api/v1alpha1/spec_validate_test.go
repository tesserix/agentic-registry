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

func TestValidateSpecPortableAgentRequiresPinnedContainer(t *testing.T) {
	valid := map[string]interface{}{
		"definitionVersion": "v1",
		"framework":         "tesserix-adk",
		"runtime": map[string]interface{}{
			"type":       "container",
			"protocol":   "a2a",
			"image":      "ghcr.io/acme/support-agent@sha256:" + strings.Repeat("a", 64),
			"port":       9090,
			"path":       "/a2a/v1",
			"healthPath": "/readyz",
		},
	}
	if err := ValidateSpec(KindAgent, valid); err != nil {
		t.Fatalf("valid portable agent: %v", err)
	}

	valid["runtime"].(map[string]interface{})["image"] = "ghcr.io/acme/support-agent:latest"
	err := ValidateSpec(KindAgent, valid)
	if err == nil {
		t.Fatal("portable container agent with a mutable image tag must be rejected")
	}
	if !strings.Contains(err.Error(), "spec.runtime.image") {
		t.Fatalf("error must identify the mutable image: %v", err)
	}
}

func TestValidateSpecPortableAgentRuntimeIsCompleteAndUnambiguous(t *testing.T) {
	base := func() map[string]interface{} {
		return map[string]interface{}{
			"definitionVersion": "v1",
			"framework":         "langgraph",
			"runtime": map[string]interface{}{
				"type":     "remote",
				"protocol": "a2a",
				"url":      "https://agents.example.com/support",
				"auth": map[string]interface{}{
					"type":          "bearer",
					"credentialRef": "openbao://agents/support-token",
				},
			},
		}
	}
	if err := ValidateSpec(KindAgent, base()); err != nil {
		t.Fatalf("valid remote portable agent: %v", err)
	}

	tests := []struct {
		name  string
		edit  func(map[string]interface{})
		field string
	}{
		{"unsupported definition", func(spec map[string]interface{}) { spec["definitionVersion"] = "v2" }, "spec.definitionVersion"},
		{"missing framework", func(spec map[string]interface{}) { delete(spec, "framework") }, "spec.framework"},
		{"missing runtime", func(spec map[string]interface{}) { delete(spec, "runtime") }, "spec.runtime"},
		{"unknown runtime", func(spec map[string]interface{}) { spec["runtime"].(map[string]interface{})["type"] = "process" }, "spec.runtime.type"},
		{"unknown protocol", func(spec map[string]interface{}) { spec["runtime"].(map[string]interface{})["protocol"] = "stdio" }, "spec.runtime.protocol"},
		{"insecure remote", func(spec map[string]interface{}) {
			spec["runtime"].(map[string]interface{})["url"] = "http://agents.example.com/support"
		}, "spec.runtime.url"},
		{"remote with image", func(spec map[string]interface{}) {
			spec["runtime"].(map[string]interface{})["image"] = "ghcr.io/acme/a@sha256:" + strings.Repeat("a", 64)
		}, "spec.runtime.image"},
		{"remote without auth", func(spec map[string]interface{}) {
			delete(spec["runtime"].(map[string]interface{}), "auth")
		}, "spec.runtime.auth"},
		{"remote inline token", func(spec map[string]interface{}) {
			spec["runtime"].(map[string]interface{})["auth"] = map[string]interface{}{
				"type": "bearer", "token": "secret",
			}
		}, "spec.runtime.auth.token"},
		{"invalid port", func(spec map[string]interface{}) {
			spec["runtime"].(map[string]interface{})["port"] = 70000
		}, "spec.runtime.port"},
		{"unsafe path", func(spec map[string]interface{}) {
			spec["runtime"].(map[string]interface{})["path"] = "/../admin"
		}, "spec.runtime.path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := base()
			tc.edit(spec)
			err := ValidateSpec(KindAgent, spec)
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("want %s error, got %v", tc.field, err)
			}
		})
	}
}

func TestValidateSpecPortableAgentPinsRegistryDependencies(t *testing.T) {
	base := func(skill interface{}) map[string]interface{} {
		return map[string]interface{}{
			"definitionVersion": "v1",
			"framework":         "langgraph",
			"runtime": map[string]interface{}{
				"type":     "remote",
				"protocol": "a2a",
				"url":      "https://agents.example.com/support",
				"auth": map[string]interface{}{
					"type":          "bearer",
					"credentialRef": "openbao://agents/support-token",
				},
			},
			"skills": []interface{}{skill},
		}
	}
	if err := ValidateSpec(KindAgent, base(map[string]interface{}{
		"ref": "triage", "version": "1.0.0",
	})); err != nil {
		t.Fatalf("versioned dependency: %v", err)
	}

	for name, skill := range map[string]interface{}{
		"bare name":       "triage",
		"missing version": map[string]interface{}{"ref": "triage"},
		"moving latest":   map[string]interface{}{"ref": "triage", "version": "latest"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateSpec(KindAgent, base(skill))
			if err == nil || !strings.Contains(err.Error(), "spec.skills[0]") {
				t.Fatalf("want pinned skill reference error, got %v", err)
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
