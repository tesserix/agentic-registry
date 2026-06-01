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
	backendAPIVersion   = "agentgateway.dev/v1alpha1"
	backendKind         = "AgentgatewayBackend"
	backendGroup        = "agentgateway.dev"
)

// Options controls where the rendered routes attach and how unrouted
// (non-remote) MCP servers resolve to an in-cluster Service.
type Options struct {
	// Namespace the Backend/HTTPRoute objects are created in.
	Namespace string
	// GatewayName / GatewayNamespace the HTTPRoutes attach to (parentRef).
	GatewayName      string
	GatewayNamespace string
	// PathPrefix is prepended to each server's path. Defaults to "/mcp".
	PathPrefix string
	// SandboxNamespace is where image/package MCP servers run; their Backend
	// targets <sanitized-name>.<SandboxNamespace>.svc.cluster.local. Defaults
	// to the Namespace when empty.
	SandboxNamespace string
	// SandboxPort is the port image/package MCP servers listen on. Default 8080.
	SandboxPort int
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
	if o.SandboxNamespace == "" {
		o.SandboxNamespace = o.Namespace
	}
	if o.SandboxPort == 0 {
		o.SandboxPort = 8080
	}
	return o
}

// target is the resolved upstream for one MCP server's Backend.
type target struct {
	host     string
	port     int
	path     string
	protocol string // StreamableHTTP | SSE
}

// Route is the rendered routing for a single MCP server.
type Route struct {
	ServerName string // original registry name
	Name       string // sanitized resource/path name
	Path       string // "/mcp/<name>"
	Target     target
	Backend    map[string]interface{}
	HTTPRoute  map[string]interface{}
}

// Build renders Backend + HTTPRoute objects for every MCP server as a single
// multi-document YAML stream (sorted by name for stable output).
func Build(servers []v1alpha1.Object, opts Options) ([]byte, error) {
	routes, err := BuildRoutes(servers, opts)
	if err != nil {
		return nil, err
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

	for _, srv := range servers {
		name := serverName(srv)
		if name == "" {
			continue
		}
		san := adapters.SanitizeName(name)
		if seen[san] {
			continue
		}
		seen[san] = true

		tgt := targetFor(srv, san, opts)
		path := opts.PathPrefix + "/" + san
		labels := map[string]interface{}{
			"app.kubernetes.io/managed-by": "agentic-registry",
			"registry.agentic.dev/mcp":     san,
		}

		backend := map[string]interface{}{
			"apiVersion": backendAPIVersion,
			"kind":       backendKind,
			"metadata": map[string]interface{}{
				"name":      san,
				"namespace": opts.Namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"mcp": map[string]interface{}{
					"targets": []interface{}{
						map[string]interface{}{
							"name": san,
							"static": map[string]interface{}{
								"host":     tgt.host,
								"port":     tgt.port,
								"path":     tgt.path,
								"protocol": tgt.protocol,
							},
						},
					},
				},
			},
		}

		httpRoute := map[string]interface{}{
			"apiVersion": httpRouteAPIVersion,
			"kind":       "HTTPRoute",
			"metadata": map[string]interface{}{
				"name":      san,
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
						"matches": []interface{}{
							map[string]interface{}{
								"path": map[string]interface{}{
									"type":  "PathPrefix",
									"value": path,
								},
							},
						},
						"backendRefs": []interface{}{
							map[string]interface{}{
								"group": backendGroup,
								"kind":  backendKind,
								"name":  san,
							},
						},
					},
				},
			},
		}

		routes = append(routes, Route{
			ServerName: name,
			Name:       san,
			Path:       path,
			Target:     tgt,
			Backend:    backend,
			HTTPRoute:  httpRoute,
		})
	}

	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return routes, nil
}

// serverName prefers spec.name, falling back to metadata.name.
func serverName(srv v1alpha1.Object) string {
	if n, ok := srv.Spec["name"].(string); ok && n != "" {
		return n
	}
	return srv.Metadata.Name
}

// targetFor resolves where a server's Backend points. Remote servers
// (spec.remotes[].url) parse the URL into host/port/path; everything else
// (image/package servers) targets an in-cluster sandbox Service by convention.
func targetFor(srv v1alpha1.Object, san string, opts Options) target {
	if u, transport := firstRemote(srv); u != "" {
		return parseRemote(u, transport)
	}
	return target{
		host:     fmt.Sprintf("%s.%s.svc.cluster.local", san, opts.SandboxNamespace),
		port:     opts.SandboxPort,
		path:     "/mcp",
		protocol: "StreamableHTTP",
	}
}

// firstRemote returns the first remote endpoint URL and its transport hint.
func firstRemote(srv v1alpha1.Object) (string, string) {
	remotes, ok := srv.Spec["remotes"].([]interface{})
	if !ok {
		return "", ""
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
