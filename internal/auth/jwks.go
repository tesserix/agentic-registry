package auth

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/tesserix/agentic-registry/internal/config"
)

// jwksAuth verifies a Bearer JWT against a remote JWKS endpoint. It validates
// signature, issuer, audience, and expiry, then maps standard + configured
// claims onto an Identity. It never mints tokens — verification only.
type jwksAuth struct {
	kf          keyfunc.Keyfunc
	issuer      string
	audience    string
	groupsClaim string
}

func newJWKSAuthenticator(cfg config.Config) (Authenticator, error) {
	if cfg.AuthJWKSURL == "" {
		return nil, fmt.Errorf("auth: AUTH_JWKS_URL required for jwks mode")
	}
	kf, err := keyfunc.NewDefault([]string{cfg.AuthJWKSURL})
	if err != nil {
		return nil, fmt.Errorf("auth: load JWKS %q: %w", cfg.AuthJWKSURL, err)
	}
	return jwksAuth{
		kf:          kf,
		issuer:      cfg.AuthIssuer,
		audience:    cfg.AuthAudience,
		groupsClaim: cfg.GroupsClaim,
	}, nil
}

func (j jwksAuth) Identify(r *http.Request) Identity {
	raw := bearerToken(r)
	if raw == "" {
		return Identity{}
	}
	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384"})}
	if j.issuer != "" {
		opts = append(opts, jwt.WithIssuer(j.issuer))
	}
	if j.audience != "" {
		opts = append(opts, jwt.WithAudience(j.audience))
	}
	tok, err := jwt.Parse(raw, j.kf.Keyfunc, opts...)
	if err != nil || !tok.Valid {
		return Identity{} // invalid token => anonymous (handlers enforce access)
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return Identity{}
	}
	id := Identity{Authenticated: true}
	id.Subject, _ = claims["sub"].(string)
	id.Email, _ = claims["email"].(string)
	if t, ok := claims["tenant"].(string); ok {
		id.TenantID = t
	} else if t, ok := claims["tid"].(string); ok {
		id.TenantID = t
	}
	id.Groups = extractGroups(claims[j.groupsClaim])
	// OAuth 2.1 scopes: "scope" is a space-delimited string (RFC 6749);
	// some IdPs use "scp" as a string or array.
	id.Scopes = extractScopes(claims["scope"], claims["scp"])
	return id
}

func extractScopes(scope, scp interface{}) []string {
	if s, ok := scope.(string); ok && s != "" {
		return strings.Fields(s)
	}
	switch v := scp.(type) {
	case string:
		return strings.Fields(v)
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// oauth2-proxy forwards the authenticated browser's access token in this
	// header when it is the trusted edge. The token still undergoes the same
	// signature, issuer, audience, algorithm, and expiry validation as a direct
	// Authorization bearer token; this header is transport, not trust.
	return strings.TrimSpace(r.Header.Get("X-Forwarded-Access-Token"))
}

func extractGroups(v interface{}) []string {
	switch vv := v.(type) {
	case map[string]interface{}:
		out := make([]string, 0, len(vv))
		for role := range vv {
			out = append(out, role)
		}
		sort.Strings(out)
		return out
	case []interface{}:
		out := make([]string, 0, len(vv))
		for _, g := range vv {
			if s, ok := g.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return vv
	case string:
		return splitTrim(vv)
	default:
		return nil
	}
}
