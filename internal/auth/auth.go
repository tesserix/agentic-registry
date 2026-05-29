// Package auth establishes the caller's identity and the access decisions that
// flow from it. The registry is NOT an auth server: it issues no tokens and
// proxies no traffic. It only *consumes* an identity — either a pre-validated
// one forwarded by a trusted gateway (X-Forwarded-* headers) or a Bearer JWT
// verified against a configured JWKS endpoint.
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// Role is a cumulative permission level: read < write < admin.
type Role int

const (
	RoleNone Role = iota
	RoleRead
	RoleWrite
	RoleAdmin
)

// Identity is the authenticated caller. Anonymous callers have Authenticated
// false and may still read public artifacts.
type Identity struct {
	Subject       string
	Email         string
	TenantID      string
	Groups        []string
	Scopes        []string // OAuth 2.1 scopes, e.g. registry:read registry:write
	Authenticated bool
}

// Scope constants (OAuth 2.1 Resource Server model). A machine-to-machine
// token from a tool's client-credentials grant carries these.
const (
	ScopeRead  = "registry:read"
	ScopeWrite = "registry:write"
	ScopeAdmin = "registry:admin"
)

func (id Identity) hasScope(s string) bool {
	for _, sc := range id.Scopes {
		if sc == s || sc == ScopeAdmin {
			return true
		}
	}
	return false
}

// scopeAllows enforces scopes ONLY when the token carries them. Group/role
// based identities (and the local anonymous-admin) have no scopes and are
// governed by RoleFor alone — this keeps backward compatibility while making
// scoped M2M tokens strict.
func (id Identity) scopeAllows(required string) bool {
	if len(id.Scopes) == 0 {
		return true
	}
	return id.hasScope(required)
}

type ctxKey struct{}

// WithIdentity returns a context carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext extracts the identity (anonymous zero value if absent).
func FromContext(ctx context.Context) Identity {
	if id, ok := ctx.Value(ctxKey{}).(Identity); ok {
		return id
	}
	return Identity{}
}

// Authenticator resolves an HTTP request to an Identity. Implementations never
// reject the request themselves — an unauthenticated caller simply gets an
// anonymous Identity, and authorization is decided later by CanRead/CanWrite.
type Authenticator interface {
	Identify(r *http.Request) Identity
}

// New builds an Authenticator from config.
func New(cfg config.Config) (Authenticator, error) {
	switch cfg.AuthMode {
	case "jwks":
		return newJWKSAuthenticator(cfg)
	case "trusted-header":
		return trustedHeaderAuth{groupsClaim: cfg.GroupsClaim}, nil
	default: // "anonymous"
		return anonymousAuth{}, nil
	}
}

// Middleware attaches the resolved Identity to the request context. It does not
// block anonymous requests; downstream handlers enforce visibility/RBAC.
func Middleware(a Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := a.Identify(r)
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}

// ---- anonymous (local/dev) -------------------------------------------------

// anonymousAuth is the local/dev mode: NO auth is configured, so the registry
// runs single-user with full access (matching solo aregistry's local daemon).
// It grants the registry:admin group so publish/delete work without a token.
// Production deployments use the "jwks" or "trusted-header" modes instead.
type anonymousAuth struct{}

func (anonymousAuth) Identify(*http.Request) Identity {
	return Identity{
		Subject:       "local",
		TenantID:      v1alpha1.DefaultNamespace,
		Groups:        []string{"registry:admin"},
		Authenticated: true,
	}
}

// ---- trusted header (behind a gateway) -------------------------------------

// trustedHeaderAuth trusts identity headers set by an upstream OIDC gateway.
// SECURITY: only mount this when the registry is reachable solely via that
// gateway (enforced by mTLS / NetworkPolicy). Otherwise a client could forge
// the headers.
type trustedHeaderAuth struct{ groupsClaim string }

func (t trustedHeaderAuth) Identify(r *http.Request) Identity {
	sub := r.Header.Get("X-Forwarded-User")
	if sub == "" {
		return Identity{}
	}
	id := Identity{
		Subject:       sub,
		Email:         r.Header.Get("X-Forwarded-Email"),
		TenantID:      r.Header.Get("X-Forwarded-Tenant"),
		Authenticated: true,
	}
	if groups := r.Header.Get("X-Forwarded-Groups"); groups != "" {
		id.Groups = splitTrim(groups)
	}
	return id
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- access decisions ------------------------------------------------------

// CanRead implements the security boundary:
//
//	public OR (internal AND same-tenant) OR an RBAC grant.
//
// The label selector is applied by the store AFTER this filter; it is never a
// security boundary.
func CanRead(id Identity, o v1alpha1.Object) bool {
	if o.Metadata.Visibility == v1alpha1.VisibilityPublic {
		return true // anonymous, no scope required
	}
	// Non-public reads require the read scope (when the token is scoped) AND a
	// qualifying tenant/role grant.
	if !id.scopeAllows(ScopeRead) {
		return false
	}
	switch o.Metadata.Visibility {
	case v1alpha1.VisibilityInternal:
		return id.Authenticated && id.TenantID != "" && id.TenantID == o.Metadata.TenantID
	default: // private
		return RoleFor(id, o) >= RoleRead
	}
}

// CanWrite requires the write scope (for scoped tokens) plus write/admin on the
// artifact's scope, and enforces tenant ownership at publish time.
func CanWrite(id Identity, o v1alpha1.Object) bool {
	if !id.Authenticated {
		return false
	}
	if !id.scopeAllows(ScopeWrite) {
		return false
	}
	return RoleFor(id, o) >= RoleWrite
}

// RoleFor derives the caller's effective (cumulative) role on an artifact from
// its group memberships. Groups are mapped by convention:
//
//	registry:admin                      -> admin everywhere
//	<tenant>:admin|writer|reader        -> role within that tenant
//
// A real deployment binds these to OIDC groups / SSO. This keeps the mapping
// transparent and out of the data path.
func RoleFor(id Identity, o v1alpha1.Object) Role {
	if !id.Authenticated {
		return RoleNone
	}
	best := RoleNone
	tenant := o.Metadata.TenantID
	for _, g := range id.Groups {
		switch g {
		case "registry:admin":
			return RoleAdmin
		case tenant + ":admin":
			best = max(best, RoleAdmin)
		case tenant + ":writer":
			best = max(best, RoleWrite)
		case tenant + ":reader":
			best = max(best, RoleRead)
		}
	}
	// A member of the owning tenant always has at least read on its artifacts.
	if best < RoleRead && id.TenantID != "" && id.TenantID == tenant {
		best = RoleRead
	}
	return best
}

func max(a, b Role) Role {
	if a > b {
		return a
	}
	return b
}
