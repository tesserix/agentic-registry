package kagent

import (
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
	if spec["systemMessage"] != "You are a strict code reviewer." {
		t.Errorf("systemMessage: got %v", spec["systemMessage"])
	}
	if spec["modelConfig"] != "claude-opus-config" {
		t.Errorf("modelConfig: got %v", spec["modelConfig"])
	}
	if len(out.ToolServers) != 1 {
		t.Fatalf("want 1 ToolServer, got %d", len(out.ToolServers))
	}
	cfg := out.ToolServers[0]["spec"].(map[string]interface{})["config"].(map[string]interface{})
	shttp := cfg["streamableHttp"].(map[string]interface{})
	want := "http://agentgateway.agentgateway-system.svc.cluster.local:8080/mcp/github"
	if shttp["url"] != want {
		t.Errorf("toolserver url through gateway: got %v want %v", shttp["url"], want)
	}
	// the Agent's tool ref points at the ToolServer by name.
	tool := spec["tools"].([]interface{})[0].(map[string]interface{})
	if tool["mcpServer"].(map[string]interface{})["toolServer"] != "github" {
		t.Errorf("tool ref: got %v", tool["mcpServer"])
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
		"apiVersion: kagent.dev/v1alpha1",
		"kind: Agent",
		"kind: ToolServer",
		"name: code-reviewer",
		"name: github",
		"type: StreamableHttp",
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
	cfg := out.ToolServers[0]["spec"].(map[string]interface{})["config"].(map[string]interface{})
	shttp := cfg["streamableHttp"].(map[string]interface{})
	if shttp["url"] != "https://gh/mcp" {
		t.Errorf("without gateway, should use remote URL: got %v", shttp["url"])
	}
}

func TestBuildOutput_NoNameErrors(t *testing.T) {
	_, err := BuildOutput(v1alpha1.Object{Kind: v1alpha1.KindAgent}, nil, Options{})
	if err == nil {
		t.Error("expected error for nameless agent")
	}
}
