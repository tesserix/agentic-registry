// Package config loads server configuration from the environment. No secret
// values are read from disk or baked into the binary; the database URL and any
// credentials arrive via environment variables that the deployment populates
// from an external secret manager.
package config

import (
	"os"
	"strings"
)

// Version is set at build time via -ldflags.
var Version = "dev"

type DeployKey struct {
	TenantID string
	SHA256   string
}

type Config struct {
	// Addr is the HTTP listen address.
	Addr string
	// StoreBackend is "memory" (default, zero-dependency) or "postgres".
	StoreBackend string
	// DatabaseURL is the Postgres DSN, injected from a secret manager. Empty
	// when StoreBackend is memory.
	DatabaseURL string

	// Auth. The registry validates identity but never issues tokens.
	//   AuthMode: "anonymous" (dev), "trusted-header" (behind a gateway), "jwks".
	AuthMode     string
	AuthJWKSURL  string // JWKS endpoint for verifying Bearer JWTs
	AuthIssuer   string // expected "iss" claim
	AuthAudience string // expected "aud" claim (optional)
	GroupsClaim  string // JWT claim carrying group/role membership
	TrustedProxy bool   // trust X-Forwarded-* identity headers
	// AdminEmails and AdminRole form a two-factor human administration policy:
	// a human must have both an exact allowlisted email and the configured IdP
	// project role. Machine identities and tenant deploy keys remain governed by
	// scopes and tenant roles instead.
	AdminEmails []string
	AdminRole   string
	// AnonymousRole is the effective role granted to anonymous (no-auth-mode)
	// callers: "admin" (current/default behavior — full read+write, what local
	// dev and the in-cluster bootstrap SA rely on) or "read" (read-only;
	// recommended hardening once writes arrive only via a mesh-trusted identity).
	// DEFAULT is "admin" to preserve existing behavior; the flip to "read" is a
	// deliberate chart-values change made later, paired with the mesh DENY policy.
	AnonymousRole string
	// DeployKeySHA256 contains one or more hex-encoded SHA-256 digests for
	// overlap-safe machine credential rotation. The raw keys are never stored by
	// the registry. DeployKeyTenantID scopes matching credentials to one tenant.
	DeployKeySHA256   []string
	DeployKeyTenantID string
	// DeployKeys supports independent tenant credentials and overlap rotation.
	// AUTH_DEPLOY_KEYS uses tenant=digest entries separated by commas.
	DeployKeys []DeployKey

	// CORS allowed origins for the marketplace UI (comma-separated).
	CORSOrigins []string

	// ToolSourceURLs are upstream tool catalogs for the pull-through resolver
	// (docs/agentic/MCP-HUB.md §5.5). Each entry is "name=urlTemplate" (or just a
	// urlTemplate; name defaults to its host); the template must contain "{name}".
	// When a declared tool is absent locally the resolver tries these in order and
	// pull-through-caches the first hit. Empty = registry-only (no upstream).
	ToolSourceURLs []string

	// PublicBaseURL is advertised in server.json and discovery responses.
	PublicBaseURL string

	// WebDir, when set, serves the built marketplace SPA (with history-API
	// fallback) from this directory so one image ships both API and UI.
	WebDir string

	// SeedExamples applies the embedded starter catalog on first start when the
	// store is empty. Intended for local/dev/demo; leave false in production.
	SeedExamples bool

	// ImmutableTags rejects re-publishing an existing version tag with different
	// content (the floating "latest" tag stays mutable). Artifact-repository
	// semantics: a released version is permanent. Default true.
	ImmutableTags bool

	// AutoVersion assigns the next semver tag (v0.0.1, v0.0.2, …) when a publish
	// omits an explicit version (tag empty or "latest"), so every release is a
	// unique, immutable version rather than an overwritten "latest". Default true.
	AutoVersion bool

	// SigningKey is a base64 Ed25519 private key (seed or full key) the registry
	// uses to sign artifact digests (provenance attestation). Empty disables
	// signing unless SigningDev is set.
	SigningKey string
	// SigningDev generates an ephemeral signing key when no SigningKey is set —
	// for local/dev so the feature works out of the box.
	SigningDev bool
}

func Load() Config {
	c := Config{
		Addr:         env("ADDR", ":8080"),
		StoreBackend: env("STORE_BACKEND", "memory"),
		DatabaseURL:  env("DATABASE_URL", ""),
		AuthMode:     env("AUTH_MODE", "anonymous"),
		AuthJWKSURL:  env("AUTH_JWKS_URL", ""),
		AuthIssuer:   env("AUTH_ISSUER", ""),
		AuthAudience: env("AUTH_AUDIENCE", ""),
		GroupsClaim:  env("AUTH_GROUPS_CLAIM", "groups"),
		TrustedProxy: env("AUTH_TRUSTED_PROXY", "false") == "true",
		AdminRole:    strings.TrimSpace(env("AUTH_ADMIN_ROLE", "")),
		// Default "admin" preserves current behavior (see field doc); set to
		// "read" in chart values to downgrade anonymous callers to read-only.
		AnonymousRole:     env("AUTH_ANONYMOUS_ROLE", "admin"),
		DeployKeyTenantID: strings.TrimSpace(env("AUTH_DEPLOY_KEY_TENANT", "")),
		PublicBaseURL:     env("PUBLIC_BASE_URL", "http://localhost:8080"),
		WebDir:            env("WEB_DIR", ""),
		SeedExamples:      env("SEED_EXAMPLES", "false") == "true",
		ImmutableTags:     env("IMMUTABLE_TAGS", "true") == "true",
		AutoVersion:       env("AUTO_VERSION", "true") == "true",
		SigningKey:        env("SIGNING_PRIVATE_KEY", ""),
		SigningDev:        env("SIGNING_DEV", "false") == "true",
	}
	if emails := env("AUTH_ADMIN_EMAILS", ""); emails != "" {
		for _, email := range strings.Split(emails, ",") {
			if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
				c.AdminEmails = append(c.AdminEmails, email)
			}
		}
	}
	if digests := env("AUTH_DEPLOY_KEY_SHA256", ""); digests != "" {
		for _, digest := range strings.Split(digests, ",") {
			if digest = strings.TrimSpace(digest); digest != "" {
				c.DeployKeySHA256 = append(c.DeployKeySHA256, digest)
			}
		}
	}
	if entries := env("AUTH_DEPLOY_KEYS", ""); entries != "" {
		for _, entry := range strings.Split(entries, ",") {
			if entry = strings.TrimSpace(entry); entry == "" {
				continue
			}
			tenant, digest, ok := strings.Cut(entry, "=")
			if !ok {
				c.DeployKeys = append(c.DeployKeys, DeployKey{SHA256: strings.TrimSpace(entry)})
				continue
			}
			c.DeployKeys = append(c.DeployKeys, DeployKey{
				TenantID: strings.TrimSpace(tenant),
				SHA256:   strings.TrimSpace(digest),
			})
		}
	}
	if c.DatabaseURL != "" && c.StoreBackend == "memory" {
		c.StoreBackend = "postgres"
	}
	if origins := env("CORS_ORIGINS", "*"); origins != "" {
		c.CORSOrigins = strings.Split(origins, ",")
	}
	if srcs := env("TOOL_SOURCE_URLS", ""); srcs != "" {
		for _, s := range strings.Split(srcs, ",") {
			if s = strings.TrimSpace(s); s != "" {
				c.ToolSourceURLs = append(c.ToolSourceURLs, s)
			}
		}
	}
	return c
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
