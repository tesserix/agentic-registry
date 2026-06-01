// Package a2a renders an Agentic Registry `Agent` object into an A2A
// (Agent2Agent protocol) Agent Card — the JSON capability descriptor a
// runtime consumer fetches to learn WHAT an agent can do and WHERE/HOW to call
// it (https://a2a-protocol.org). The registry is a catalog/control-plane: it
// renders and serves the card, but never sits on the A2A request path. A
// consumer does: discover agent -> fetch card -> talk A2A directly to card.url.
//
// Design goals:
//   - Flexible & forward-compatible. The card is built as a generic map and any
//     unknown fields under spec.a2a pass straight through, so a newer A2A
//     version's fields work without a code change here.
//   - Never errors on missing optionals. A sparse Agent still yields a valid
//     (if minimal) card; a dangling skill reference is skipped, not fatal.
//   - Single source of truth for skills. spec.skills entries that name a
//     registry Skill resolve to that real Skill object (reuse + provenance);
//     inline skill objects are also accepted for one-offs.
package a2a

import (
	"maps"
	"sort"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// ProtocolVersion is the A2A spec version this renderer targets by default.
// An Agent may override it via spec.a2a.protocolVersion.
const ProtocolVersion = "0.3.0"

// ProvenanceExtensionURI identifies the registry-provenance AgentExtension
// carried inside capabilities.extensions — it lets a consumer verify the card
// came from this registry and pin the exact artifact (arn/digest/ref).
const ProvenanceExtensionURI = "https://registry.agentic.dev/ext/provenance"

// SkillResolver resolves a registry Skill object by name within the agent's
// namespace. ok=false (not found / not readable) makes the renderer skip that
// reference rather than fail — a dangling ref never breaks the card.
type SkillResolver func(name string) (v1alpha1.Object, bool)

// Options carries render-time context the agent spec cannot supply itself.
type Options struct {
	// RegistryURL is this registry's public base URL, recorded in the
	// provenance extension so a consumer can trace the card to its source.
	RegistryURL string
}

// Card renders an A2A Agent Card for the given Agent object. resolve may be nil
// (inline-only skills). It returns an error only when obj is not an Agent.
func Card(obj v1alpha1.Object, resolve SkillResolver, opts Options) (map[string]any, error) {
	if obj.Kind != v1alpha1.KindAgent {
		return nil, &NotAnAgentError{Kind: obj.Kind}
	}

	spec := orMap(obj.Spec)
	a2a := orMap(spec["a2a"]) // optional dedicated A2A block

	// Start from any pass-through fields the publisher put under spec.a2a, then
	// overlay the canonical/derived fields. Copying first keeps forward-compat:
	// a field this code doesn't know about still reaches the consumer.
	card := map[string]any{}
	for k, v := range a2a {
		switch k {
		// These are derived below from real registry data — never trust the
		// publisher's copy (prevents a spoofed skills list / version / name).
		case "skills", "version", "protocolVersion":
			continue
		default:
			card[k] = v
		}
	}

	card["protocolVersion"] = firstString(a2a["protocolVersion"], ProtocolVersion)
	card["name"] = firstString(a2a["name"], spec["title"], obj.Metadata.Name)
	card["description"] = firstString(a2a["description"], spec["description"])
	// A2A `version` is the agent artifact's version — the resolved tag.
	card["version"] = firstString(obj.Metadata.Tag, v1alpha1.DefaultTag)

	if _, ok := card["preferredTransport"]; !ok {
		card["preferredTransport"] = "JSONRPC"
	}
	if _, ok := card["defaultInputModes"]; !ok {
		card["defaultInputModes"] = []any{"application/json", "text/plain"}
	}
	if _, ok := card["defaultOutputModes"]; !ok {
		card["defaultOutputModes"] = []any{"application/json", "text/plain"}
	}

	// capabilities: keep the publisher's flags, then inject the registry
	// provenance AgentExtension so the card is traceable + verifiable.
	caps := orMap(card["capabilities"])
	caps["extensions"] = appendProvenance(caps["extensions"], obj, opts.RegistryURL)
	card["capabilities"] = caps

	card["skills"] = buildSkills(spec["skills"], resolve)

	return card, nil
}

// buildSkills resolves spec.skills into A2A skill objects. Each entry is either
// a string (a registry Skill name to resolve) or a map (an inline A2A skill).
func buildSkills(raw any, resolve SkillResolver) []any {
	list := orSlice(raw)
	out := make([]any, 0, len(list))
	seen := map[string]bool{}
	for _, entry := range list {
		switch v := entry.(type) {
		case string:
			if v == "" || resolve == nil {
				continue
			}
			skillObj, ok := resolve(v)
			if !ok {
				continue // dangling ref — skip, don't fail
			}
			sk := skillToA2A(skillObj)
			if id, _ := sk["id"].(string); id != "" && !seen[id] {
				seen[id] = true
				out = append(out, sk)
			}
		case map[string]any:
			sk := normalizeInlineSkill(v)
			if id, _ := sk["id"].(string); id != "" && !seen[id] {
				seen[id] = true
				out = append(out, sk)
			}
		}
	}
	return out
}

// skillToA2A maps a registry Skill object onto an A2A skill. inputModes /
// outputModes are omitted when unset so the consumer inherits the card-level
// defaults (per the A2A spec).
func skillToA2A(skill v1alpha1.Object) map[string]any {
	sp := orMap(skill.Spec)
	sk := map[string]any{
		"id":          skill.Metadata.Name,
		"name":        firstString(sp["title"], skill.Metadata.Name),
		"description": firstString(sp["description"]),
		"tags":        skillTags(skill),
	}
	if ex := orSlice(sp["examples"]); len(ex) > 0 {
		sk["examples"] = ex
	}
	if im := orSlice(sp["inputModes"]); len(im) > 0 {
		sk["inputModes"] = im
	}
	if om := orSlice(sp["outputModes"]); len(om) > 0 {
		sk["outputModes"] = om
	}
	return sk
}

// normalizeInlineSkill ensures an inline skill carries an id (defaulting to its
// name) and a tags slice, while passing through every other field as-is.
func normalizeInlineSkill(in map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, in)
	id := firstString(out["id"], out["name"])
	out["id"] = id
	if _, ok := out["name"]; !ok {
		out["name"] = id
	}
	if _, ok := out["tags"]; !ok {
		out["tags"] = []any{}
	}
	return out
}

// skillTags prefers an explicit spec.tags list; otherwise it derives stable,
// sorted tag values from the Skill's labels so the card is still searchable.
func skillTags(skill v1alpha1.Object) []any {
	if tags := orSlice(orMap(skill.Spec)["tags"]); len(tags) > 0 {
		return tags
	}
	vals := make([]string, 0, len(skill.Metadata.Labels))
	for k, v := range skill.Metadata.Labels {
		// Skip the registry's own system labels — they aren't capability tags.
		if len(k) > len("registry.agentic.dev/") && k[:len("registry.agentic.dev/")] == "registry.agentic.dev/" {
			continue
		}
		if v != "" {
			vals = append(vals, v)
		}
	}
	sort.Strings(vals)
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

// appendProvenance adds (or extends) the registry-provenance AgentExtension.
func appendProvenance(existing any, obj v1alpha1.Object, registryURL string) []any {
	params := map[string]any{
		"arn":       obj.ARN(),
		"digest":    obj.Digest(),
		"ref":       obj.Ref(),
		"namespace": obj.Metadata.Namespace,
	}
	if registryURL != "" {
		params["registry"] = registryURL
	}
	if obj.Metadata.Signature != "" {
		params["signature"] = obj.Metadata.Signature
		params["signedBy"] = obj.Metadata.SignedBy
	}
	ext := map[string]any{
		"uri":         ProvenanceExtensionURI,
		"description": "Agentic Registry provenance: pins the exact artifact this card was rendered from.",
		"required":    false,
		"params":      params,
	}
	out := orSlice(existing)
	return append(out, ext)
}

// --- small, defensive helpers over free-form maps ---

func orMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func orSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// firstString returns the first non-empty stringified candidate.
func firstString(cands ...any) string {
	for _, c := range cands {
		if s, ok := c.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
