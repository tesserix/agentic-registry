// Package discovery builds the secret-safe, progressively fetchable view used
// for search embeddings and registry discovery responses.
package discovery

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

var (
	secretAssignment = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|token|secret|password|authorization|credential)[[:space:]]*[:=][[:space:]]*[^[:space:],;]+`)
	bearerValue      = regexp.MustCompile(`(?i)bearer[[:space:]]+[^[:space:],;]+`)
)

const (
	maxMapEntries = 50
	maxReferences = 100
	maxValueRunes = 512
	maxSearchText = 12_000
)

var annotationPrefixes = []string{
	"discovery.agentic.dev/",
	"ai.tesserix.dev/",
	"devai.io/",
	"devai.tesserix.app/",
	"mcp.devai.io/",
}

var attributeFields = []string{
	"category",
	"status",
	"tags",
	"capabilities",
	"requires",
	"skills",
	"tools",
	"mcpServers",
	"prompts",
	"workflows",
	"blueprints",
	"contextKeys",
	"outputKey",
	"type",
	"transport",
	"authMode",
	"language",
	"framework",
	"riskLevel",
	"inputSchema",
	"caseCount",
	"datasetRef",
	"scorers",
	"thresholds",
}

var referenceFields = map[string]bool{
	"tags":         true,
	"capabilities": true,
	"requires":     true,
	"skills":       true,
	"tools":        true,
	"mcpServers":   true,
	"prompts":      true,
	"workflows":    true,
	"blueprints":   true,
	"contextKeys":  true,
	"scorers":      true,
}

// Stub is the bounded discovery result returned to agents and gateways. It
// intentionally excludes executable bodies and credentials; FetchPath points
// at the exact object for a separately authorized progressive fetch.
type Stub struct {
	Kind        v1alpha1.Kind     `json:"kind"`
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Tag         string            `json:"tag,omitempty"`
	ARN         string            `json:"arn,omitempty"`
	Digest      string            `json:"digest,omitempty"`
	Ref         string            `json:"ref,omitempty"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Visibility  string            `json:"visibility,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Attributes  map[string]any    `json:"attributes,omitempty"`
	FetchPath   string            `json:"fetchPath"`
}

// BuildStub projects one authorized registry object into safe discovery
// metadata. Authorization must happen before this function is called.
func BuildStub(obj v1alpha1.Object) Stub {
	annotations := safeAnnotations(obj.Metadata.Annotations)
	description := safeText(stringValue(obj.Spec["description"]))
	if summary := annotations["discovery.agentic.dev/summary"]; summary != "" && summary != "***" {
		description = summary
	}
	return Stub{
		Kind:        obj.Kind,
		Name:        obj.Metadata.Name,
		Namespace:   obj.Metadata.Namespace,
		Tag:         obj.Metadata.Tag,
		ARN:         obj.Metadata.ARN,
		Digest:      obj.Metadata.Digest,
		Ref:         obj.Metadata.Ref,
		Title:       safeText(firstString(obj.Spec, "displayName", "title")),
		Description: description,
		Visibility:  string(obj.Metadata.Visibility),
		Labels:      safeStringMap(obj.Metadata.Labels, false),
		Annotations: annotations,
		Attributes:  safeAttributes(obj.Spec),
		FetchPath:   fetchPath(obj),
	}
}

// Text returns the only artifact text allowed into an embedding. It is built
// from the same stub projection, with ownership/system metadata excluded from
// the feature set even though selected values remain in the stub for policy
// enforcement by downstream gateways.
func Text(obj v1alpha1.Object) string {
	stub := BuildStub(obj)
	parts := []string{
		string(stub.Kind),
		stub.Name,
		stub.Title,
		stub.Description,
		safeText(stringValue(obj.Spec["description"])),
	}
	appendMap := func(values map[string]string) {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := values[key]
			if value == "***" || sensitiveKey(key) || strings.HasPrefix(key, "registry.agentic.dev/") || strings.HasSuffix(key, "/owner-id") {
				continue
			}
			parts = append(parts, key, value)
		}
	}
	appendMap(stub.Labels)
	appendMap(stub.Annotations)
	if len(stub.Attributes) > 0 {
		if encoded, err := json.Marshal(stub.Attributes); err == nil {
			parts = append(parts, string(encoded))
		}
	}
	return truncate(strings.Join(nonEmpty(parts), "\n"), maxSearchText)
}

// ParseKinds accepts canonical Kind names, REST plurals, and the underscore /
// hyphen aliases used by agent gateways.
func ParseKinds(values []string) (map[v1alpha1.Kind]bool, error) {
	if len(values) == 0 {
		return nil, nil
	}
	aliases := map[string]v1alpha1.Kind{
		"mcpserver":  v1alpha1.KindMCPServer,
		"mcpservers": v1alpha1.KindMCPServer,
		"dataset":    v1alpha1.KindDataset,
		"datasets":   v1alpha1.KindDataset,
		"evalsuite":  v1alpha1.KindEvalSuite,
		"evalsuites": v1alpha1.KindEvalSuite,
	}
	for _, kind := range v1alpha1.AllKinds {
		aliases[normalizeKind(string(kind))] = kind
		aliases[normalizeKind(v1alpha1.Plural(kind))] = kind
	}
	result := map[v1alpha1.Kind]bool{}
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kind, ok := aliases[normalizeKind(part)]
			if !ok {
				return nil, fmt.Errorf("unknown capability kind %q", part)
			}
			result[kind] = true
		}
	}
	return result, nil
}

// Allows reports whether kind passes a parsed kind filter. A nil/empty filter
// means all kinds.
func Allows(kinds map[v1alpha1.Kind]bool, kind v1alpha1.Kind) bool {
	return len(kinds) == 0 || kinds[kind]
}

func safeAttributes(spec map[string]any) map[string]any {
	out := map[string]any{}
	for _, field := range attributeFields {
		value, ok := spec[field]
		if !ok {
			continue
		}
		var safe any
		switch {
		case field == "inputSchema":
			safe = safeInputSchema(value)
		case referenceFields[field]:
			safe = safeReferences(value)
		case field == "thresholds":
			safe = safeNumbers(value)
		default:
			safe = safeScalar(value)
		}
		if !emptyValue(safe) {
			out[field] = safe
		}
	}
	return out
}

func safeInputSchema(value any) map[string]any {
	schema, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, field := range []string{"type", "title", "description"} {
		if text := safeText(stringValue(schema[field])); text != "" {
			out[field] = text
		}
	}
	if required := stringSlice(schema["required"]); len(required) > 0 {
		out["required"] = required
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return out
	}
	safeProperties := map[string]any{}
	for _, name := range sortedKeys(properties, maxMapEntries) {
		raw := properties[name]
		descriptor, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		safeDescriptor := map[string]any{}
		for _, field := range []string{"type", "title", "description"} {
			if text := safeText(stringValue(descriptor[field])); text != "" {
				safeDescriptor[field] = text
			}
		}
		safeProperties[name] = safeDescriptor
	}
	if len(safeProperties) > 0 {
		out["properties"] = safeProperties
	}
	return out
}

func safeReferences(value any) []string {
	values, ok := value.([]any)
	if !ok {
		if text := safeReference(value); text != "" {
			return []string{text}
		}
		return nil
	}
	out := make([]string, 0, len(values))
	for _, item := range values[:min(len(values), maxReferences)] {
		if text := safeReference(item); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func safeReference(value any) string {
	switch typed := value.(type) {
	case string:
		return safeText(typed)
	case map[string]any:
		return safeText(firstString(typed, "name", "ref", "id", "arn"))
	default:
		return ""
	}
}

func safeNumbers(value any) map[string]any {
	values, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, key := range sortedKeys(values, maxMapEntries) {
		item := values[key]
		switch item.(type) {
		case int, int32, int64, float32, float64:
			out[key] = item
		}
	}
	return out
}

func safeScalar(value any) any {
	switch typed := value.(type) {
	case string:
		return safeText(typed)
	case bool, int, int32, int64, float32, float64:
		return typed
	default:
		return nil
	}
}

func safeAnnotations(values map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range sortedStringKeys(values, maxMapEntries) {
		value := values[key]
		if !hasPrefix(key, annotationPrefixes) {
			continue
		}
		if sensitiveKey(key) {
			out[key] = "***"
			continue
		}
		out[key] = safeText(value)
	}
	return out
}

func safeStringMap(values map[string]string, annotations bool) map[string]string {
	if annotations {
		return safeAnnotations(values)
	}
	out := map[string]string{}
	for _, key := range sortedStringKeys(values, maxMapEntries) {
		value := values[key]
		if sensitiveKey(key) {
			out[key] = "***"
			continue
		}
		out[key] = safeText(value)
	}
	return out
}

func safeText(value string) string {
	value = bearerValue.ReplaceAllString(value, "Bearer ***")
	return truncate(secretAssignment.ReplaceAllString(value, "${1}=***"), maxValueRunes)
}

func sensitiveKey(key string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(key), func(r rune) bool {
		return r == '/' || r == '.' || r == '_' || r == '-' || r == ':'
	}) {
		switch token {
		case "secret", "password", "passwd", "token", "key", "apikey", "authorization", "credential", "credentials", "secretref":
			return true
		}
	}
	return false
}

func fetchPath(obj v1alpha1.Object) string {
	path := "/v0/" + v1alpha1.Plural(obj.Kind) + "/" + url.PathEscape(obj.Metadata.Name)
	query := url.Values{}
	if obj.Metadata.Namespace != "" {
		query.Set("namespace", obj.Metadata.Namespace)
	}
	if obj.Metadata.Tag != "" && obj.Metadata.Tag != v1alpha1.DefaultTag {
		path += "/" + url.PathEscape(obj.Metadata.Tag)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, min(len(values), maxReferences))
	for _, item := range values[:min(len(values), maxReferences)] {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func hasPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func normalizeKind(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToLower(value))
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func emptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case []string:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func sortedKeys[V any](values map[string]V, limit int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys[:min(len(keys), limit)]
}

func sortedStringKeys(values map[string]string, limit int) []string {
	return sortedKeys(values, limit)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
