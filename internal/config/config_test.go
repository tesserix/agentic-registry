package config

import (
	"reflect"
	"testing"
)

func TestLoadParsesDeployKeyRotationDigests(t *testing.T) {
	t.Setenv("AUTH_DEPLOY_KEY_SHA256", "first, second ,,")
	t.Setenv("AUTH_DEPLOY_KEY_TENANT", " kora ")

	cfg := Load()
	if !reflect.DeepEqual(cfg.DeployKeySHA256, []string{"first", "second"}) {
		t.Fatalf("unexpected deploy-key digests: %#v", cfg.DeployKeySHA256)
	}
	if cfg.DeployKeyTenantID != "kora" {
		t.Fatalf("unexpected deploy-key tenant: %q", cfg.DeployKeyTenantID)
	}
}

func TestLoadParsesTenantScopedDeployKeys(t *testing.T) {
	t.Setenv("AUTH_DEPLOY_KEYS", "devai=aaa,kora=bbb,kora=ccc,,")

	cfg := Load()
	want := []DeployKey{
		{TenantID: "devai", SHA256: "aaa"},
		{TenantID: "kora", SHA256: "bbb"},
		{TenantID: "kora", SHA256: "ccc"},
	}
	if !reflect.DeepEqual(cfg.DeployKeys, want) {
		t.Fatalf("unexpected tenant-scoped deploy keys: %#v", cfg.DeployKeys)
	}
}
