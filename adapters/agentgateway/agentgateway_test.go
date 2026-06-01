package agentgateway

import (
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func remoteServer(name, url string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: name},
		Spec: map[string]interface{}{
			"name": name,
			"remotes": []interface{}{
				map[string]interface{}{"type": "streamableHttp", "url": url},
			},
		},
	}
}

func TestBuildRoutes_RemoteAndSandbox(t *testing.T) {
	servers := []v1alpha1.Object{
		remoteServer("io.github.acme/files", "https://files.acme.dev/mcp"),
		// no remotes → sandbox Service
		{Kind: v1alpha1.KindMCPServer, Metadata: v1alpha1.ObjectMeta{Name: "local-tool"}, Spec: map[string]interface{}{"name": "local-tool"}},
	}
	routes, err := BuildRoutes(servers, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("want 2 routes, got %d", len(routes))
	}
	// Sorted by sanitized name: "io-github-acme-files" < "local-tool".
	if routes[0].Name != "io-github-acme-files" {
		t.Errorf("name sanitization: got %q", routes[0].Name)
	}
	if routes[0].Path != "/mcp/io-github-acme-files" {
		t.Errorf("path: got %q", routes[0].Path)
	}
	// Remote URL parses into a static host/port/path target.
	if routes[0].Target.host != "files.acme.dev" || routes[0].Target.port != 443 || routes[0].Target.path != "/mcp" {
		t.Errorf("remote target: got %+v", routes[0].Target)
	}
	if routes[0].Target.protocol != "StreamableHTTP" {
		t.Errorf("protocol: got %q", routes[0].Target.protocol)
	}
	// Sandbox server resolves to an in-cluster Service.
	if routes[1].Target.host != "local-tool.agentgateway-system.svc.cluster.local" || routes[1].Target.port != 8080 {
		t.Errorf("sandbox target: got %+v", routes[1].Target)
	}
}

func TestBuild_DeterministicYAML(t *testing.T) {
	servers := []v1alpha1.Object{
		remoteServer("b-server", "https://b.dev/mcp"),
		remoteServer("a-server", "https://a.dev/mcp"),
	}
	out, err := Build(servers, Options{Namespace: "agentgateway-system", GatewayName: "agentgateway"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// yaml.v3 sorts map keys alphabetically — the golden reflects that.
	// GVK + target shape verified against the installed agentgateway CRD.
	want := `apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata:
  labels:
    app.kubernetes.io/managed-by: agentic-registry
    registry.agentic.dev/mcp: a-server
  name: a-server
  namespace: agentgateway-system
spec:
  mcp:
    targets:
      - name: a-server
        static:
          host: a.dev
          path: /mcp
          port: 443
          protocol: StreamableHTTP
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  labels:
    app.kubernetes.io/managed-by: agentic-registry
    registry.agentic.dev/mcp: a-server
  name: a-server
  namespace: agentgateway-system
spec:
  parentRefs:
    - name: agentgateway
      namespace: agentgateway-system
  rules:
    - backendRefs:
        - group: agentgateway.dev
          kind: AgentgatewayBackend
          name: a-server
      matches:
        - path:
            type: PathPrefix
            value: /mcp/a-server
`
	// Assert the a-server block (sorted first) appears verbatim.
	if !strings.Contains(got, strings.TrimSpace(want)) {
		t.Errorf("golden mismatch.\n--- got ---\n%s\n--- want (substring) ---\n%s", got, want)
	}
	// b-server must come after a-server (deterministic ordering).
	if strings.Index(got, "name: a-server") > strings.Index(got, "name: b-server") {
		t.Error("routes not sorted by name")
	}
}

func TestBuild_DedupesBySanitizedName(t *testing.T) {
	servers := []v1alpha1.Object{
		remoteServer("My Tool", "https://1.dev/mcp"),
		remoteServer("my-tool", "https://2.dev/mcp"), // collides after sanitize
	}
	routes, err := BuildRoutes(servers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 deduped route, got %d", len(routes))
	}
}
