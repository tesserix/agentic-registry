// Package kagent renders a resolved registry Agent into a kagent.dev Agent CR
// plus one ToolServer per MCP dependency, so a long-lived agent can be
// reconciled into a controller-managed Deployment by the kagent operator.
//
// Pure function over pkg/api/v1alpha1 types. The MCP servers passed in are the
// resolved `mcpServers` slice from the /resolved endpoint; their tools are
// reached through agentgateway at {gateway}/mcp/<name>, matching the routes the
// agentgateway adapter renders.
//
// GVKs verified against the installed CRDs (kagent v0.9.x): the Agent CRD's
// STORAGE version is kagent.dev/v1alpha2, whose spec nests the model + prompt
// under spec.declarative and selects the kind via spec.type. v1alpha1 is still
// served, but applying a v1alpha1 Agent converts to v1alpha2 for storage and
// drops the flat fields, leaving spec.declarative nil → the controller panics.
// So the Agent is emitted as v1alpha2. ToolServer stays v1alpha1 (the tool path
// still needs porting to v1alpha2 RemoteMCPServer + toolNames — TODO).
package kagent

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tesserix/agentic-registry/adapters"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const kagentAPIVersion = "kagent.dev/v1alpha1"

// The Agent CRD's storage version. The declarative spec shape only exists here;
// emitting v1alpha1 would convert-and-drop the model/prompt on storage.
const kagentAgentAPIVersion = "kagent.dev/v1alpha2"

// Options controls namespacing, the ModelConfig the Agent references, and how
// MCP tools are reached.
type Options struct {
	// Namespace the Agent + ToolServer CRs are created in.
	Namespace string
	// ModelConfigRef is the name of the kagent ModelConfig CR the Agent uses.
	// The registry can't know cluster ModelConfig names, so it's injected.
	ModelConfigRef string
	// GatewayURL is the agentgateway base URL MCP tools are reached through,
	// e.g. "http://agentgateway.agentgateway-system.svc.cluster.local:8080".
	// Each ToolServer points at {GatewayURL}/mcp/<name>. When empty, the MCP
	// server's own remote URL (if any) is used instead.
	GatewayURL string
	// NameSuffix, when set, is appended to the Agent CR name as "<name>-<suffix>"
	// so one registry Agent can render multiple variants (e.g. one per provider/
	// model ModelConfig) without colliding. Empty = the bare agent name.
	NameSuffix string
	// SystemPrompt, when set, becomes the kagent systemMessage. The export
	// resolves the agent's promptRef (a Prompt artifact) into this, so agents
	// that keep their prompt in a referenced artifact — almost all of them —
	// still render a valid CR without duplicating the prompt inline.
	SystemPrompt string
	// WorkerPoolRef, when set, renders a **SandboxAgent** (Agent Substrate)
	// instead of a classic Agent: same spec.declarative, plus spec.substrate.
	// workerPoolRef pinning it to a Substrate WorkerPool so it runs as a
	// gVisor-isolated Actor (low overhead, fast cold start) rather than its own
	// standing Deployment. Empty = classic Agent (one Deployment per agent).
	WorkerPoolRef string
}

func (o Options) withDefaults() Options {
	if o.Namespace == "" {
		o.Namespace = "kagent"
	}
	if o.ModelConfigRef == "" {
		o.ModelConfigRef = "default-model-config"
	}
	return o
}

// Output is the structured render (Agent + its ToolServers).
type Output struct {
	Agent       map[string]interface{}
	ToolServers []map[string]interface{}
}

// Build renders the Agent CR + ToolServer CRs as a single multi-document YAML
// stream (Agent first, then ToolServers sorted by name).
func Build(agent v1alpha1.Object, mcpServers []v1alpha1.Object, opts Options) ([]byte, error) {
	out, err := BuildOutput(agent, mcpServers, opts)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out.Agent); err != nil {
		return nil, err
	}
	for _, m := range out.ToolServers {
		if err := enc.Encode(m); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BuildOutput is the structured form of Build.
func BuildOutput(agent v1alpha1.Object, mcpServers []v1alpha1.Object, opts Options) (Output, error) {
	opts = opts.withDefaults()
	name := adapters.SanitizeName(agentName(agent))
	if opts.NameSuffix != "" {
		name = adapters.SanitizeName(name + "-" + opts.NameSuffix)
	}
	if name == "unnamed" {
		return Output{}, fmt.Errorf("agent has no name")
	}

	// Render a ToolServer per MCP dependency and collect tool refs.
	seen := map[string]bool{}
	var toolDocs []map[string]interface{}
	var toolRefs []interface{}
	for _, srv := range mcpServers {
		raw := mcpName(srv)
		if raw == "" {
			continue
		}
		san := adapters.SanitizeName(raw)
		if seen[san] {
			continue
		}
		seen[san] = true

		toolDocs = append(toolDocs, map[string]interface{}{
			"apiVersion": kagentAPIVersion,
			"kind":       "ToolServer",
			"metadata": map[string]interface{}{
				"name":      san,
				"namespace": opts.Namespace,
				"labels":    managedLabels(name),
			},
			"spec": map[string]interface{}{
				"description": stringField(srv, "description"),
				"config": map[string]interface{}{
					"type": "StreamableHttp",
					"streamableHttp": map[string]interface{}{
						"url": mcpURL(srv, san, opts),
					},
				},
			},
		})
		toolRefs = append(toolRefs, map[string]interface{}{
			"type": "McpServer",
			"mcpServer": map[string]interface{}{
				"toolServer": san,
			},
		})
	}
	sort.Slice(toolDocs, func(i, j int) bool {
		return toolDocs[i]["metadata"].(map[string]interface{})["name"].(string) <
			toolDocs[j]["metadata"].(map[string]interface{})["name"].(string)
	})

	// kagent v0.9.x nests the model + prompt + tools under spec.declarative
	// and selects the agent kind via spec.type. (Pre-0.9 used a flat spec;
	// emitting that against a 0.9 CRD prunes modelConfig/systemMessage and
	// leaves spec.declarative nil, which makes the controller panic.)
	declarative := map[string]interface{}{
		"modelConfig":   opts.ModelConfigRef,
		"systemMessage": systemMessage(agent, opts.SystemPrompt),
	}
	if len(toolRefs) > 0 {
		declarative["tools"] = toolRefs
	}
	agentSpec := map[string]interface{}{
		"type":        "Declarative",
		"description": stringField(agent, "description"),
		"declarative": declarative,
	}

	// Default: a classic Agent (one Deployment per agent). When a WorkerPool is
	// set, render a SandboxAgent instead — identical declarative block, plus the
	// substrate.workerPoolRef that makes the controller run it as a gVisor Actor
	// in the shared Substrate WorkerPool (Agent Substrate). Both CRDs are
	// kagent.dev/v1alpha2 and share the declarative schema, so this is the same
	// renderer with one extra field.
	kind := "Agent"
	if opts.WorkerPoolRef != "" {
		kind = "SandboxAgent"
		agentSpec["substrate"] = map[string]interface{}{"workerPoolRef": opts.WorkerPoolRef}
	}

	agentDoc := map[string]interface{}{
		"apiVersion": kagentAgentAPIVersion,
		"kind":       kind,
		"metadata": map[string]interface{}{
			"name":        name,
			"namespace":   opts.Namespace,
			"labels":      managedLabels(name),
			"annotations": modelAnnotations(agent),
		},
		"spec": agentSpec,
	}

	return Output{Agent: agentDoc, ToolServers: toolDocs}, nil
}

func managedLabels(agent string) map[string]interface{} {
	return map[string]interface{}{
		"app.kubernetes.io/managed-by": "agentic-registry",
		"registry.agentic.dev/agent":   agent,
	}
}

func agentName(agent v1alpha1.Object) string {
	if agent.Metadata.Name != "" {
		return agent.Metadata.Name
	}
	return stringField(agent, "title")
}

func mcpName(srv v1alpha1.Object) string {
	if n, ok := srv.Spec["name"].(string); ok && n != "" {
		return n
	}
	return srv.Metadata.Name
}

// mcpURL routes tools through agentgateway when a GatewayURL is set, else falls
// back to the server's own remote URL.
func mcpURL(srv v1alpha1.Object, san string, opts Options) string {
	if opts.GatewayURL != "" {
		return strings.TrimRight(opts.GatewayURL, "/") + "/mcp/" + san
	}
	if remotes, ok := srv.Spec["remotes"].([]interface{}); ok {
		for _, r := range remotes {
			if m, ok := r.(map[string]interface{}); ok {
				if u, ok := m["url"].(string); ok && u != "" {
					return u
				}
			}
		}
	}
	return ""
}

// systemMessage prefers an explicit override (a promptRef the export resolved),
// then the agent's own systemPrompt, then its description. kagent requires a
// non-empty systemMessage, so a registry agent that keeps its prompt in a
// referenced Prompt artifact (most do) still renders a valid CR.
func systemMessage(agent v1alpha1.Object, override string) string {
	if override != "" {
		return override
	}
	if sp, ok := agent.Spec["systemPrompt"].(string); ok && sp != "" {
		return sp
	}
	return stringField(agent, "description")
}

// Issue is one cross-validation finding: whether a DevAI registry agent will
// render a kagent CR the controller ACCEPTS (error) or merely something to note
// (warning). Lets DevAI authoring validate an agent against the kagent contract
// before publish/deploy instead of discovering a rejection at reconcile time.
type Issue struct {
	Severity string `json:"severity"` // "error" | "warning"
	Field    string `json:"field"`
	Message  string `json:"message"`
}

// Validate renders the agent exactly as Build does and reports whether the
// result satisfies the kagent Agent contract. Pure (applies nothing). errors =
// the controller would reject it; warnings = accepted but worth flagging. This
// is the cross-validation surface for DevAI ⇄ kagent schema alignment.
func Validate(agent v1alpha1.Object, mcpServers []v1alpha1.Object, opts Options) []Issue {
	out, err := BuildOutput(agent, mcpServers, opts)
	if err != nil {
		return []Issue{{Severity: "error", Field: "metadata.name", Message: err.Error()}}
	}
	var issues []Issue
	spec, _ := out.Agent["spec"].(map[string]interface{})
	if t, _ := spec["type"].(string); t == "" {
		issues = append(issues, Issue{"error", "spec.type", "must be set (Declarative) — a flat/typeless spec makes the controller nil-panic"})
	}
	decl, _ := spec["declarative"].(map[string]interface{})
	if decl == nil {
		issues = append(issues, Issue{"error", "spec.declarative", "missing — model/prompt/tools must nest under spec.declarative for v1alpha2"})
		return issues
	}
	if mc, _ := decl["modelConfig"].(string); mc == "" {
		issues = append(issues, Issue{"error", "spec.declarative.modelConfig", "a ModelConfig reference is required"})
	}
	if sm, _ := decl["systemMessage"].(string); sm == "" {
		issues = append(issues, Issue{
			"error", "spec.declarative.systemMessage",
			"empty — the controller rejects an agent with no system message; set spec.systemPrompt inline or a resolvable spec.promptRef → Prompt.spec.systemPrompt",
		})
	}
	if _, hasTools := decl["tools"]; hasTools {
		issues = append(issues, Issue{
			"warning", "spec.declarative.tools",
			"tools render as v1alpha1 ToolServer refs; kagent 0.9 prefers RemoteMCPServer + toolNames (port pending)",
		})
	}
	// SandboxAgent (Agent Substrate) must pin a WorkerPool to schedule its Actor.
	if k, _ := out.Agent["kind"].(string); k == "SandboxAgent" {
		sub, _ := spec["substrate"].(map[string]interface{})
		if wp, _ := sub["workerPoolRef"].(string); wp == "" {
			issues = append(issues, Issue{"error", "spec.substrate.workerPoolRef", "a SandboxAgent must reference a Substrate WorkerPool"})
		}
	}
	return issues
}

func stringField(o v1alpha1.Object, field string) string {
	if v, ok := o.Spec[field].(string); ok {
		return v
	}
	return ""
}

// modelAnnotations records the registry's intended provider/model so a human
// (or a ModelConfig-sync step) can map it to the right kagent ModelConfig.
func modelAnnotations(agent v1alpha1.Object) map[string]interface{} {
	ann := map[string]interface{}{}
	if model, ok := agent.Spec["model"].(map[string]interface{}); ok {
		if p, ok := model["provider"].(string); ok && p != "" {
			ann["registry.agentic.dev/model-provider"] = p
		}
		if n, ok := model["name"].(string); ok && n != "" {
			ann["registry.agentic.dev/model-name"] = n
		}
	}
	if len(ann) == 0 {
		return nil
	}
	return ann
}
