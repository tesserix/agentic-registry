package v1alpha1

import "testing"

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
