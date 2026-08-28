package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/internal/signing"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// seedAgent puts a skill + an agent that links it into a fresh memory store.
func seedAgent(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := st.Apply(ctx, v1alpha1.Object{
		Kind:     v1alpha1.KindSkill,
		Metadata: v1alpha1.ObjectMeta{Name: "k8s-troubleshooter", Namespace: "sre"},
		Spec:     map[string]any{"title": "K8s Troubleshooter", "description": "debugs clusters"},
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if _, _, err := st.Apply(ctx, v1alpha1.Object{
		Kind:     v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{Name: "oncall", Namespace: "sre", Tag: "1.0.0"},
		Spec: map[string]any{
			"title":       "On-Call Responder",
			"description": "watches alerts",
			"a2a":         map[string]any{"url": "https://oncall.sre.svc/a2a/v1"},
			"skills":      []any{"k8s-troubleshooter"},
		},
	}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func TestSearchToolReturnsSafeKindFilteredStubs(t *testing.T) {
	st := store.NewMemory()
	for _, obj := range []v1alpha1.Object{
		{
			Kind: v1alpha1.KindTool,
			Metadata: v1alpha1.ObjectMeta{
				Name: "scanner", Namespace: "devai", Visibility: v1alpha1.VisibilityPublic,
			},
			Spec: map[string]any{
				"description": "Static application security",
				"inputSchema": map[string]any{"properties": map[string]any{"repository": map[string]any{"type": "string"}}},
			},
		},
		{
			Kind: v1alpha1.KindAgent,
			Metadata: v1alpha1.ObjectMeta{
				Name: "reviewer", Namespace: "devai", Visibility: v1alpha1.VisibilityPublic,
			},
			Spec: map[string]any{
				"description":  "Static application security",
				"systemPrompt": "PRIVATE SYSTEM PROMPT",
			},
		},
	} {
		if _, _, err := st.Apply(context.Background(), obj); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	d := &DiscoveryServer{store: st}
	params, _ := json.Marshal(map[string]any{
		"name": "search_registry",
		"arguments": map[string]any{
			"query": "static application",
			"kinds": []string{"tools"},
			"limit": 3,
		},
	})
	req := httptest.NewRequest("POST", "/mcp", nil)
	result, err := d.callTool(context.Background(), req, params)
	if err != nil {
		t.Fatalf("callTool: %v", err)
	}
	envelope, _ := result.(map[string]any)
	content, _ := envelope["content"].([]map[string]any)
	if len(content) != 1 {
		t.Fatalf("unexpected envelope: %#v", result)
	}
	text, _ := content[0]["text"].(string)
	if strings.Contains(text, "PRIVATE SYSTEM PROMPT") {
		t.Fatalf("search leaked an executable body: %s", text)
	}
	var hits []map[string]any
	if err := json.Unmarshal([]byte(text), &hits); err != nil {
		t.Fatalf("decode hits: %v", err)
	}
	if len(hits) != 1 || hits[0]["kind"] != "Tool" || hits[0]["name"] != "scanner" {
		t.Fatalf("unexpected hits: %#v", hits)
	}
	if hits[0]["fetchPath"] == "" {
		t.Fatalf("hit lacks exact fetch path: %#v", hits[0])
	}
}

// cardFromToolResult unwraps the MCP tools/call envelope and parses the card.
func cardFromToolResult(t *testing.T, res interface{}) map[string]any {
	t.Helper()
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("tool result is not a map: %T", res)
	}
	content, _ := m["content"].([]map[string]interface{})
	if len(content) == 0 {
		t.Fatalf("tool result has no content: %v", m)
	}
	text, _ := content[0]["text"].(string)
	var card map[string]any
	if err := json.Unmarshal([]byte(text), &card); err != nil {
		t.Fatalf("card text not JSON: %v\n%s", err, text)
	}
	return card
}

func provenance(t *testing.T, card map[string]any) map[string]any {
	t.Helper()
	caps, _ := card["capabilities"].(map[string]any)
	exts, _ := caps["extensions"].([]any)
	for _, e := range exts {
		ext, _ := e.(map[string]any)
		if uri, _ := ext["uri"].(string); uri == "https://registry.agentic.dev/ext/provenance" {
			params, _ := ext["params"].(map[string]any)
			return params
		}
	}
	t.Fatalf("card has no provenance extension: %v", card)
	return nil
}

func strFn(args map[string]string) func(string) string {
	return func(k string) string { return args[k] }
}

// The MCP get_agent_card tool must sign the card with the same registry
// attestation as the HTTP path — otherwise a verifying consumer would reject a
// card discovered over MCP. (Regression guard: it used to render unsigned.)
func TestAgentCardToolSignsCardWhenSigningEnabled(t *testing.T) {
	st := store.NewMemory()
	seedAgent(t, st)
	d := &DiscoveryServer{store: st, signer: signing.New("", true)} // ephemeral key

	res, err := d.agentCardTool(context.Background(),
		strFn(map[string]string{"name": "oncall", "namespace": "sre"}),
		func(v1alpha1.Object) bool { return true })
	if err != nil {
		t.Fatalf("agentCardTool: %v", err)
	}
	card := cardFromToolResult(t, res)
	if card["url"] != "https://oncall.sre.svc/a2a/v1" {
		t.Fatalf("card missing agent url: %v", card)
	}
	prov := provenance(t, card)
	if sig, _ := prov["signature"].(string); sig == "" {
		t.Fatalf("MCP card carries no signature despite signing enabled: %v", prov)
	}
	if by, _ := prov["signedBy"].(string); by == "" {
		t.Fatalf("MCP card carries no signedBy key id: %v", prov)
	}
}

// A nil/disabled signer must not panic and renders an unsigned-but-valid card.
func TestAgentCardToolNilSignerIsSafe(t *testing.T) {
	st := store.NewMemory()
	seedAgent(t, st)
	d := &DiscoveryServer{store: st, signer: nil}

	res, err := d.agentCardTool(context.Background(),
		strFn(map[string]string{"name": "oncall", "namespace": "sre"}),
		func(v1alpha1.Object) bool { return true })
	if err != nil {
		t.Fatalf("agentCardTool with nil signer: %v", err)
	}
	prov := provenance(t, cardFromToolResult(t, res))
	if _, ok := prov["signature"]; ok {
		t.Fatalf("nil signer should produce no signature: %v", prov)
	}
	if arn, _ := prov["arn"].(string); arn == "" {
		t.Fatalf("card should still pin the agent arn: %v", prov)
	}
}
