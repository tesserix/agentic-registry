package kagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func sampleAgent() v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{Name: "code-reviewer"},
		Spec: map[string]interface{}{
			"description":  "Reviews PRs",
			"systemPrompt": "You are a strict code reviewer.",
			"model":        map[string]interface{}{"provider": "claude", "name": "claude-opus"},
		},
	}
}

func mcp(name, url string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: name},
		Spec: map[string]interface{}{
			"name":    name,
			"remotes": []interface{}{map[string]interface{}{"url": url}},
			"tools":   []interface{}{"create_pr", "get_pr"},
		},
	}
}

func TestBuildOutput_AgentAndMCP(t *testing.T) {
	out, err := BuildOutput(sampleAgent(), []v1alpha1.Object{mcp("github", "https://gh/mcp")}, Options{
		Namespace:      "kagent",
		ModelConfigRef: "claude-opus-config",
		GatewayURL:     "http://agentgateway.agentgateway-system.svc.cluster.local:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := out.Agent["spec"].(map[string]interface{})
	// kagent v0.9.x: type=Declarative and the model/prompt/tools live under
	// spec.declarative (not flat on spec).
	if spec["type"] != "Declarative" {
		t.Errorf("spec.type: got %v want Declarative", spec["type"])
	}
	dec := spec["declarative"].(map[string]interface{})
	if dec["systemMessage"] != "You are a strict code reviewer." {
		t.Errorf("declarative.systemMessage: got %v", dec["systemMessage"])
	}
	if dec["modelConfig"] != "claude-opus-config" {
		t.Errorf("declarative.modelConfig: got %v", dec["modelConfig"])
	}
	if len(out.RemoteMCPServers) != 1 {
		t.Fatalf("want 1 RemoteMCPServer, got %d", len(out.RemoteMCPServers))
	}
	remote := out.RemoteMCPServers[0]
	if remote["apiVersion"] != "kagent.dev/v1alpha2" || remote["kind"] != "RemoteMCPServer" {
		t.Fatalf("unexpected remote MCP GVK: %v %v", remote["apiVersion"], remote["kind"])
	}
	remoteSpec := remote["spec"].(map[string]interface{})
	want := "http://agentgateway.agentgateway-system.svc.cluster.local:8080/mcp/github"
	if remoteSpec["url"] != want || remoteSpec["protocol"] != "STREAMABLE_HTTP" {
		t.Errorf("remote MCP route: got %v want %v over STREAMABLE_HTTP", remoteSpec, want)
	}
	// The Agent's least-privilege tool ref points at the v1alpha2 remote server.
	tool := dec["tools"].([]interface{})[0].(map[string]interface{})
	mcpRef := tool["mcpServer"].(map[string]interface{})
	if mcpRef["apiGroup"] != "kagent.dev" || mcpRef["kind"] != "RemoteMCPServer" || mcpRef["name"] != "github" {
		t.Errorf("tool ref: got %v", mcpRef)
	}
	if got := mcpRef["toolNames"].([]interface{}); len(got) != 2 || got[0] != "create_pr" || got[1] != "get_pr" {
		t.Errorf("tool allowlist: got %v", got)
	}
	// model provider/name surface as annotations for ModelConfig mapping.
	ann := out.Agent["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
	if ann["registry.agentic.dev/model-provider"] != "claude" {
		t.Errorf("model-provider annotation: got %v", ann["registry.agentic.dev/model-provider"])
	}
}

func TestBuild_YAMLContainsBothKinds(t *testing.T) {
	out, err := Build(sampleAgent(), []v1alpha1.Object{mcp("github", "https://gh/mcp")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"apiVersion: kagent.dev/v1alpha2", // the Agent (declarative)
		"kind: Agent",
		"kind: RemoteMCPServer",
		"name: code-reviewer",
		"name: github",
		"protocol: STREAMABLE_HTTP",
		"toolNames:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

func TestBuild_NoGatewayFallsBackToRemoteURL(t *testing.T) {
	out, err := BuildOutput(sampleAgent(), []v1alpha1.Object{mcp("github", "https://gh/mcp")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	spec := out.RemoteMCPServers[0]["spec"].(map[string]interface{})
	if spec["url"] != "https://gh/mcp" {
		t.Errorf("without gateway, should use remote URL: got %v", spec["url"])
	}
}

func TestBuild_NoGatewayPrefersEndpointAndPreservesSSE(t *testing.T) {
	server := mcp("github", "https://legacy.example/mcp")
	server.Spec["endpoint"] = "https://mcp.example/sse"
	server.Spec["transport"] = "sse"

	out, err := BuildOutput(sampleAgent(), []v1alpha1.Object{server}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	spec := out.RemoteMCPServers[0]["spec"].(map[string]interface{})
	if spec["url"] != "https://mcp.example/sse" || spec["protocol"] != "SSE" {
		t.Fatalf("direct remote MCP transport: got %v", spec)
	}
}

func TestBuildOutput_ExplicitAgentToolsNarrowServerAllowlist(t *testing.T) {
	agent := sampleAgent()
	agent.Spec["tools"] = []interface{}{"get_pr"}
	out, err := BuildOutput(agent, []v1alpha1.Object{mcp("github", "https://gh/mcp")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	dec := out.Agent["spec"].(map[string]interface{})["declarative"].(map[string]interface{})
	ref := dec["tools"].([]interface{})[0].(map[string]interface{})["mcpServer"].(map[string]interface{})
	got := ref["toolNames"].([]interface{})
	if len(got) != 1 || got[0] != "get_pr" {
		t.Fatalf("agent tool selection must narrow the server allowlist, got %v", got)
	}
}

func TestBuildOutput_MCPServerWithoutDeclaredToolsFailsClosed(t *testing.T) {
	server := mcp("github", "https://gh/mcp")
	delete(server.Spec, "tools")
	_, err := BuildOutput(sampleAgent(), []v1alpha1.Object{server}, Options{})
	if err == nil || !strings.Contains(err.Error(), "declared tools") {
		t.Fatalf("expected missing tool allowlist error, got %v", err)
	}
}

func TestBuildOutput_MCPServerOverFiftyToolsFailsClosed(t *testing.T) {
	server := mcp("github", "https://gh/mcp")
	tools := make([]interface{}, 51)
	for i := range tools {
		tools[i] = fmt.Sprintf("tool-%02d", i)
	}
	server.Spec["tools"] = tools

	_, err := BuildOutput(sampleAgent(), []v1alpha1.Object{server}, Options{})
	if err == nil || !strings.Contains(err.Error(), "50-tool limit") {
		t.Fatalf("expected 50-tool limit error, got %v", err)
	}
}

func TestBuildOutput_NameSuffixVariant(t *testing.T) {
	out, err := BuildOutput(sampleAgent(), nil, Options{ModelConfigRef: "kagent-mc-openai", NameSuffix: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	name := out.Agent["metadata"].(map[string]interface{})["name"]
	if name != "code-reviewer-openai" {
		t.Errorf("variant name: got %v want code-reviewer-openai", name)
	}
	dec := out.Agent["spec"].(map[string]interface{})["declarative"].(map[string]interface{})
	if dec["modelConfig"] != "kagent-mc-openai" {
		t.Errorf("variant modelConfig: got %v", dec["modelConfig"])
	}
}

func TestBuildOutput_NoNameErrors(t *testing.T) {
	_, err := BuildOutput(v1alpha1.Object{Kind: v1alpha1.KindAgent}, nil, Options{})
	if err == nil {
		t.Error("expected error for nameless agent")
	}
}
