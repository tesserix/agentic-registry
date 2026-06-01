package v1alpha1

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Per-kind spec validation.
//
// The registry stores spec as a free-form map[string]interface{} and that does
// NOT change here. These typed "views" exist ONLY to validate: we marshal the
// stored map into the view, check the KNOWN fields, then discard the view and
// store the original map untouched. This keeps forward-compatibility (unknown
// fields pass through, exactly like the A2A card renderer) while giving authors
// real feedback when a known field has the wrong shape.
//
// Validation is deliberately LENIENT: it only rejects wrong types on known
// fields and missing required sub-fields. It never rejects unknown fields, and
// reference *existence* (does spec.skills[0] name a real Skill?) is checked
// elsewhere, behind a flag — so every existing seed, solo.io/ar.dev manifest,
// and bare server.json keeps applying.

// FieldError is a single structured validation problem with a dot-path.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// SpecError aggregates field errors for one object's spec.
type SpecError struct {
	Errors []FieldError
}

func (e *SpecError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		parts[i] = fe.Field + ": " + fe.Message
	}
	return "spec validation failed: " + strings.Join(parts, "; ")
}

// Fields exposes the structured errors for callers that emit a JSON body.
func (e *SpecError) Fields() []FieldError { return e.Errors }

// ValidateSpec checks the known fields of spec for the given kind. It returns
// nil for kinds without a schema (Project), an empty spec, or a spec that only
// uses unknown/extra fields. A non-nil error is always a *SpecError.
func ValidateSpec(kind Kind, spec map[string]interface{}) error {
	if len(spec) == 0 {
		return nil
	}
	var errs []FieldError
	switch kind {
	case KindAgent:
		errs = validateAgentSpec(spec)
	case KindTool:
		errs = validateToolSpec(spec)
	case KindWorkflow, KindBlueprint:
		errs = validateGraphSpec(spec)
	case KindMCPServer:
		errs = validateMCPSpec(spec)
	default:
		return nil // Skill, Prompt, Project: free-form
	}
	if len(errs) == 0 {
		return nil
	}
	return &SpecError{Errors: errs}
}

// decode marshals the free-form spec and unmarshals into v WITHOUT
// DisallowUnknownFields (unknown fields must pass through). A decode failure
// means a known field carried an incompatible JSON type.
func decode(spec map[string]interface{}, v interface{}) error {
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ---- Agent ----------------------------------------------------------------

type agentView struct {
	Model        *modelView `json:"model"`
	SystemPrompt *string    `json:"systemPrompt"`
	Skills       []ref      `json:"skills"`
	Tools        []ref      `json:"tools"`
	MCPServers   []ref      `json:"mcpServers"`
	Prompts      []ref      `json:"prompts"`
}

type modelView struct {
	Provider    string   `json:"provider"`
	Name        string   `json:"name"`
	Temperature *float64 `json:"temperature"`
}

// ref is the polymorphic reference used by skills/tools/mcpServers/prompts and
// by graph nodes: EITHER a bare string (a registry name) OR an object that may
// carry an explicit name/id plus inline overrides. Mirrors the A2A skill
// resolver so inline skills keep working.
type ref struct {
	Name   string
	Inline map[string]interface{}
}

func (r *ref) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &r.Name)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	r.Inline = m
	if s, ok := m["name"].(string); ok {
		r.Name = s
	} else if s, ok := m["id"].(string); ok {
		r.Name = s
	}
	return nil
}

func validateAgentSpec(spec map[string]interface{}) []FieldError {
	var v agentView
	if err := decode(spec, &v); err != nil {
		return typeErrors("spec", err,
			"model", "systemPrompt", "skills", "tools", "mcpServers", "prompts")
	}
	var errs []FieldError
	if v.Model != nil {
		if strings.TrimSpace(v.Model.Provider) == "" {
			errs = append(errs, FieldError{"spec.model.provider", "required when model is set"})
		}
		if strings.TrimSpace(v.Model.Name) == "" {
			errs = append(errs, FieldError{"spec.model.name", "required when model is set"})
		}
		if v.Model.Temperature != nil && (*v.Model.Temperature < 0 || *v.Model.Temperature > 2) {
			errs = append(errs, FieldError{"spec.model.temperature", "must be between 0 and 2"})
		}
	}
	// Each ref entry must be a string or an object with a name/id. The decoder
	// already enforced that; a pure-inline object with neither name nor id is
	// only flagged for skills (which need an id to render as an A2A skill).
	for i, s := range v.Skills {
		if s.Name == "" && s.Inline == nil {
			errs = append(errs, FieldError{fmt.Sprintf("spec.skills[%d]", i), "must be a name or an object with name/id"})
		}
	}
	return errs
}

// ---- Tool -----------------------------------------------------------------

type toolView struct {
	Inputs  []toolParam `json:"inputs"`
	Outputs []toolParam `json:"outputs"`
}

type toolParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

var toolParamTypes = map[string]bool{
	"": true, "string": true, "number": true, "integer": true,
	"boolean": true, "object": true, "array": true,
}

func validateToolSpec(spec map[string]interface{}) []FieldError {
	var v toolView
	if err := decode(spec, &v); err != nil {
		return typeErrors("spec", err, "inputs", "outputs")
	}
	var errs []FieldError
	check := func(group string, params []toolParam) {
		for i, p := range params {
			if strings.TrimSpace(p.Name) == "" {
				errs = append(errs, FieldError{fmt.Sprintf("spec.%s[%d].name", group, i), "required"})
			}
			if !toolParamTypes[p.Type] {
				errs = append(errs, FieldError{fmt.Sprintf("spec.%s[%d].type", group, i),
					"must be one of string|number|integer|boolean|object|array"})
			}
		}
	}
	check("inputs", v.Inputs)
	check("outputs", v.Outputs)
	return errs
}

// ---- Workflow / Blueprint -------------------------------------------------

type graphView struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

type graphNode struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func validateGraphSpec(spec map[string]interface{}) []FieldError {
	// Graph spec is optional; if neither nodes nor edges is present, skip.
	if spec["nodes"] == nil && spec["edges"] == nil {
		return nil
	}
	var v graphView
	if err := decode(spec, &v); err != nil {
		return typeErrors("spec", err, "nodes", "edges")
	}
	var errs []FieldError
	ids := map[string]bool{}
	for i, n := range v.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			errs = append(errs, FieldError{fmt.Sprintf("spec.nodes[%d].id", i), "required"})
			continue
		}
		ids[n.ID] = true
		if n.Ref != "" && n.Kind != "" && !ValidKind(Kind(n.Kind)) {
			errs = append(errs, FieldError{fmt.Sprintf("spec.nodes[%d].kind", i),
				fmt.Sprintf("unknown kind %q", n.Kind)})
		}
	}
	for i, e := range v.Edges {
		if e.From != "" && !ids[e.From] {
			errs = append(errs, FieldError{fmt.Sprintf("spec.edges[%d].from", i),
				fmt.Sprintf("references unknown node %q", e.From)})
		}
		if e.To != "" && !ids[e.To] {
			errs = append(errs, FieldError{fmt.Sprintf("spec.edges[%d].to", i),
				fmt.Sprintf("references unknown node %q", e.To)})
		}
	}
	return errs
}

// ---- MCPServer ------------------------------------------------------------

// MCPServer stays server.json-lenient (it is the OCI MCP Registry interop
// contract). We only require a name; packages/remotes shape is not enforced.
func validateMCPSpec(spec map[string]interface{}) []FieldError {
	if _, ok := spec["name"]; !ok {
		// The publish paths set metadata.name; spec.name is the server.json
		// field. Only flag when the spec is clearly a server body missing its
		// required name and is otherwise non-trivial.
		if len(spec) > 0 {
			if _, hasDesc := spec["description"]; hasDesc {
				return []FieldError{{"spec.name", "required for an MCP server body"}}
			}
		}
	}
	return nil
}

// typeErrors converts a json decode failure into a best-effort field error. The
// std-lib error names the offending field (e.g. "cannot unmarshal string into
// Go struct field agentView.skills of type ..."); we surface the field name.
func typeErrors(prefix string, err error, known ...string) []FieldError {
	msg := err.Error()
	for _, k := range known {
		if strings.Contains(msg, "."+k+" ") || strings.Contains(strings.ToLower(msg), strings.ToLower(k)) {
			return []FieldError{{prefix + "." + k, "has the wrong type"}}
		}
	}
	return []FieldError{{prefix, "could not be parsed: " + msg}}
}
