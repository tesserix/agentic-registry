// Package agentgateway renders registry MCPServer objects into agentgateway
// data-plane config: one AgentgatewayBackend + one HTTPRoute per server, so a
// registered MCP server becomes reachable at {gateway}/mcp/<name>.
//
// The output is plain Kubernetes YAML (an agentgateway.dev AgentgatewayBackend
// plus a gateway.networking.k8s.io HTTPRoute), ready for server-side-apply by
// the route-sync Job (workstream G1). Pure function, no I/O.
//
// GVKs verified against the installed CRDs (agentgateway.dev/v1alpha1
// AgentgatewayBackend; gateway.networking.k8s.io/v1 HTTPRoute) — re-verify on a
// chart bump before wiring an automated apply.
package agentgateway

import (
	"bytes"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tesserix/agentic-registry/adapters"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const (
	httpRouteAPIVersion = "gateway.networking.k8s.io/v1"
	httpRouteGroup      = "gateway.networking.k8s.io"
	httpRouteKind       = "HTTPRoute"
	backendAPIVersion   = "agentgateway.dev/v1alpha1"
	backendKind         = "AgentgatewayBackend"
	backendGroup        = "agentgateway.dev"
	policyKind          = "AgentgatewayPolicy"

	// tenantLabel names the tenant that owns an MCP server. It decides the
	// route's path segment, its resource names and its required scope, so a
	// server may not change tenant without changing all three.
	tenantLabel = "mcp.tesserix.app/tenant"

	defaultTenant = "default"
	// defaultScopeClaim is Zitadel's project-agnostic roles claim. Deployments
	// that pin the audience use the project-scoped claim instead.
	defaultScopeClaim = "urn:zitadel:iam:org:project:roles"
)

// Options controls where the rendered routes attach.
type Options struct {
	// Namespace the Backend/HTTPRoute objects are created in.
	Namespace string
	// GatewayName / GatewayNamespace the HTTPRoutes attach to (parentRef).
	GatewayName      string
	GatewayNamespace string
	// PathPrefix is prepended to each server's path. Defaults to "/mcp".
	PathPrefix string
	// DefaultTenant owns servers that declare no tenant label and no
	// namespace. Defaults to "default".
	DefaultTenant string
	// LegacyFlatPath also serves each route at the pre-tenancy /mcp/<server>
	// path, so callers can migrate to /mcp/<tenant>/<server> without a flag
	// day. Only rendered where <server> is unambiguous across tenants.
	LegacyFlatPath *bool
	// RequireServerScope renders an AgentgatewayPolicy per route demanding the
	// mcp:<tenant>:<server> scope. Off by default: the gateway denies every
	// caller that has not been granted the scope in the identity provider yet.
	RequireServerScope bool
	// ScopeClaim is the JWT claim the scope is read from.
	ScopeClaim string
}

func (o Options) withDefaults() Options {
	if o.Namespace == "" {
		o.Namespace = "agentgateway-system"
	}
	if o.GatewayName == "" {
		o.GatewayName = "agentgateway"
	}
	if o.GatewayNamespace == "" {
		o.GatewayNamespace = o.Namespace
	}
	if o.PathPrefix == "" {
		o.PathPrefix = "/mcp"
	}
	if o.DefaultTenant == "" {
		o.DefaultTenant = defaultTenant
	}
	if o.ScopeClaim == "" {
		o.ScopeClaim = defaultScopeClaim
	}
	if o.LegacyFlatPath == nil {
		enabled := true
		o.LegacyFlatPath = &enabled
	}
	return o
}

// target is the resolved upstream for one MCP server's Backend.
type target struct {
	host     string
	port     int
	path     string
	protocol string // StreamableHTTP | SSE
	selector map[string]interface{}
}

// Route is the rendered routing for a single MCP server.
type Route struct {
	ServerName string // original registry name
	Tenant     string // owning tenant
	Server     string // sanitized server name
	Name       string // sanitized resource name, "<tenant>-<server>"
	Path       string // "/mcp/<tenant>/<server>"
	Scope      string // "mcp:<tenant>:<server>"
	Target     target
	Backend    map[string]interface{}
	HTTPRoute  map[string]interface{}
	Policy     map[string]interface{} // nil unless RequireServerScope
	// CredentialPolicy injects the upstream credential from a vault-backed
	// Secret. Nil unless the server declares a credentialRef.
	CredentialPolicy map[string]interface{}
}

// Build renders Backend + HTTPRoute objects for every MCP server as a single
// multi-document YAML stream (sorted by name for stable output).
func Build(servers []v1alpha1.Object, opts Options) ([]byte, error) {
	routes, err := BuildRoutes(servers, opts)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, r := range routes {
		if err := enc.Encode(r.Backend); err != nil {
			return nil, err
		}
		if err := enc.Encode(r.HTTPRoute); err != nil {
			return nil, err
		}
		if r.Policy != nil {
			if err := enc.Encode(r.Policy); err != nil {
				return nil, err
			}
		}
		if r.CredentialPolicy != nil {
			if err := enc.Encode(r.CredentialPolicy); err != nil {
				return nil, err
			}
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BuildRoutes is the structured form of Build.
func BuildRoutes(servers []v1alpha1.Object, opts Options) ([]Route, error) {
	opts = opts.withDefaults()
	seen := map[string]bool{}
	routes := make([]Route, 0, len(servers))
	tenantsPerServer := serverTenantCount(servers, opts)

	for _, srv := range servers {
		name := serverName(srv)
		if name == "" || !gatewayExportQualified(srv) || isDirectory(srv) || gatewayExportDisabled(srv) {
			continue
		}
		san := adapters.SanitizeName(name)
		tenant := tenantFor(srv, opts)
		resourceName := tenant + "-" + san
		if seen[resourceName] {
			continue
		}
		seen[resourceName] = true

		tgt, ok, err := targetFor(srv)
		if err != nil {
			return nil, fmt.Errorf("mcp server %s: %w", name, err)
		}
		if !ok {
			continue
		}
		path := opts.PathPrefix + "/" + tenant + "/" + san
		scope := "mcp:" + tenant + ":" + san
		labels := map[string]interface{}{
			"app.kubernetes.io/managed-by": "agentic-registry",
			"registry.agentic.dev/mcp":     san,
			"mcp.tesserix.app/tenant":      tenant,
		}

		targetSpec := map[string]interface{}{"name": san}
		if tgt.selector != nil {
			targetSpec["selector"] = tgt.selector
		} else {
			targetSpec["static"] = map[string]interface{}{
				"host":     tgt.host,
				"port":     tgt.port,
				"path":     tgt.path,
				"protocol": tgt.protocol,
			}
		}

		backend := map[string]interface{}{
			"apiVersion": backendAPIVersion,
			"kind":       backendKind,
			"metadata": map[string]interface{}{
				"name":      resourceName,
				"namespace": opts.Namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"mcp": map[string]interface{}{
					"targets": []interface{}{targetSpec},
				},
			},
		}

		matches := []interface{}{
			map[string]interface{}{
				"path": map[string]interface{}{
					"type":  "PathPrefix",
					"value": path,
				},
			},
		}
		if *opts.LegacyFlatPath && tenantsPerServer[san] == 1 {
			matches = append(matches, map[string]interface{}{
				"path": map[string]interface{}{
					"type":  "PathPrefix",
					"value": opts.PathPrefix + "/" + san,
				},
			})
		}

		httpRoute := map[string]interface{}{
			"apiVersion": httpRouteAPIVersion,
			"kind":       httpRouteKind,
			"metadata": map[string]interface{}{
				"name":      resourceName,
				"namespace": opts.Namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"parentRefs": []interface{}{
					map[string]interface{}{
						"name":      opts.GatewayName,
						"namespace": opts.GatewayNamespace,
					},
				},
				"rules": []interface{}{
					map[string]interface{}{
						"matches": matches,
						"backendRefs": []interface{}{
							map[string]interface{}{
								"group": backendGroup,
								"kind":  backendKind,
								"name":  resourceName,
							},
						},
					},
				},
			},
		}

		var policy map[string]interface{}
		if opts.RequireServerScope {
			policy = scopePolicy(resourceName, scope, labels, opts)
		}

		credential, err := credentialPolicy(srv, resourceName, labels, opts)
		if err != nil {
			return nil, fmt.Errorf("mcp server %s: %w", name, err)
		}

		routes = append(routes, Route{
			ServerName: name,
			Tenant:     tenant,
			Server:     san,
			Name:       resourceName,
			Path:       path,
			Scope:      scope,
			Target:     tgt,
			Backend:    backend,
			HTTPRoute:  httpRoute,
			Policy:     policy,

			CredentialPolicy: credential,
		})
	}

	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return routes, nil
}

// scopePolicy renders the per-route authorization: a caller reaches this one
// server only if its token carries that server's scope. Without it the coarse
// gateway-wide role grants every MCP server on the origin.
func scopePolicy(resourceName, scope string, labels map[string]interface{}, opts Options) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": backendAPIVersion,
		"kind":       policyKind,
		"metadata": map[string]interface{}{
			"name":      resourceName,
			"namespace": opts.Namespace,
			"labels":    labels,
		},
		"spec": map[string]interface{}{
			"targetRefs": []interface{}{
				map[string]interface{}{
					"group": httpRouteGroup,
					"kind":  httpRouteKind,
					"name":  resourceName,
				},
			},
			"traffic": map[string]interface{}{
				"authorization": map[string]interface{}{
					"action": "Allow",
					"policy": map[string]interface{}{
						"matchExpressions": []interface{}{
							fmt.Sprintf("%q in jwt[%q]", scope, opts.ScopeClaim),
						},
					},
				},
			},
		},
	}
}

// inlineCredentialFields are the shapes a literal secret would take in a
// manifest. The registry is a catalog, not a vault: a credential reaches the
// gateway only as a reference to a Secret the platform controls.
var inlineCredentialFields = []string{"value", "token", "apiKey", "secret", "password"}

// credentialPolicy renders the brokered upstream credential: the gateway reads
// the Secret and injects it, so the calling agent never holds the upstream key
// and only ever presents its own identity token.
func credentialPolicy(srv v1alpha1.Object, resourceName string, labels map[string]interface{}, opts Options) (map[string]interface{}, error) {
	raw, ok := srv.Spec["credentialRef"].(map[string]interface{})
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	for _, field := range inlineCredentialFields {
		if _, present := raw[field]; present {
			return nil, fmt.Errorf("credentialRef.%s carries credential material; reference a Secret instead", field)
		}
	}
	secretName, _ := raw["secretName"].(string)
	if secretName == "" {
		return nil, fmt.Errorf("credentialRef must name a Secret via secretName")
	}

	secretRef := map[string]interface{}{"name": secretName}
	if key, _ := raw["key"].(string); key != "" {
		secretRef["key"] = key
	}
	auth := map[string]interface{}{"secretRef": secretRef}
	if header, _ := raw["header"].(string); header != "" {
		location := map[string]interface{}{"name": header}
		if prefix, _ := raw["prefix"].(string); prefix != "" {
			location["prefix"] = prefix
		}
		auth["location"] = map[string]interface{}{"header": location}
	}

	return map[string]interface{}{
		"apiVersion": backendAPIVersion,
		"kind":       policyKind,
		"metadata": map[string]interface{}{
			"name":      resourceName + "-credential",
			"namespace": opts.Namespace,
			"labels":    labels,
		},
		"spec": map[string]interface{}{
			"targetRefs": []interface{}{
				map[string]interface{}{
					"group": backendGroup,
					"kind":  backendKind,
					"name":  resourceName,
				},
			},
			"backend": map[string]interface{}{"auth": auth},
		},
	}, nil
}

// tenantFor resolves the owning tenant: the explicit label, else the registry
// namespace the server was published into, else the export's default.
func tenantFor(srv v1alpha1.Object, opts Options) string {
	if t := srv.Metadata.Labels[tenantLabel]; t != "" {
		return adapters.SanitizeName(t)
	}
	if srv.Metadata.Namespace != "" {
		return adapters.SanitizeName(srv.Metadata.Namespace)
	}
	return adapters.SanitizeName(opts.DefaultTenant)
}

// serverTenantCount counts how many tenants publish each server name. A name
// claimed by two tenants can have no unambiguous flat path.
func serverTenantCount(servers []v1alpha1.Object, opts Options) map[string]int {
	tenants := map[string]map[string]bool{}
	for _, srv := range servers {
		name := serverName(srv)
		if name == "" || isDirectory(srv) {
			continue
		}
		san := adapters.SanitizeName(name)
		if tenants[san] == nil {
			tenants[san] = map[string]bool{}
		}
		tenants[san][tenantFor(srv, opts)] = true
	}
	counts := make(map[string]int, len(tenants))
	for san, set := range tenants {
		counts[san] = len(set)
	}
	return counts
}

// serverName prefers spec.name, falling back to metadata.name.
func serverName(srv v1alpha1.Object) string {
	if n, ok := srv.Spec["name"].(string); ok && n != "" {
		return n
	}
	return srv.Metadata.Name
}

// targetFor resolves where a server's Backend points. An explicit Kubernetes
// Service selector takes precedence over remotes because it lets AgentGateway
// use WDS/HBONE and preserve its workload identity. Public/SaaS servers retain
// their static URL target. A server with neither has no resolvable upstream.
func targetFor(srv v1alpha1.Object) (target, bool, error) {
	selector, present, err := serviceSelectorFor(srv)
	if err != nil {
		return target{}, false, err
	}
	if present {
		return target{selector: selector}, true, nil
	}

	u, transport := firstRemote(srv)
	if u == "" {
		return target{}, false, nil
	}
	return parseRemote(u, transport), true, nil
}

// serviceSelectorFor accepts the portable Registry extension used for
// in-cluster MCP Services. Only non-empty matchLabels are supported initially;
// rejecting malformed or broader shapes prevents an intended identity-aware
// target from silently degrading to raw TCP.
func serviceSelectorFor(srv v1alpha1.Object) (map[string]interface{}, bool, error) {
	value, present := srv.Spec["serviceSelector"]
	if !present {
		return nil, false, nil
	}
	raw, ok := value.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("serviceSelector must be an object")
	}

	selector := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		if key != "namespaces" && key != "services" {
			return nil, false, fmt.Errorf("serviceSelector.%s is not supported", key)
		}
		part, ok := value.(map[string]interface{})
		if !ok || len(part) != 1 {
			return nil, false, fmt.Errorf("serviceSelector.%s must contain only matchLabels", key)
		}
		labels, ok := part["matchLabels"].(map[string]interface{})
		if !ok || len(labels) == 0 {
			return nil, false, fmt.Errorf("serviceSelector.%s.matchLabels must not be empty", key)
		}
		for label, value := range labels {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(label) == "" || strings.TrimSpace(text) == "" {
				return nil, false, fmt.Errorf("serviceSelector.%s.matchLabels must contain non-empty strings", key)
			}
		}
		selector[key] = map[string]interface{}{"matchLabels": labels}
	}
	if len(selector) == 0 {
		return nil, false, fmt.Errorf("serviceSelector must select namespaces or services")
	}
	return selector, true, nil
}

// isDirectory reports whether a server is a directory entry — a third-party MCP
// the catalog lists for humans to wire into their own client, authenticated per
// user. It has no platform-held credential, so the gateway must never route it.
// Same signal the DevAI MCP Hub skips on.
func isDirectory(srv v1alpha1.Object) bool {
	if c, ok := srv.Spec["catalog"].(bool); ok && c {
		return true
	}
	return srv.Metadata.Labels["mcp.devai.io/catalog"] == "true"
}

func gatewayExportQualified(srv v1alpha1.Object) bool {
	return srv.Metadata.Labels["mcp.tesserix.app/class"] == "platform" &&
		srv.Spec["protocolVersion"] == "2026-07-28"
}

// gatewayExportDisabled keeps a server discoverable by Registry consumers
// while excluding it from Gateway routing. This is useful for runtimes such as
// the DevAI Hub that can provide workload-local authentication (for example,
// GCP ADC) which the shared Gateway data plane does not hold.
func gatewayExportDisabled(srv v1alpha1.Object) bool {
	if enabled, ok := srv.Spec["gatewayExport"].(bool); ok && !enabled {
		return true
	}
	return srv.Metadata.Labels["mcp.tesserix.app/gateway-export"] == "false"
}

// firstRemote returns the first remote endpoint URL and its transport hint,
// accepting both dialects: spec.remotes[] (MCP-registry) and spec.endpoint
// (devai/solo, what the seeds carry and the MCP Hub dials).
func firstRemote(srv v1alpha1.Object) (string, string) {
	remotes, ok := srv.Spec["remotes"].([]interface{})
	if !ok {
		u, _ := srv.Spec["endpoint"].(string)
		transport, _ := srv.Spec["transport"].(string)
		return u, transport
	}
	for _, r := range remotes {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		u, _ := m["url"].(string)
		if u == "" {
			continue
		}
		transport, _ := m["type"].(string)
		return u, transport
	}
	return "", ""
}

// parseRemote turns a remote endpoint URL into a static MCP target.
func parseRemote(raw, transport string) target {
	t := target{path: "/mcp", protocol: "StreamableHTTP"}
	if strings.EqualFold(transport, "sse") {
		t.protocol = "SSE"
		t.path = "/sse"
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.host = raw
		return t
	}
	t.host = u.Hostname()
	if u.Path != "" && u.Path != "/" {
		t.path = u.Path
	}
	if p := u.Port(); p != "" {
		fmt.Sscanf(p, "%d", &t.port)
	} else if u.Scheme == "https" {
		t.port = 443
	} else {
		t.port = 80
	}
	return t
}
