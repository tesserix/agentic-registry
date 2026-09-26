package v1alpha1

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
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
	case KindGatewayResource:
		errs = validateGatewayResourceSpec(spec)
	default:
		return nil // Skill, Prompt, Project: free-form
	}
	if len(errs) == 0 {
		return nil
	}
	return &SpecError{Errors: errs}
}

var kubernetesName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

var gatewayResourceGVKs = map[string]string{
	"agentgateway.dev/v1alpha1/AgentgatewayBackend": "backend",
	"agentgateway.dev/v1alpha1/AgentgatewayPolicy":  "policy",
	"gateway.networking.k8s.io/v1/HTTPRoute":        "route",
}

var secretFieldNames = map[string]bool{
	"apikey": true, "clientsecret": true, "credentials": true,
	"password": true, "secret": true, "token": true,
}

func validateGatewayResourceSpec(spec map[string]interface{}) []FieldError {
	var errs []FieldError
	allowedTop := map[string]bool{"apiVersion": true, "kind": true, "metadata": true, "spec": true}
	for key := range spec {
		if !allowedTop[key] {
			errs = append(errs, FieldError{"spec." + key, "field is not allowed on a GatewayResource"})
		}
	}
	apiVersion, _ := spec["apiVersion"].(string)
	kind, _ := spec["kind"].(string)
	if gatewayResourceGVKs[apiVersion+"/"+kind] == "" {
		errs = append(errs, FieldError{"spec.kind", "resource GVK is not allowlisted"})
	}
	metadata, ok := spec["metadata"].(map[string]interface{})
	if !ok {
		return append(errs, FieldError{"spec.metadata", "must be an object"})
	}
	for key := range metadata {
		if key != "name" && key != "namespace" {
			errs = append(errs, FieldError{"spec.metadata." + key, "field is server-managed"})
		}
	}
	name, _ := metadata["name"].(string)
	if len(name) == 0 || len(name) > 253 || !kubernetesName.MatchString(name) {
		errs = append(errs, FieldError{"spec.metadata.name", "must be a valid DNS subdomain name"})
	}
	if namespace, _ := metadata["namespace"].(string); namespace != "" && namespace != "agentgateway-system" {
		errs = append(errs, FieldError{"spec.metadata.namespace", "must be agentgateway-system"})
	}
	resourceSpec, ok := spec["spec"].(map[string]interface{})
	if !ok || len(resourceSpec) == 0 {
		errs = append(errs, FieldError{"spec.spec", "must be a non-empty object"})
	} else {
		errs = append(errs, findSecretFields(resourceSpec, "spec.spec")...)
	}
	return errs
}

func findSecretFields(value interface{}, path string) []FieldError {
	var errs []FieldError
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			if normalized == "credentials" && strings.HasSuffix(path, ".auth") && credentialSecretRefs(child) {
				continue
			}
			if secretFieldNames[normalized] {
				errs = append(errs, FieldError{path + "." + key, "secret material is not allowed; use workload identity or a Kubernetes secret reference owned by GitOps"})
				continue
			}
			errs = append(errs, findSecretFields(child, path+"."+key)...)
		}
	case []interface{}:
		for i, child := range typed {
			errs = append(errs, findSecretFields(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return errs
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
	DefinitionVersion string            `json:"definitionVersion"`
	Framework         string            `json:"framework"`
	Runtime           *agentRuntimeView `json:"runtime"`
	Model             *modelView        `json:"model"`
	SystemPrompt      *string           `json:"systemPrompt"`
	Skills            []ref             `json:"skills"`
	Tools             []ref             `json:"tools"`
	MCPServers        []ref             `json:"mcpServers"`
	Prompts           []ref             `json:"prompts"`
}

type agentRuntimeView struct {
	Type       string                `json:"type"`
	Protocol   string                `json:"protocol"`
	Image      string                `json:"image"`
	URL        string                `json:"url"`
	Port       *int                  `json:"port"`
	Path       string                `json:"path"`
	HealthPath string                `json:"healthPath"`
	Auth       *agentRuntimeAuthView `json:"auth"`
}

type agentRuntimeAuthView struct {
	Type          string `json:"type"`
	CredentialRef string `json:"credentialRef"`
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
	if v.DefinitionVersion != "" && v.DefinitionVersion != "v1" {
		errs = append(errs, FieldError{"spec.definitionVersion", "must be v1"})
	}
	if v.DefinitionVersion == "v1" {
		if strings.TrimSpace(v.Framework) == "" {
			errs = append(errs, FieldError{"spec.framework", "required for a portable agent"})
		}
		if v.Runtime == nil {
			errs = append(errs, FieldError{"spec.runtime", "required for a portable agent"})
		} else {
			errs = append(errs, validatePortableRuntime(*v.Runtime)...)
			if runtime, ok := spec["runtime"].(map[string]interface{}); ok {
				errs = append(errs, validatePortableAuthFields(runtime)...)
			}
		}
		errs = append(errs, validatePortableRefs("skills", v.Skills)...)
		errs = append(errs, validatePortableRefs("tools", v.Tools)...)
		errs = append(errs, validatePortableRefs("mcpServers", v.MCPServers)...)
		errs = append(errs, validatePortableRefs("prompts", v.Prompts)...)
	}
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

var immutableOCIImage = regexp.MustCompile(`^[^[:space:]@]+@sha256:[a-fA-F0-9]{64}$`)

func validatePortableRuntime(runtime agentRuntimeView) []FieldError {
	var errs []FieldError
	if runtime.Protocol != "a2a" && runtime.Protocol != "http" {
		errs = append(errs, FieldError{"spec.runtime.protocol", "must be a2a or http"})
	}
	if runtime.Port != nil && (*runtime.Port < 1 || *runtime.Port > 65535) {
		errs = append(errs, FieldError{"spec.runtime.port", "must be between 1 and 65535"})
	}
	if runtime.Path != "" && !safeRuntimePath(runtime.Path) {
		errs = append(errs, FieldError{"spec.runtime.path", "must be an absolute path without traversal"})
	}
	if runtime.HealthPath != "" && !safeRuntimePath(runtime.HealthPath) {
		errs = append(errs, FieldError{"spec.runtime.healthPath", "must be an absolute path without traversal"})
	}
	switch runtime.Type {
	case "container":
		if !immutableOCIImage.MatchString(runtime.Image) {
			errs = append(errs, FieldError{
				"spec.runtime.image",
				"container runtime image must be pinned by sha256 digest",
			})
		}
		if runtime.URL != "" {
			errs = append(errs, FieldError{"spec.runtime.url", "must be omitted for a container runtime"})
		}
		if runtime.Auth != nil {
			errs = append(errs, FieldError{"spec.runtime.auth", "must be omitted for a container runtime"})
		}
	case "remote":
		parsed, err := url.Parse(runtime.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			errs = append(errs, FieldError{"spec.runtime.url", "remote runtime must use an absolute HTTPS URL without user info"})
		}
		if runtime.Image != "" {
			errs = append(errs, FieldError{"spec.runtime.image", "must be omitted for a remote runtime"})
		}
		if runtime.Auth == nil {
			errs = append(errs, FieldError{"spec.runtime.auth", "authenticated remote runtimes require bearer credentials"})
		} else {
			if runtime.Auth.Type != "bearer" {
				errs = append(errs, FieldError{"spec.runtime.auth.type", "must be bearer"})
			}
			if strings.TrimSpace(runtime.Auth.CredentialRef) == "" {
				errs = append(errs, FieldError{"spec.runtime.auth.credentialRef", "must reference server-managed credential material"})
			}
		}
	default:
		errs = append(errs, FieldError{"spec.runtime.type", "must be container or remote"})
	}
	return errs
}

func safeRuntimePath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

func validatePortableAuthFields(runtime map[string]interface{}) []FieldError {
	auth, ok := runtime["auth"].(map[string]interface{})
	if !ok {
		return nil
	}
	allowed := map[string]bool{"type": true, "credentialRef": true}
	for key := range auth {
		if !allowed[key] {
			return []FieldError{{"spec.runtime.auth." + key, "credential material and unknown fields are not allowed"}}
		}
	}
	return nil
}

func validatePortableRefs(field string, refs []ref) []FieldError {
	var errs []FieldError
	for i, item := range refs {
		path := fmt.Sprintf("spec.%s[%d]", field, i)
		if item.Inline == nil {
			errs = append(errs, FieldError{path, "registry dependency must use an object ref with an explicit version"})
			continue
		}
		refName, hasRef := item.Inline["ref"]
		if !hasRef {
			continue
		}
		name, ok := refName.(string)
		if !ok || strings.TrimSpace(name) == "" {
			errs = append(errs, FieldError{path + ".ref", "must be a non-empty artifact name"})
		}
		version, ok := item.Inline["version"].(string)
		if !ok || strings.TrimSpace(version) == "" || version == DefaultTag {
			errs = append(errs, FieldError{path + ".version", "must pin an immutable version"})
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
	if raw, present := spec["credentialRef"]; present {
		ref, ok := raw.(map[string]interface{})
		if !ok {
			return []FieldError{{"spec.credentialRef", "must be an object"}}
		}
		// The upstream credential is brokered by the gateway from a Secret the
		// platform owns; the catalog only ever carries the reference.
		errs := findSecretFields(ref, "spec.credentialRef")
		if name, _ := ref["secretName"].(string); name == "" {
			errs = append(errs, FieldError{"spec.credentialRef.secretName", "required: name the Secret the gateway reads the credential from"})
		}
		if len(errs) > 0 {
			return errs
		}
	}
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

func credentialSecretRefs(value interface{}) bool {
	entries, ok := value.([]interface{})
	if !ok || len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		credential, ok := entry.(map[string]interface{})
		if !ok || len(credential) != 2 {
			return false
		}
		ref, ok := credential["secretRef"].(map[string]interface{})
		if !ok || len(ref) != 2 {
			return false
		}
		name, _ := ref["name"].(string)
		key, _ := ref["key"].(string)
		if !kubernetesName.MatchString(name) || len(name) > 253 || key == "" {
			return false
		}
		location, ok := credential["location"].(map[string]interface{})
		if !ok || len(location) != 1 {
			return false
		}
		header, ok := location["header"].(map[string]interface{})
		if !ok || len(header) != 1 {
			return false
		}
		headerName, _ := header["name"].(string)
		if headerName == "" {
			return false
		}
	}
	return true
}
