package auth

import (
	"encoding/base64"
	"encoding/json"
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
	tenantClaim string
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
		tenantClaim: cfg.TenantClaim,
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
	id.TenantID = tenantFromClaims(claims, j.tenantClaim)
	id.Groups = extractGroups(claims[j.groupsClaim])
	if id.TenantID == "" {
		id.TenantID = tenantFromRegistryMetadata(
			claims["urn:zitadel:iam:user:metadata"], claims[j.groupsClaim],
		)
	}
	if id.TenantID == "" {
		id.TenantID = tenantFromRoleClaim(claims[j.groupsClaim])
	}
	// OAuth 2.1 scopes: "scope" is a space-delimited string (RFC 6749);
	// some IdPs use "scp" as a string or array.
	id.Scopes = extractScopes(claims["scope"], claims["scp"])
	id.AllowedNamespaces, id.AllowedKinds = registryRestrictions(claims)
	return id
}

func tenantFromRoleClaim(value interface{}) string {
	organizations := registryRoleOrganizations(value)
	if len(organizations) != 1 {
		return ""
	}
	for orgID := range organizations {
		return orgID
	}
	return ""
}

func registryRoleOrganizations(value interface{}) map[string]bool {
	organizations := map[string]bool{}
	roles, ok := value.(map[string]interface{})
	if !ok {
		return organizations
	}
	for _, role := range []string{"registry.reader", "registry.publisher", "registry.deleter"} {
		raw, exists := roles[role]
		if !exists {
			continue
		}
		switch grants := raw.(type) {
		case map[string]interface{}:
			for orgID := range grants {
				if strings.TrimSpace(orgID) != "" {
					organizations[orgID] = true
				}
			}
		case map[string]string:
			for orgID := range grants {
				if strings.TrimSpace(orgID) != "" {
					organizations[orgID] = true
				}
			}
		}
	}
	return organizations
}

func tenantFromRegistryMetadata(metadataValue, roleValue interface{}) string {
	metadata, ok := metadataValue.(map[string]interface{})
	if !ok {
		return ""
	}
	raw, ok := decodeMetadataValue(metadata["registry_tenant"])
	if !ok {
		return ""
	}
	candidate := strings.TrimSpace(string(raw))
	if candidate == "" || len(candidate) > 200 || !registryRoleOrganizations(roleValue)[candidate] {
		return ""
	}
	return candidate
}

func registryRestrictions(claims jwt.MapClaims) ([]string, []string) {
	metadata, ok := claims["urn:zitadel:iam:user:metadata"].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	return decodeMetadataList(metadata["registry_namespaces"]), decodeMetadataList(metadata["registry_kinds"])
}

func decodeMetadataList(value interface{}) []string {
	raw, ok := decodeMetadataValue(value)
	if !ok || len(raw) > 4096 {
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil || len(values) > 100 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && len(value) <= 200 {
			out = append(out, value)
		}
	}
	return out
}

func decodeMetadataValue(value interface{}) ([]byte, bool) {
	encoded, ok := value.(string)
	if !ok || encoded == "" {
		return nil, false
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		raw, err := encoding.DecodeString(encoded)
		if err == nil {
			return raw, true
		}
	}
	return nil, false
}

func tenantFromClaims(claims jwt.MapClaims, configured string) string {
	for _, claim := range []string{strings.TrimSpace(configured), "tenant", "tid", "urn:zitadel:iam:org:id"} {
		if claim == "" {
			continue
		}
		if tenant, ok := claims[claim].(string); ok && strings.TrimSpace(tenant) != "" {
			return strings.TrimSpace(tenant)
		}
	}
	return ""
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
	// oauth2-proxy forwards both a browser ID token in Authorization and the
	// resource-audience access token in this header. The access token carries
	// the project roles the Registry authorizes; both still undergo full JWT
	// verification below.
	if raw := strings.TrimSpace(r.Header.Get("X-Forwarded-Access-Token")); raw != "" {
		return raw
	}
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// AccessToken returns the actor token already verified by the configured
// authenticator. It exists so a narrow identity-control-plane adapter can
// delegate the actor's authority without exposing the token in responses.
func AccessToken(r *http.Request) string { return bearerToken(r) }

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
