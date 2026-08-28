package embed

import (
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func TestSearchTextIncludesSafeCapabilityMetadata(t *testing.T) {
	obj := v1alpha1.Object{
		Kind: v1alpha1.KindTool,
		Metadata: v1alpha1.ObjectMeta{
			Name:   "security-scan-sast",
			Labels: map[string]string{"devai.io/domain": "code-quality"},
			Annotations: map[string]string{
				"discovery.agentic.dev/when-to-use": "review a repository for application vulnerabilities",
				"mcp.devai.io/wire-name":            "security_scan_sast",
			},
		},
		Spec: map[string]any{
			"title":       "SAST scanner",
			"description": "Finds insecure application code",
			"tags":        []any{"security", "quality"},
			"mcpServers":  []any{"analyst-mcp"},
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repository": map[string]any{
						"type":        "string",
						"description": "Repository URL to scan",
					},
				},
			},
		},
	}

	got := SearchText(obj)
	for _, want := range []string{
		"security-scan-sast",
		"code-quality",
		"review a repository for application vulnerabilities",
		"security_scan_sast",
		"security",
		"analyst-mcp",
		"repository",
		"Repository URL to scan",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SearchText() missing %q: %s", want, got)
		}
	}
}

func TestSearchTextExcludesRuntimeConfigurationAndSecrets(t *testing.T) {
	obj := v1alpha1.Object{
		Kind: v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{
			Name: "private-runtime",
			Labels: map[string]string{
				"devai.io/domain":  "safe-domain",
				"devai.io/api-key": "label-secret",
			},
			Annotations: map[string]string{
				"discovery.agentic.dev/summary": "Runs safe analysis token=annotation-secret",
				"devai.io/api-key":              "annotation-key-secret",
				"untrusted.example/note":        "untrusted-annotation-secret",
			},
		},
		Spec: map[string]any{
			"description":  "Analysis server api_key=description-secret",
			"systemPrompt": "PRIVATE SYSTEM INSTRUCTION",
			"content":      "PRIVATE PROMPT CONTENT",
			"env":          map[string]any{"API_TOKEN": "environment-secret"},
			"headers":      map[string]any{"Authorization": "Bearer header-secret"},
			"credentials":  map[string]any{"secretRef": "credential-reference"},
			"url":          "https://private-runtime.internal.example",
			"endpoint":     "https://private-endpoint.internal.example",
			"command":      "/opt/private-launcher",
			"args":         []any{"--token", "argument-secret"},
		},
	}

	got := SearchText(obj)
	for _, secret := range []string{
		"label-secret",
		"annotation-secret",
		"annotation-key-secret",
		"untrusted-annotation-secret",
		"description-secret",
		"PRIVATE SYSTEM INSTRUCTION",
		"PRIVATE PROMPT CONTENT",
		"environment-secret",
		"header-secret",
		"credential-reference",
		"private-runtime.internal.example",
		"private-endpoint.internal.example",
		"/opt/private-launcher",
		"argument-secret",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("SearchText() leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "safe-domain") || !strings.Contains(got, "Analysis server") {
		t.Fatalf("SearchText() dropped safe discovery fields: %s", got)
	}
}
