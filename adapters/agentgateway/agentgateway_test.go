package agentgateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func remoteServer(name, url string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: name, Labels: map[string]string{"mcp.tesserix.app/class": "platform"}},
		Spec: map[string]interface{}{
			"name":            name,
			"protocolVersion": "2026-07-28",
			"remotes": []interface{}{
				map[string]interface{}{"type": "streamableHttp", "url": url},
			},
		},
	}
}

func TestBuildRoutes_RequiresPlatformApprovalAndStatelessRevision(t *testing.T) {
	unapproved := remoteServer("unapproved-mcp", "https://unapproved.example/mcp")
	delete(unapproved.Metadata.Labels, "mcp.tesserix.app/class")

	legacy := remoteServer("legacy-mcp", "https://legacy.example/mcp")
	legacy.Spec["protocolVersion"] = "2025-11-25"

	routes, err := BuildRoutes([]v1alpha1.Object{
		unapproved,
		legacy,
		remoteServer("approved-mcp", "https://approved.example/mcp"),
	}, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Server != "approved-mcp" {
		t.Fatalf("want only the approved stateless server routed, got %+v", routes)
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
	if routes[0].Server != "io-github-acme-files" {
		t.Errorf("name sanitization: got %q", routes[0].Server)
	}
	if routes[0].Path != "/mcp/default/io-github-acme-files" {
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
    mcp.tesserix.app/tenant: default
    registry.agentic.dev/mcp: a-server
  name: default-a-server
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
    mcp.tesserix.app/tenant: default
    registry.agentic.dev/mcp: a-server
  name: default-a-server
  namespace: agentgateway-system
spec:
  parentRefs:
    - name: agentgateway
      namespace: agentgateway-system
  rules:
    - backendRefs:
        - group: agentgateway.dev
          kind: AgentgatewayBackend
          name: default-a-server
      matches:
        - path:
            type: PathPrefix
            value: /mcp/default/a-server
        - path:
            type: PathPrefix
            value: /mcp/a-server
`
	// Assert the a-server block (sorted first) appears verbatim.
	if !strings.Contains(got, strings.TrimSpace(want)) {
		t.Errorf("golden mismatch.\n--- got ---\n%s\n--- want (substring) ---\n%s", got, want)
	}
	// b-server must come after a-server (deterministic ordering).
	if strings.Index(got, "name: default-a-server") > strings.Index(got, "name: default-b-server") {
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
	if routes[0].Server != "homechef-mcp" {
		t.Errorf("routed the wrong server: %q", routes[0].Server)
	}
}

func TestBuildRoutes_SkipsServersThatDisableGatewayExport(t *testing.T) {
	disabledBySpec := remoteServer("google-vertex-mcp", "https://aiplatform.googleapis.com/mcp/generate")
	disabledBySpec.Spec["gatewayExport"] = false

	disabledByLabel := remoteServer("google-registry-mcp", "https://agentregistry.googleapis.com/mcp")
	disabledByLabel.Metadata.Labels = map[string]string{"mcp.tesserix.app/gateway-export": "false"}

	routes, err := BuildRoutes([]v1alpha1.Object{
		disabledBySpec,
		disabledByLabel,
		remoteServer("sample-mcp", "http://sample.devai.svc.cluster.local:8080/mcp"),
	}, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Server != "sample-mcp" {
		t.Fatalf("want only the gateway-exported server routed, got %+v", routes)
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
			Metadata: v1alpha1.ObjectMeta{Name: "homechef-mcp", Labels: map[string]string{"mcp.tesserix.app/class": "platform"}},
			Spec: map[string]interface{}{
				"name":            "homechef-mcp",
				"endpoint":        "http://homechef-mcp.homechef.svc.cluster.local:8765/mcp",
				"transport":       "streamable-http",
				"protocolVersion": "2026-07-28",
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

func TestBuildRoutes_ServiceSelectorUsesIdentityAwareBackend(t *testing.T) {
	srv := remoteServer("devai-mcp", "http://devai-api.devai.svc.cluster.local:8080/mcp/devai")
	srv.Spec["serviceSelector"] = map[string]interface{}{
		"namespaces": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"kubernetes.io/metadata.name": "devai",
			},
		},
		"services": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app.kubernetes.io/name": "devai-api",
			},
		},
	}

	routes, err := BuildRoutes([]v1alpha1.Object{srv}, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}

	spec := routes[0].Backend["spec"].(map[string]interface{})
	mcp := spec["mcp"].(map[string]interface{})
	target := mcp["targets"].([]interface{})[0].(map[string]interface{})
	if _, exists := target["static"]; exists {
		t.Fatalf("in-cluster selector must not render a raw static target: %+v", target)
	}
	want := srv.Spec["serviceSelector"]
	if !reflect.DeepEqual(want, target["selector"]) {
		t.Fatalf("selector:\n got %#v\nwant %#v", target["selector"], want)
	}
}

func TestBuildRoutes_InvalidServiceSelectorFailsClosed(t *testing.T) {
	srv := remoteServer("devai-mcp", "http://devai-api.devai.svc.cluster.local:8080/mcp/devai")
	srv.Spec["serviceSelector"] = map[string]interface{}{
		"services": map[string]interface{}{
			"matchLabels": map[string]interface{}{},
		},
	}

	if _, err := BuildRoutes([]v1alpha1.Object{srv}, Options{}); err == nil {
		t.Fatal("empty serviceSelector must be rejected instead of falling back to raw TCP")
	}
}

func tenantServer(tenant, name, url string) v1alpha1.Object {
	srv := remoteServer(name, url)
	srv.Metadata.Labels[tenantLabel] = tenant
	return srv
}

func TestBuildRoutes_TenantScopedPath(t *testing.T) {
	servers := []v1alpha1.Object{
		tenantServer("homechef", "homechef-mcp", "http://homechef-mcp.homechef.svc.cluster.local:8765/mcp"),
	}
	routes, err := BuildRoutes(servers, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}
	if routes[0].Tenant != "homechef" {
		t.Errorf("tenant: got %q", routes[0].Tenant)
	}
	if routes[0].Path != "/mcp/homechef/homechef-mcp" {
		t.Errorf("path: got %q", routes[0].Path)
	}
	if routes[0].Name != "homechef-homechef-mcp" {
		t.Errorf("resource name: got %q", routes[0].Name)
	}
}

func TestBuildRoutes_TenantFallsBackToNamespaceThenDefault(t *testing.T) {
	inNamespace := remoteServer("scm-mcp", "https://scm.dev/mcp")
	inNamespace.Metadata.Namespace = "mark8ly"

	servers := []v1alpha1.Object{inNamespace, remoteServer("devai-mcp", "https://devai.dev/mcp")}
	routes, err := BuildRoutes(servers, Options{DefaultTenant: "devai"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Route{}
	for _, r := range routes {
		byName[r.ServerName] = r
	}
	if got := byName["scm-mcp"].Tenant; got != "mark8ly" {
		t.Errorf("namespace fallback: got %q", got)
	}
	if got := byName["devai-mcp"].Tenant; got != "devai" {
		t.Errorf("default fallback: got %q", got)
	}
}

// Two tenants may legitimately publish a server with the same name; neither
// may overwrite the other's Backend, HTTPRoute or policy.
func TestBuildRoutes_SameNameAcrossTenantsDoNotCollide(t *testing.T) {
	servers := []v1alpha1.Object{
		tenantServer("homechef", "support-mcp", "http://support-mcp.homechef.svc.cluster.local:8765/mcp"),
		tenantServer("mark8ly", "support-mcp", "http://support-mcp.mark8ly.svc.cluster.local:8765/mcp"),
	}
	routes, err := BuildRoutes(servers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("want both tenants routed, got %d", len(routes))
	}
	if routes[0].Name == routes[1].Name {
		t.Fatalf("resource names collide: %q", routes[0].Name)
	}
	if routes[0].Path == routes[1].Path {
		t.Fatalf("paths collide: %q", routes[0].Path)
	}
}

func TestBuildRoutes_PerServerAuthorizationPolicy(t *testing.T) {
	servers := []v1alpha1.Object{
		tenantServer("homechef", "homechef-mcp", "http://homechef-mcp.homechef.svc.cluster.local:8765/mcp"),
	}
	routes, err := BuildRoutes(servers, Options{
		Namespace:          "agentgateway-system",
		RequireServerScope: true,
		ScopeClaim:         "urn:zitadel:iam:org:project:1:roles",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := routes[0].Policy
	if policy == nil {
		t.Fatal("want an AgentgatewayPolicy for the route")
	}
	if got := policy["kind"]; got != "AgentgatewayPolicy" {
		t.Errorf("kind: got %v", got)
	}
	meta := policy["metadata"].(map[string]interface{})
	if meta["name"] != "homechef-homechef-mcp" || meta["namespace"] != "agentgateway-system" {
		t.Errorf("metadata: got %v", meta)
	}
	spec := policy["spec"].(map[string]interface{})
	targetRefs := spec["targetRefs"].([]interface{})
	ref := targetRefs[0].(map[string]interface{})
	if ref["kind"] != "HTTPRoute" || ref["name"] != "homechef-homechef-mcp" {
		t.Errorf("policy must target its own HTTPRoute, got %v", ref)
	}
	traffic := spec["traffic"].(map[string]interface{})
	authz := traffic["authorization"].(map[string]interface{})
	if authz["action"] != "Allow" {
		t.Errorf("action: got %v", authz["action"])
	}
	exprs := authz["policy"].(map[string]interface{})["matchExpressions"].([]interface{})
	want := `"mcp:homechef:homechef-mcp" in jwt["urn:zitadel:iam:org:project:1:roles"]`
	if exprs[0] != want {
		t.Errorf("scope expression:\n got %v\nwant %s", exprs[0], want)
	}
}

// The scope grant has to exist in Zitadel before enforcement, so policy
// rendering is opt-in and off by default.
func TestBuildRoutes_NoPolicyUnlessScopeRequired(t *testing.T) {
	routes, err := BuildRoutes([]v1alpha1.Object{
		tenantServer("homechef", "homechef-mcp", "https://homechef.dev/mcp"),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].Policy != nil {
		t.Error("policy must not be rendered unless RequireServerScope is set")
	}
}

func TestBuild_IncludesPolicyDocument(t *testing.T) {
	out, err := Build([]v1alpha1.Object{
		tenantServer("homechef", "homechef-mcp", "https://homechef.dev/mcp"),
	}, Options{RequireServerScope: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "kind: AgentgatewayPolicy") {
		t.Errorf("policy missing from stream:\n%s", out)
	}
}

// The flat /mcp/<server> path keeps existing callers working during the move
// to tenant-scoped paths, but only while one tenant owns the name.
func TestBuildRoutes_FlatPathDroppedWhenNameIsAmbiguous(t *testing.T) {
	shared := []v1alpha1.Object{
		tenantServer("homechef", "support-mcp", "https://a.dev/mcp"),
		tenantServer("mark8ly", "support-mcp", "https://b.dev/mcp"),
	}
	routes, err := BuildRoutes(shared, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		for _, m := range routeMatches(t, r) {
			if m == "/mcp/support-mcp" {
				t.Fatalf("%s: ambiguous flat path must not be served", r.Name)
			}
		}
	}

	sole, err := BuildRoutes([]v1alpha1.Object{shared[0]}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := routeMatches(t, sole[0]); len(got) != 2 || got[1] != "/mcp/support-mcp" {
		t.Errorf("sole owner keeps its flat path, got %v", got)
	}
}

func TestBuildRoutes_FlatPathDisabled(t *testing.T) {
	off := false
	routes, err := BuildRoutes([]v1alpha1.Object{
		tenantServer("homechef", "homechef-mcp", "https://a.dev/mcp"),
	}, Options{LegacyFlatPath: &off})
	if err != nil {
		t.Fatal(err)
	}
	if got := routeMatches(t, routes[0]); len(got) != 1 || got[0] != "/mcp/homechef/homechef-mcp" {
		t.Errorf("want only the tenant path, got %v", got)
	}
}

func routeMatches(t *testing.T, r Route) []string {
	t.Helper()
	spec := r.HTTPRoute["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	matches := rules[0].(map[string]interface{})["matches"].([]interface{})
	paths := make([]string, 0, len(matches))
	for _, m := range matches {
		path := m.(map[string]interface{})["path"].(map[string]interface{})
		paths = append(paths, path["value"].(string))
	}
	return paths
}

// Credential brokering: the agent presents only its own identity token; the
// upstream API key lives in a vault-backed Secret the gateway injects.
func TestBuildRoutes_BrokersUpstreamCredentialFromSecret(t *testing.T) {
	srv := remoteServer("jira-mcp", "https://jira.acme.dev/mcp")
	srv.Spec["credentialRef"] = map[string]interface{}{
		"secretName": "jira-mcp-upstream",
		"key":        "token",
	}
	routes, err := BuildRoutes([]v1alpha1.Object{srv}, Options{Namespace: "agentgateway-system"})
	if err != nil {
		t.Fatal(err)
	}
	cred := routes[0].CredentialPolicy
	if cred == nil {
		t.Fatal("a server declaring a credentialRef must get a backend auth policy")
	}
	spec := cred["spec"].(map[string]interface{})
	target := spec["targetRefs"].([]interface{})[0].(map[string]interface{})
	if target["kind"] != backendKind || target["name"] != "default-jira-mcp" {
		t.Errorf("credential must attach to the server's own backend: %+v", target)
	}
	auth := spec["backend"].(map[string]interface{})["auth"].(map[string]interface{})
	ref := auth["secretRef"].(map[string]interface{})
	if ref["name"] != "jira-mcp-upstream" || ref["key"] != "token" {
		t.Errorf("secretRef: %+v", ref)
	}
}

func TestBuildRoutes_CredentialLocationIsHonoured(t *testing.T) {
	srv := remoteServer("github-mcp", "https://api.github.com/mcp")
	srv.Spec["credentialRef"] = map[string]interface{}{
		"secretName": "github-mcp-upstream",
		"header":     "X-Api-Key",
		"prefix":     "token ",
	}
	routes, err := BuildRoutes([]v1alpha1.Object{srv}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	auth := routes[0].CredentialPolicy["spec"].(map[string]interface{})["backend"].(map[string]interface{})["auth"].(map[string]interface{})
	header := auth["location"].(map[string]interface{})["header"].(map[string]interface{})
	if header["name"] != "X-Api-Key" || header["prefix"] != "token " {
		t.Errorf("credential location: %+v", header)
	}
}

// A registry manifest is world-readable to every catalog consumer, so a
// literal credential in it is a leak, not a configuration choice.
func TestBuildRoutes_InlineCredentialIsRejected(t *testing.T) {
	for _, field := range []string{"value", "token", "apiKey"} {
		srv := remoteServer("leaky-mcp", "https://leaky.dev/mcp")
		srv.Spec["credentialRef"] = map[string]interface{}{
			"secretName": "leaky-upstream",
			field:        "super-secret",
		}
		if _, err := BuildRoutes([]v1alpha1.Object{srv}, Options{}); err == nil {
			t.Errorf("inline credential field %q must be rejected", field)
		}
	}
}

func TestBuildRoutes_CredentialRefWithoutSecretNameIsRejected(t *testing.T) {
	srv := remoteServer("jira-mcp", "https://jira.acme.dev/mcp")
	srv.Spec["credentialRef"] = map[string]interface{}{"key": "token"}
	if _, err := BuildRoutes([]v1alpha1.Object{srv}, Options{}); err == nil {
		t.Error("a credentialRef naming no Secret must be rejected")
	}
}

func TestBuild_IncludesCredentialPolicyDocument(t *testing.T) {
	srv := remoteServer("jira-mcp", "https://jira.acme.dev/mcp")
	srv.Spec["credentialRef"] = map[string]interface{}{"secretName": "jira-mcp-upstream"}
	out, err := Build([]v1alpha1.Object{srv}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "name: jira-mcp-upstream") || !strings.Contains(got, "secretRef:") {
		t.Errorf("credential policy missing from stream:\n%s", got)
	}
	if strings.Contains(got, "super-secret") {
		t.Error("rendered config must never carry credential material")
	}
}
