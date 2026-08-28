package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/internal/config"
)

func TestPortableADKBundlePublishesSignsAndResolvesAnExactDependencyLock(t *testing.T) {
	srv, _ := testServerWith(t, config.Config{
		StoreBackend: "memory",
		AuthMode:     "anonymous",
		SigningDev:   true,
	})
	bundle := `apiVersion: registry.agentic.dev/v1alpha1
kind: Tool
metadata:
  name: ticket-search
  namespace: acme-ai
  tag: 2.1.0
spec:
  description: Find a support ticket
---
apiVersion: registry.agentic.dev/v1alpha1
kind: Agent
metadata:
  name: support-agent
  namespace: acme-ai
  tag: 1.2.0
  visibility: private
  labels:
    framework: langgraph
spec:
  definitionVersion: v1
  framework: langgraph
  runtime:
    type: container
    protocol: a2a
    image: ghcr.io/acme/support-agent@sha256:` + strings.Repeat("a", 64) + `
    port: 8080
    path: /a2a/v1
    healthPath: /readyz
  tools:
    - ref: ticket-search
      version: 2.1.0
`

	apply := httptest.NewRequest(http.MethodPost, "/v0/apply", bytes.NewBufferString(bundle))
	apply.Header.Set("Content-Type", "application/yaml")
	apply.Header.Set("Idempotency-Key", "portable-contract-1")
	created := httptest.NewRecorder()
	srv.ServeHTTP(created, apply)
	if created.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", created.Code, created.Body.String())
	}

	resolvedRequest := httptest.NewRequest(
		http.MethodGet,
		"/v0/agents/support-agent/resolved?namespace=acme-ai&tag=1.2.0",
		nil,
	)
	resolvedResponse := httptest.NewRecorder()
	srv.ServeHTTP(resolvedResponse, resolvedRequest)
	if resolvedResponse.Code != http.StatusOK {
		t.Fatalf("resolved status=%d body=%s", resolvedResponse.Code, resolvedResponse.Body.String())
	}
	var resolved ResolvedAgent
	if err := json.Unmarshal(resolvedResponse.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode resolved Agent: %v", err)
	}
	if len(resolved.Unresolved) != 0 || len(resolved.Resolved["tools"]) != 1 {
		t.Fatalf("dependency lock was not exact: %#v", resolved)
	}
	tool := resolved.Resolved["tools"][0]
	if tool.Metadata.Name != "ticket-search" || tool.Metadata.Tag != "2.1.0" || tool.Metadata.Digest == "" {
		t.Fatalf("resolved moving or unsigned dependency: %#v", tool.Metadata)
	}
	if resolved.Agent.Metadata.Digest == "" || resolved.Agent.Metadata.Signature == "" || resolved.Agent.Metadata.SignedBy == "" {
		t.Fatalf("Agent has no Registry identity attestation: %#v", resolved.Agent.Metadata)
	}
	runtime, _ := resolved.Agent.Spec["runtime"].(map[string]any)
	if image, _ := runtime["image"].(string); !strings.Contains(image, "@sha256:") {
		t.Fatalf("resolved runtime is mutable: %#v", runtime)
	}
}
