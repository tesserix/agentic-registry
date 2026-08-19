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

func TestBuildRoutes_Remote(t *testing.T) {
	servers := []v1alpha1.Object{
		remoteServer("io.github.acme/files", "https://files.acme.dev/mcp"),
	}
	routes, err := BuildRoutes(servers, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
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

func TestBuildRoutes_SkipsDirectoryEntries(t *testing.T) {
	catalogBySpec := remoteServer("catalog-notion-mcp", "https://mcp.notion.com/mcp")
	catalogBySpec.Spec["catalog"] = true

	catalogByLabel := remoteServer("catalog-slack-mcp", "https://mcp.slack.com/mcp")
	catalogByLabel.Metadata.Labels = map[string]string{"mcp.devai.io/catalog": "true"}

	servers := []v1alpha1.Object{
		catalogBySpec,
		catalogByLabel,
		remoteServer("homechef-mcp", "http://homechef-mcp.homechef.svc.cluster.local:8765/mcp"),
	}
	routes, err := BuildRoutes(servers, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want only the platform server routed, got %d routes", len(routes))
	}
	if routes[0].Name != "homechef-mcp" {
		t.Errorf("routed the wrong server: %q", routes[0].Name)
	}
}

func TestBuildRoutes_SkipsServerWithoutRemote(t *testing.T) {
	servers := []v1alpha1.Object{
		{Kind: v1alpha1.KindMCPServer, Metadata: v1alpha1.ObjectMeta{Name: "no-remote"}, Spec: map[string]interface{}{"name": "no-remote"}},
	}
	routes, err := BuildRoutes(servers, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Fatalf("a server with no endpoint and no spec.remotes[] must not be routed, got %d routes", len(routes))
	}
}

func TestBuildRoutes_RemoteCarriesItsOwnPort(t *testing.T) {
	servers := []v1alpha1.Object{
		remoteServer("platform-mcp", "http://platform-mcp.support-platform.svc.cluster.local:8765/mcp"),
	}
	routes, err := BuildRoutes(servers, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}
	got := routes[0].Target
	if got.host != "platform-mcp.support-platform.svc.cluster.local" || got.port != 8765 || got.path != "/mcp" {
		t.Errorf("target: got %+v", got)
	}
}

// spec.endpoint is the devai/solo dialect the seeds and the MCP Hub use;
// spec.remotes[] is the MCP-registry dialect. Both must route.
func TestBuildRoutes_EndpointDialect(t *testing.T) {
	servers := []v1alpha1.Object{
		{
			Kind:     v1alpha1.KindMCPServer,
			Metadata: v1alpha1.ObjectMeta{Name: "homechef-mcp"},
			Spec: map[string]interface{}{
				"name":      "homechef-mcp",
				"endpoint":  "http://homechef-mcp.homechef.svc.cluster.local:8765/mcp",
				"transport": "streamable-http",
			},
		},
	}
	routes, err := BuildRoutes(servers, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}
	got := routes[0].Target
	if got.host != "homechef-mcp.homechef.svc.cluster.local" || got.port != 8765 || got.path != "/mcp" {
		t.Errorf("target: got %+v", got)
	}
	if got.protocol != "StreamableHTTP" {
		t.Errorf("protocol: got %q", got.protocol)
	}
}

func TestBuildRoutes_RemotesWinOverEndpoint(t *testing.T) {
	srv := remoteServer("dual", "https://remote.dev/mcp")
	srv.Spec["endpoint"] = "http://legacy.svc.cluster.local:8080/mcp"
	routes, err := BuildRoutes([]v1alpha1.Object{srv}, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].Target.host != "remote.dev" {
		t.Errorf("spec.remotes[] must win: got %+v", routes[0].Target)
	}
}
