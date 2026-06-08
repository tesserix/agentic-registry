package auth

import (
	"testing"

	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

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
