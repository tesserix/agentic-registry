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

	// Multitenancy: when false, the default namespace/tenant is hidden on the
	// wire so the registry "feels" single-tenant until deliberately enabled.
	MultiTenant bool

	// CORS allowed origins for the marketplace UI (comma-separated).
	CORSOrigins []string

	// PublicBaseURL is advertised in server.json and discovery responses.
	PublicBaseURL string

	// WebDir, when set, serves the built marketplace SPA (with history-API
	// fallback) from this directory so one image ships both API and UI.
	WebDir string
}

func Load() Config {
	c := Config{
		Addr:          env("ADDR", ":8080"),
		StoreBackend:  env("STORE_BACKEND", "memory"),
		DatabaseURL:   env("DATABASE_URL", ""),
		AuthMode:      env("AUTH_MODE", "anonymous"),
		AuthJWKSURL:   env("AUTH_JWKS_URL", ""),
		AuthIssuer:    env("AUTH_ISSUER", ""),
		AuthAudience:  env("AUTH_AUDIENCE", ""),
		GroupsClaim:   env("AUTH_GROUPS_CLAIM", "groups"),
		TrustedProxy:  env("AUTH_TRUSTED_PROXY", "false") == "true",
		MultiTenant:   env("MULTI_TENANT", "false") == "true",
		PublicBaseURL: env("PUBLIC_BASE_URL", "http://localhost:8080"),
		WebDir:        env("WEB_DIR", ""),
	}
	if c.DatabaseURL != "" && c.StoreBackend == "memory" {
		c.StoreBackend = "postgres"
	}
	if origins := env("CORS_ORIGINS", "*"); origins != "" {
		c.CORSOrigins = strings.Split(origins, ",")
	}
	return c
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
