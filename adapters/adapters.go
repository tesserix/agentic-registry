// Package adapters turns resolved registry artifacts into the
// runtime-specific config other control planes consume:
//
//   - adapters/agentgateway — MCPServer[] → AgentgatewayBackend + HTTPRoute
//     (so a registered MCP server is reachable at /mcp/<name> through the
//     agentgateway data plane).
//   - adapters/kagent      — a resolved Agent → a kagent.dev Agent CR plus a
//     RemoteMCPServer per MCP dependency (for long-lived, controller-managed
//     agents).
//
// Every adapter is a PURE function over pkg/api/v1alpha1 types — no I/O, no
// dependency on internal/api — so they're trivially golden-file tested and can
// be called from the CLI (`agentic export …`) or an in-cluster sync Job alike.
//
// CRD-version caveat: the emitted apiVersions (kagent.dev/v1alpha2,
// gateway.networking.k8s.io/v1) are pinned as constants in each sub-package.
// Verify them against the CRDs actually installed in the target cluster before
// wiring these into a server-side-apply sync Job — control-plane/CRD skew is
// the top integration risk here.
package adapters

import (
	"regexp"
	"strings"
)

// nonDNS matches any run of characters that are not valid in an RFC-1123
// label, so registry names like "io.github.acme/files" collapse to a single
// dash run before trimming.
var nonDNS = regexp.MustCompile(`[^a-z0-9]+`)

// SanitizeName coerces an arbitrary registry name into an RFC-1123 label
// (lower-case alphanumerics + single dashes, ≤63 chars) suitable for both a
// Kubernetes resource name and a URL path segment. It is deterministic so the
// same MCP server always maps to the same backend/route/path.
//
//	"io.github.acme/files" → "io-github-acme-files"
//	"My Tool (v2)"         → "my-tool-v2"
func SanitizeName(name string) string {
	s := nonDNS.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	if s == "" {
		return "unnamed"
	}
	return s
}
