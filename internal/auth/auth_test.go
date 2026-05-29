package auth

import (
	"testing"

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
