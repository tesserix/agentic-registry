package discovery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func TestStubIsSafeAndPointsAtExactArtifact(t *testing.T) {
	obj := v1alpha1.Object{
		Kind: v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{
			Name:       "reviewer",
			Namespace:  "tenant-a",
			Tag:        "v2",
			ARN:        "arn:agentic:tenant-a:Agent:reviewer",
			Digest:     "sha256:abc",
			Visibility: v1alpha1.VisibilityPrivate,
			Labels: map[string]string{
				"devai.tesserix.app/owner-id": "owner-hash",
				"devai.io/api-key":            "label-secret",
			},
			Annotations: map[string]string{
				"discovery.agentic.dev/summary": "Reviews code token=annotation-secret",
				"mcp.devai.io/wire-name":        "review_code",
				"untrusted.example/private":     "untrusted-secret",
			},
		},
		Spec: map[string]any{
			"title":        "Code reviewer",
			"description":  "Reviews pull requests api_key=description-secret",
			"skills":       []any{"security-review"},
			"tools":        []any{map[string]any{"name": "sast-scan", "token": "nested-secret"}},
			"systemPrompt": "PRIVATE SYSTEM PROMPT",
		},
	}

	got := BuildStub(obj)
	if got.FetchPath != "/v0/agents/reviewer/v2?namespace=tenant-a" {
		t.Errorf("FetchPath = %q", got.FetchPath)
	}
	if got.Labels["devai.tesserix.app/owner-id"] != "owner-hash" {
		t.Errorf("owner label needed for downstream authorization was dropped: %#v", got.Labels)
	}
	if got.Labels["devai.io/api-key"] != "***" {
		t.Errorf("sensitive label was not redacted: %#v", got.Labels)
	}
	if got.Annotations["mcp.devai.io/wire-name"] != "review_code" {
		t.Errorf("safe routing annotation was dropped: %#v", got.Annotations)
	}
	if _, ok := got.Annotations["untrusted.example/private"]; ok {
		t.Errorf("untrusted annotation was returned: %#v", got.Annotations)
	}
	if got.Attributes["skills"] == nil || got.Attributes["tools"] == nil {
		t.Errorf("composition relationships were dropped: %#v", got.Attributes)
	}

	serialized := strings.Join([]string{got.Title, got.Description, stringMapText(got.Labels), stringMapText(got.Annotations)}, " ")
	for _, secret := range []string{"label-secret", "annotation-secret", "untrusted-secret", "description-secret", "nested-secret", "PRIVATE SYSTEM PROMPT"} {
		if strings.Contains(serialized, secret) {
			t.Errorf("stub leaked %q: %s", secret, serialized)
		}
	}
}

func TestParseKindsAcceptsRegistryAndGatewayNames(t *testing.T) {
	got, err := ParseKinds([]string{"tools", "Skill", "mcp_servers", "eval-suites"})
	if err != nil {
		t.Fatalf("ParseKinds: %v", err)
	}
	for _, want := range []v1alpha1.Kind{
		v1alpha1.KindTool,
		v1alpha1.KindSkill,
		v1alpha1.KindMCPServer,
		v1alpha1.KindEvalSuite,
	} {
		if !got[want] {
			t.Errorf("ParseKinds() omitted %q: %#v", want, got)
		}
	}
	if _, err := ParseKinds([]string{"secrets"}); err == nil {
		t.Fatal("ParseKinds() accepted an unknown kind")
	}
}

func TestProjectionBoundsPublisherControlledMetadata(t *testing.T) {
	properties := map[string]any{}
	annotations := map[string]string{}
	for i := 0; i < 500; i++ {
		properties[fmt.Sprintf("property-%03d", i)] = map[string]any{
			"type":        "string",
			"description": strings.Repeat("description ", 500),
		}
		annotations[fmt.Sprintf("discovery.agentic.dev/note-%03d", i)] = strings.Repeat("annotation ", 500)
	}
	obj := v1alpha1.Object{
		Kind: v1alpha1.KindTool,
		Metadata: v1alpha1.ObjectMeta{
			Name:        "bounded",
			Annotations: annotations,
		},
		Spec: map[string]any{
			"inputSchema": map[string]any{"properties": properties},
		},
	}

	stub := BuildStub(obj)
	schema, _ := stub.Attributes["inputSchema"].(map[string]any)
	safeProperties, _ := schema["properties"].(map[string]any)
	if len(stub.Annotations) > 50 || len(safeProperties) > 50 {
		t.Fatalf("projection is unbounded: annotations=%d properties=%d", len(stub.Annotations), len(safeProperties))
	}
	if got := len(Text(obj)); got > 12_000 {
		t.Fatalf("search text length = %d, want <= 12000", got)
	}
}

func stringMapText(values map[string]string) string {
	var out strings.Builder
	for key, value := range values {
		out.WriteString(key)
		out.WriteString(value)
	}
	return out.String()
}
