package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"reflect"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func TestTenantClaimIsConfigurableForZitadel(t *testing.T) {
	claims := jwt.MapClaims{
		"tenant":                 "legacy",
		"urn:zitadel:iam:org:id": "zitadel-org-42",
	}
	if got := tenantFromClaims(claims, "urn:zitadel:iam:org:id"); got != "zitadel-org-42" {
		t.Fatalf("tenantFromClaims() = %q", got)
	}
	if got := tenantFromClaims(claims, ""); got != "legacy" {
		t.Fatalf("legacy tenant fallback = %q", got)
	}
}

func TestTenantFallsBackToTheUniqueRegistryRoleOrganization(t *testing.T) {
	claims := jwt.MapClaims{
		"urn:zitadel:iam:org:project:386930054896026901:roles": map[string]interface{}{
			"registry.reader":    map[string]interface{}{"customer-org": "acme.example"},
			"registry.publisher": map[string]interface{}{"customer-org": "acme.example"},
		},
	}
	got := tenantFromRoleClaim(claims["urn:zitadel:iam:org:project:386930054896026901:roles"])
	if got != "customer-org" {
		t.Fatalf("tenant from role claim = %q, want customer-org", got)
	}
	claims["urn:zitadel:iam:org:project:386930054896026901:roles"] = map[string]interface{}{
		"registry.reader": map[string]interface{}{"org-a": "a.example", "org-b": "b.example"},
	}
	if got := tenantFromRoleClaim(claims["urn:zitadel:iam:org:project:386930054896026901:roles"]); got != "" {
		t.Fatalf("ambiguous role organizations selected %q", got)
	}
}

func TestSignedMetadataSelectsOneOfSeveralGrantedOrganizations(t *testing.T) {
	roles := map[string]interface{}{
		"registry.reader": map[string]interface{}{"org-a": "a.example", "org-b": "b.example"},
	}
	metadata := map[string]interface{}{
		"registry_tenant": "b3JnLWI", // raw URL base64 for org-b
	}
	if got := tenantFromRegistryMetadata(metadata, roles); got != "org-b" {
		t.Fatalf("selected tenant = %q, want org-b", got)
	}
	metadata["registry_tenant"] = "b3JnLWM"
	if got := tenantFromRegistryMetadata(metadata, roles); got != "" {
		t.Fatalf("metadata selected ungranted tenant %q", got)
	}
}

func deployKeyDigest(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}

func TestBearerTokenAcceptsOAuthProxyForwardedAccessToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/v0/session", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("X-Forwarded-Access-Token", "verified-upstream-token")
	if got := bearerToken(req); got != "verified-upstream-token" {
		t.Fatalf("bearerToken()=%q", got)
	}
	req.Header.Set("Authorization", "Bearer browser-id-token")
	if got := bearerToken(req); got != "verified-upstream-token" {
		t.Fatalf("forwarded OAuth access token must take precedence over the browser ID token, got %q", got)
	}

	directReq, err := http.NewRequest(http.MethodGet, "/v0/session", nil)
	if err != nil {
		t.Fatalf("direct request: %v", err)
	}
	directReq.Header.Set("Authorization", "Bearer direct-token")
	if got := bearerToken(directReq); got != "direct-token" {
		t.Fatalf("direct Authorization token must remain supported, got %q", got)
	}
}

func obj(vis v1alpha1.Visibility, tenant string) v1alpha1.Object {
	return v1alpha1.Object{
		Kind:     v1alpha1.KindSkill,
		Metadata: v1alpha1.ObjectMeta{Name: "x", Visibility: vis, TenantID: tenant},
	}
}

func TestPublicReadableByAnyone(t *testing.T) {
	if !CanRead(Identity{}, obj(v1alpha1.VisibilityPublic, "acme")) {
		t.Fatal("public must be readable anonymously")
	}
}

func TestInternalRequiresSameTenant(t *testing.T) {
	o := obj(v1alpha1.VisibilityInternal, "acme")
	if CanRead(Identity{Authenticated: true, TenantID: "other"}, o) {
		t.Fatal("internal must not be readable cross-tenant")
	}
	if !CanRead(Identity{Authenticated: true, TenantID: "acme"}, o) {
		t.Fatal("internal must be readable by same-tenant member")
	}
}

func TestScopedTokenMustCarryReadScope(t *testing.T) {
	o := obj(v1alpha1.VisibilityInternal, "acme")
	// Token IS scoped but lacks registry:read => denied even for same tenant.
	noRead := Identity{Authenticated: true, TenantID: "acme", Scopes: []string{"openid"}}
	if CanRead(noRead, o) {
		t.Fatal("scoped token without registry:read must be denied")
	}
	withRead := Identity{Authenticated: true, TenantID: "acme", Scopes: []string{ScopeRead}}
	if !CanRead(withRead, o) {
		t.Fatal("scoped token with registry:read must be allowed")
	}
}

func TestWriteRequiresWriteScopeAndRole(t *testing.T) {
	o := obj(v1alpha1.VisibilityPrivate, "acme")
	// Has write role via group but token scoped without write => denied.
	roleNoScope := Identity{Authenticated: true, TenantID: "acme", Groups: []string{"acme:writer"}, Scopes: []string{ScopeRead}}
	if CanWrite(roleNoScope, o) {
		t.Fatal("scoped token without registry:write must not write")
	}
	// Write scope + write role => allowed.
	ok := Identity{Authenticated: true, TenantID: "acme", Groups: []string{"acme:writer"}, Scopes: []string{ScopeRead, ScopeWrite}}
	if !CanWrite(ok, o) {
		t.Fatal("registry:write + writer role must write")
	}
	// admin scope alone grants write (cumulative).
	adminScope := Identity{Authenticated: true, TenantID: "acme", Groups: []string{"acme:admin"}, Scopes: []string{ScopeAdmin}}
	if !CanWrite(adminScope, o) {
		t.Fatal("registry:admin scope must satisfy write")
	}
}

func TestUnscopedGroupTokenStillWorks(t *testing.T) {
	// Backward compat: no scopes at all (group-based / local admin).
	o := obj(v1alpha1.VisibilityPrivate, "acme")
	groupAdmin := Identity{Authenticated: true, TenantID: "acme", Groups: []string{"registry:admin"}}
	if !CanWrite(groupAdmin, o) {
		t.Fatal("unscoped group admin must retain write (backward compat)")
	}
}

func TestPublishAndDeleteScopesAreLeastPrivilege(t *testing.T) {
	o := obj(v1alpha1.VisibilityPrivate, "acme")
	publisher := Identity{
		Authenticated: true, TenantID: "acme", Groups: []string{"acme:writer"},
		Scopes: []string{ScopeRead, ScopePublish},
	}
	if !CanPublish(publisher, o) || CanDelete(publisher, o) {
		t.Fatal("registry:publish must publish without gaining delete")
	}
	deleter := Identity{
		Authenticated: true, TenantID: "acme", Groups: []string{"acme:writer"},
		Scopes: []string{ScopeRead, ScopeDelete},
	}
	if CanPublish(deleter, o) || !CanDelete(deleter, o) {
		t.Fatal("registry:delete must delete without gaining publish")
	}
	legacy := Identity{
		Authenticated: true, TenantID: "acme", Groups: []string{"acme:writer"},
		Scopes: []string{ScopeRead, ScopeWrite},
	}
	if !CanPublish(legacy, o) || !CanDelete(legacy, o) {
		t.Fatal("registry:write must remain a temporary alias for publish and delete")
	}
}

func TestPublisherMetadataRestrictsNamespaceKindAndDelete(t *testing.T) {
	publisher := Identity{
		Authenticated:     true,
		TenantID:          "tenant-42",
		Groups:            []string{"registry.publisher"},
		Scopes:            []string{ScopeRead, ScopePublish, ScopeDelete},
		AllowedNamespaces: []string{"agents-team"},
		AllowedKinds:      []string{"Agent", "Tool"},
	}
	allowed := v1alpha1.Object{
		Kind: v1alpha1.KindAgent,
		Metadata: v1alpha1.ObjectMeta{
			Name: "support", Namespace: "agents-team", TenantID: "tenant-42", Visibility: v1alpha1.VisibilityPrivate,
		},
	}
	if !CanPublish(publisher, allowed) {
		t.Fatal("publisher must publish an allowed kind in an allowed namespace")
	}
	if CanDelete(publisher, allowed) {
		t.Fatal("publisher without registry.deleter role must not delete")
	}
	wrongNamespace := allowed
	wrongNamespace.Metadata.Namespace = "finance"
	if CanPublish(publisher, wrongNamespace) {
		t.Fatal("publisher escaped namespace restriction")
	}
	wrongKind := allowed
	wrongKind.Kind = v1alpha1.KindPrompt
	if CanPublish(publisher, wrongKind) {
		t.Fatal("publisher escaped kind restriction")
	}
	publisher.Groups = append(publisher.Groups, "registry.deleter")
	if !CanDelete(publisher, allowed) {
		t.Fatal("explicit deleter role must permit deletion within restrictions")
	}
}

func TestZitadelMetadataClaimsDecodeRegistryRestrictions(t *testing.T) {
	claims := jwt.MapClaims{
		"urn:zitadel:iam:user:metadata": map[string]any{
			"registry_namespaces": "WyJhZ2VudHMtdGVhbSJd",
			"registry_kinds":      "WyJBZ2VudCIsIlRvb2wiXQ==",
		},
	}
	namespaces, kinds := registryRestrictions(claims)
	if !reflect.DeepEqual(namespaces, []string{"agents-team"}) || !reflect.DeepEqual(kinds, []string{"Agent", "Tool"}) {
		t.Fatalf("namespaces=%#v kinds=%#v", namespaces, kinds)
	}
}

// TestAnonymousDefaultIsAdmin asserts the default no-auth posture is UNCHANGED:
// an anonymous caller (empty AUTH_ANONYMOUS_ROLE) still gets registry:admin and
// can write. This is the behavior-neutrality guard for CODE-24.
func TestAnonymousDefaultIsAdmin(t *testing.T) {
	a, err := New(config.Config{AuthMode: "anonymous"})
	if err != nil {
		t.Fatalf("anonymous default must build: %v", err)
	}
	id := a.Identify(nil)
	// A private artifact in the anonymous tenant must be writable (current behavior).
	o := obj(v1alpha1.VisibilityPrivate, v1alpha1.DefaultNamespace)
	if !CanWrite(id, o) {
		t.Fatal("default anonymous role must remain admin (writable) for behavior neutrality")
	}
}

// TestAnonymousReadRoleFailsClosedOnWrite asserts the opt-in downgrade works:
// AUTH_ANONYMOUS_ROLE=read keeps public reads but makes writes fail closed.
func TestAnonymousReadRoleFailsClosedOnWrite(t *testing.T) {
	a, err := New(config.Config{AuthMode: "anonymous", AnonymousRole: "read"})
	if err != nil {
		t.Fatalf("anonymous read must build: %v", err)
	}
	id := a.Identify(nil)
	o := obj(v1alpha1.VisibilityPrivate, v1alpha1.DefaultNamespace)
	if CanWrite(id, o) {
		t.Fatal("downgraded anonymous (read) must not be able to write")
	}
	// Public reads still work.
	if !CanRead(id, obj(v1alpha1.VisibilityPublic, v1alpha1.DefaultNamespace)) {
		t.Fatal("downgraded anonymous must still read public artifacts")
	}
}

func TestAnonymousInvalidRoleRejected(t *testing.T) {
	if _, err := New(config.Config{AuthMode: "anonymous", AnonymousRole: "superuser"}); err == nil {
		t.Fatal("invalid AUTH_ANONYMOUS_ROLE must be rejected")
	}
}

// TestTrustedHeaderRequiresTrustedProxy asserts the mode refuses to start unless
// AUTH_TRUSTED_PROXY is set (it was previously dead config).
func TestTrustedHeaderRequiresTrustedProxy(t *testing.T) {
	if _, err := New(config.Config{AuthMode: "trusted-header"}); err == nil {
		t.Fatal("trusted-header must refuse to start without AUTH_TRUSTED_PROXY=true")
	}
	if _, err := New(config.Config{AuthMode: "trusted-header", TrustedProxy: true}); err != nil {
		t.Fatalf("trusted-header with AUTH_TRUSTED_PROXY=true must build: %v", err)
	}
}

func TestHumanAdminRequiresAllowlistedEmailAndZitadelRole(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:     "trusted-header",
		TrustedProxy: true,
		AdminEmails:  []string{"samyak.rout@gmail.com", "mahesh.sangawar@gmail.com"},
		AdminRole:    "agentregistry.admin",
	})
	if err != nil {
		t.Fatalf("trusted-header admin policy must build: %v", err)
	}

	for name, tc := range map[string]struct {
		email     string
		groups    string
		wantAdmin bool
	}{
		"allowlisted role holder":     {email: "Samyak.Rout@gmail.com", groups: "agentregistry.admin", wantAdmin: true},
		"allowlisted without role":    {email: "mahesh.sangawar@gmail.com", groups: "tesserix:writer", wantAdmin: false},
		"role holder not allowlisted": {email: "attacker@example.com", groups: "agentregistry.admin,registry:admin,tesserix:writer", wantAdmin: false},
	} {
		t.Run(name, func(t *testing.T) {
			req, reqErr := http.NewRequest(http.MethodGet, "/v0/session", nil)
			if reqErr != nil {
				t.Fatalf("request: %v", reqErr)
			}
			req.Header.Set("X-Forwarded-User", "zitadel-user")
			req.Header.Set("X-Forwarded-Email", tc.email)
			req.Header.Set("X-Forwarded-Groups", tc.groups)

			id := a.Identify(req)
			if got := CanAdmin(id); got != tc.wantAdmin {
				t.Fatalf("CanAdmin() = %v, want %v; identity=%#v", got, tc.wantAdmin, id)
			}
			if got := CanWrite(id, obj(v1alpha1.VisibilityPrivate, "tesserix")); got != tc.wantAdmin {
				t.Fatalf("CanWrite() = %v, want %v; identity=%#v", got, tc.wantAdmin, id)
			}
		})
	}
}

func TestHumanAdminPolicyRequiresBothConfigurationValues(t *testing.T) {
	for name, cfg := range map[string]config.Config{
		"emails without role": {
			AuthMode: "anonymous", AdminEmails: []string{"samyak.rout@gmail.com"},
		},
		"role without emails": {
			AuthMode: "anonymous", AdminRole: "agentregistry.admin",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("incomplete human-admin policy must be rejected")
			}
		})
	}
}

func TestHumanAdminPolicyRejectsAnonymousAuthMode(t *testing.T) {
	_, err := New(config.Config{
		AuthMode:    "anonymous",
		AdminEmails: []string{"samyak.rout@gmail.com"},
		AdminRole:   "agentregistry.admin",
	})
	if err == nil {
		t.Fatal("human-admin policy must not create a false security boundary in anonymous mode")
	}
}

func TestHumanAdminPolicyDoesNotReplaceTenantDeployKeys(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:          "trusted-header",
		TrustedProxy:      true,
		AdminEmails:       []string{"samyak.rout@gmail.com"},
		AdminRole:         "agentregistry.admin",
		DeployKeySHA256:   []string{deployKeyDigest("github-publisher")},
		DeployKeyTenantID: "kora",
	})
	if err != nil {
		t.Fatalf("combined human and machine policy must build: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "/v0/apply", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer github-publisher")

	id := a.Identify(req)
	if !CanWrite(id, obj(v1alpha1.VisibilityPrivate, "kora")) || CanAdmin(id) {
		t.Fatalf("deploy key must remain a tenant writer, got %#v", id)
	}
}

func TestZitadelProjectRoleClaimUsesRoleNames(t *testing.T) {
	claim := map[string]interface{}{
		"agentregistry.admin": map[string]interface{}{"org-id": "tesserix"},
		"agentregistry.read":  map[string]interface{}{"org-id": "tesserix"},
	}
	want := []string{"agentregistry.admin", "agentregistry.read"}
	if got := extractGroups(claim); !reflect.DeepEqual(got, want) {
		t.Fatalf("extractGroups() = %#v, want %#v", got, want)
	}
}

func TestDeployKeyAuthenticatesTenantScopedWriter(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:          "anonymous",
		AnonymousRole:     "read",
		DeployKeySHA256:   []string{deployKeyDigest("current-key")},
		DeployKeyTenantID: "kora",
	})
	if err != nil {
		t.Fatalf("deploy-key auth must build: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "/v0/apply", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer current-key")

	id := a.Identify(req)
	if !id.Authenticated || id.TenantID != "kora" || id.Subject != "deploy-key:kora" {
		t.Fatalf("unexpected deploy-key identity: %#v", id)
	}
	if !CanWrite(id, obj(v1alpha1.VisibilityPrivate, "kora")) {
		t.Fatal("deploy key must write within its tenant")
	}
	if CanWrite(id, obj(v1alpha1.VisibilityPrivate, "other")) {
		t.Fatal("deploy key must not write outside its tenant")
	}
}

func TestDeployKeyAuthenticatesDedicatedHeader(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:          "anonymous",
		AnonymousRole:     "read",
		DeployKeySHA256:   []string{deployKeyDigest("current-key")},
		DeployKeyTenantID: "kora",
	})
	if err != nil {
		t.Fatalf("deploy-key auth must build: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "/v0/apply", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("X-Agentic-Registry-Deploy-Key", "current-key")

	id := a.Identify(req)
	if !id.Authenticated || id.TenantID != "kora" || id.Subject != "deploy-key:kora" {
		t.Fatalf("unexpected deploy-key identity: %#v", id)
	}
	if !CanWrite(id, obj(v1alpha1.VisibilityPrivate, "kora")) {
		t.Fatal("deploy key must write within its tenant")
	}
	if CanWrite(id, obj(v1alpha1.VisibilityPrivate, "other")) {
		t.Fatal("deploy key must not write outside its tenant")
	}
}

func TestDeployKeySupportsRotationOverlap(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:          "anonymous",
		AnonymousRole:     "read",
		DeployKeySHA256:   []string{deployKeyDigest("new-key"), deployKeyDigest("old-key")},
		DeployKeyTenantID: "kora",
	})
	if err != nil {
		t.Fatalf("deploy-key auth must build: %v", err)
	}
	for _, key := range []string{"new-key", "old-key"} {
		req, reqErr := http.NewRequest(http.MethodPost, "/v0/apply", nil)
		if reqErr != nil {
			t.Fatalf("request: %v", reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		if id := a.Identify(req); !id.Authenticated || id.TenantID != "kora" {
			t.Fatalf("overlap key %q was not accepted", key)
		}
	}
}

func TestDeployKeysSelectTheMatchingTenant(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:      "anonymous",
		AnonymousRole: "read",
		DeployKeys: []config.DeployKey{
			{TenantID: "devai", SHA256: deployKeyDigest("devai-key")},
			{TenantID: "kora", SHA256: deployKeyDigest("kora-key")},
		},
	})
	if err != nil {
		t.Fatalf("multi-tenant deploy-key auth must build: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "/v0/apply", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer kora-key")

	id := a.Identify(req)
	if id.TenantID != "kora" || !CanWrite(id, obj(v1alpha1.VisibilityPrivate, "kora")) {
		t.Fatalf("matching key must select only its tenant, got %#v", id)
	}
	if CanWrite(id, obj(v1alpha1.VisibilityPrivate, "devai")) {
		t.Fatal("kora deploy key must not write to the devai tenant")
	}
}

func TestInvalidDeployKeyDelegatesToConfiguredAuthenticator(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:          "anonymous",
		AnonymousRole:     "read",
		DeployKeySHA256:   []string{deployKeyDigest("valid-key")},
		DeployKeyTenantID: "kora",
	})
	if err != nil {
		t.Fatalf("deploy-key auth must build: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "/v0/skills", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer invalid-key")

	id := a.Identify(req)
	if id.Subject != "local" || id.TenantID != v1alpha1.DefaultNamespace {
		t.Fatalf("invalid deploy key must delegate, got %#v", id)
	}
	if CanWrite(id, obj(v1alpha1.VisibilityPrivate, v1alpha1.DefaultNamespace)) {
		t.Fatal("delegated read-only identity must not gain write access")
	}
}

func TestInvalidDedicatedDeployKeyDelegatesToConfiguredAuthenticator(t *testing.T) {
	a, err := New(config.Config{
		AuthMode:          "trusted-header",
		TrustedProxy:      true,
		DeployKeySHA256:   []string{deployKeyDigest("valid-key")},
		DeployKeyTenantID: "kora",
	})
	if err != nil {
		t.Fatalf("deploy-key auth must build: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "/v0/skills", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set(DeployKeyHeader, "invalid-key")
	req.Header.Set("X-Forwarded-User", "zitadel-user")
	req.Header.Set("X-Forwarded-Tenant", "kora")

	id := a.Identify(req)
	if id.Subject != "zitadel-user" || id.TenantID != "kora" {
		t.Fatalf("invalid dedicated deploy key must delegate, got %#v", id)
	}
}

func TestDeployKeyConfigurationRejectsMissingTenantAndMalformedDigest(t *testing.T) {
	for name, cfg := range map[string]config.Config{
		"missing tenant": {
			AuthMode:        "anonymous",
			DeployKeySHA256: []string{deployKeyDigest("key")},
		},
		"malformed digest": {
			AuthMode:          "anonymous",
			DeployKeySHA256:   []string{"not-a-sha256-digest"},
			DeployKeyTenantID: "kora",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("invalid deploy-key configuration must be rejected")
			}
		})
	}
}
