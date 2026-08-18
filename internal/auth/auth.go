// Package auth establishes the caller's identity and the access decisions that
// flow from it. The registry is NOT an auth server: it issues no tokens and
// proxies no traffic. It only *consumes* an identity — either a pre-validated
// one forwarded by a trusted gateway (X-Forwarded-* headers) or a Bearer JWT
// verified against a configured JWKS endpoint.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
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
	var base Authenticator
	var err error
	switch cfg.AuthMode {
	case "jwks":
		base, err = newJWKSAuthenticator(cfg)
	case "trusted-header":
		base, err = newTrustedHeaderAuth(cfg)
	default: // "anonymous"
		base, err = newAnonymousAuth(cfg)
	}
	if err != nil {
		return nil, err
	}
	return withDeployKeys(base, cfg)
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

// anonymousAuth is the no-auth mode: NO auth provider is configured, so every
// request is treated as the same local identity.
//
// The role granted to that identity is configurable (AUTH_ANONYMOUS_ROLE):
//   - "admin" (DEFAULT): grants the registry:admin group so publish/delete work
//     without a token — matches solo aregistry's local daemon and the in-cluster
//     bootstrap/seed write path (which arrives over a mesh-trusted SA).
//   - "read": grants no write groups and no read scope-elevation, so anonymous
//     callers can read public artifacts but every write fails closed. Recommended
//     hardening for shared deployments; the actual flip happens in chart values,
//     paired with the mesh DENY policy that confines writes to trusted SAs.
//
// The default is "admin" precisely to preserve existing runtime behavior.
type anonymousAuth struct {
	role Role
}

func newAnonymousAuth(cfg config.Config) (Authenticator, error) {
	r, err := parseAnonymousRole(cfg.AnonymousRole)
	if err != nil {
		return nil, err
	}
	return anonymousAuth{role: r}, nil
}

// parseAnonymousRole maps the configured AUTH_ANONYMOUS_ROLE to a Role. An empty
// value defaults to admin (preserving current behavior).
func parseAnonymousRole(s string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "admin":
		return RoleAdmin, nil
	case "read", "reader", "readonly", "read-only":
		return RoleRead, nil
	case "write", "writer":
		return RoleWrite, nil
	default:
		return RoleNone, fmt.Errorf("invalid AUTH_ANONYMOUS_ROLE %q (want admin|write|read)", s)
	}
}

func (a anonymousAuth) Identify(*http.Request) Identity {
	id := Identity{
		Subject:       "local",
		TenantID:      v1alpha1.DefaultNamespace,
		Authenticated: true,
	}
	// Admin keeps the registry:admin group (full access, current behavior). A
	// downgraded anonymous identity carries no write-granting group, so RoleFor
	// resolves to at most read on the local tenant and CanWrite fails closed.
	switch a.role {
	case RoleAdmin:
		id.Groups = []string{"registry:admin"}
	case RoleWrite:
		id.Groups = []string{v1alpha1.DefaultNamespace + ":writer"}
	default: // RoleRead
		id.Groups = []string{v1alpha1.DefaultNamespace + ":reader"}
	}
	return id
}

// ---- trusted header (behind a gateway) -------------------------------------

// trustedHeaderAuth trusts identity headers set by an upstream OIDC gateway.
// SECURITY: only mount this when the registry is reachable solely via that
// gateway (enforced by mTLS / NetworkPolicy). Otherwise a client could forge
// the headers.
type trustedHeaderAuth struct{ groupsClaim string }

// newTrustedHeaderAuth builds the trusted-header authenticator, refusing to
// start unless AUTH_TRUSTED_PROXY is explicitly set. Trusting X-Forwarded-*
// identity headers is only safe when the registry is reachable solely via the
// gateway that sets them; AUTH_TRUSTED_PROXY=true is the operator's affirmation
// that this network containment is in place. Previously this flag was dead
// config, so the mode trusted forgeable headers with no opt-in.
func newTrustedHeaderAuth(cfg config.Config) (Authenticator, error) {
	if !cfg.TrustedProxy {
		return nil, fmt.Errorf(
			"AUTH_MODE=trusted-header requires AUTH_TRUSTED_PROXY=true: only enable it when the " +
				"registry is reachable solely via the identity-setting gateway (mTLS/NetworkPolicy), " +
				"otherwise X-Forwarded-* identity headers can be forged")
	}
	return trustedHeaderAuth{groupsClaim: cfg.GroupsClaim}, nil
}

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

type deployKeyAuth struct {
	next Authenticator
	keys []deployKey
}

type deployKey struct {
	digest [sha256.Size]byte
	tenant string
}

func withDeployKeys(next Authenticator, cfg config.Config) (Authenticator, error) {
	configured := append([]config.DeployKey(nil), cfg.DeployKeys...)
	for _, digest := range cfg.DeployKeySHA256 {
		configured = append(configured, config.DeployKey{
			TenantID: cfg.DeployKeyTenantID,
			SHA256:   digest,
		})
	}
	if len(configured) == 0 {
		return next, nil
	}
	keys := make([]deployKey, 0, len(configured))
	for _, item := range configured {
		tenant := strings.TrimSpace(item.TenantID)
		if tenant == "" {
			return nil, fmt.Errorf("deploy-key tenant is required")
		}
		raw, err := hex.DecodeString(strings.TrimSpace(item.SHA256))
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("deploy-key digests must be 64-character hex SHA-256 values")
		}
		var digest [sha256.Size]byte
		copy(digest[:], raw)
		keys = append(keys, deployKey{digest: digest, tenant: tenant})
	}
	return deployKeyAuth{next: next, keys: keys}, nil
}

func (a deployKeyAuth) Identify(r *http.Request) Identity {
	token := bearerToken(r)
	candidate := sha256.Sum256([]byte(token))
	matchedTenant := ""
	for _, key := range a.keys {
		if subtle.ConstantTimeCompare(candidate[:], key.digest[:]) == 1 {
			matchedTenant = key.tenant
		}
	}
	if token != "" && matchedTenant != "" {
		return Identity{
			Subject:       "deploy-key:" + matchedTenant,
			TenantID:      matchedTenant,
			Groups:        []string{matchedTenant + ":writer"},
			Scopes:        []string{ScopeRead, ScopeWrite},
			Authenticated: true,
		}
	}
	return a.next.Identify(r)
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
