package gatewaysync

import (
	"testing"
	"time"
)

func TestLoadConfigValidatesRequiredEnvironmentAndDefaults(t *testing.T) {
	t.Setenv("REGISTRY_URL", "http://agentregistry.agentregistry-system.svc.cluster.local:12121/v0/export/agentgateway")
	t.Setenv("REGISTRY_TOKEN_FILE", "/var/run/secrets/registry/API_KEY")
	t.Setenv("TARGET_NAMESPACE", "agentgateway-system")
	t.Setenv("POD_NAME", "agentgateway-route-sync-0")
	t.Setenv("POD_NAMESPACE", "agentgateway-system")
	t.Setenv("RECONCILIATION_MODE", "shadow")
	t.Setenv("MIN_RESOURCES", "27")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeShadow || config.PollInterval != 30*time.Second || config.SafetyInterval != 5*time.Minute {
		t.Fatalf("config: got %#v", config)
	}
	if config.MinResources != 27 || config.MaxBodyBytes != 5<<20 || !config.Prune {
		t.Fatalf("safety config: got %#v", config)
	}
	if config.LeaseName != "agentgateway-route-sync" || config.FieldManager != "agentgateway-registry-sync" {
		t.Fatalf("ownership config: got %#v", config)
	}
}

func TestLoadConfigRejectsUnsafeResourceFloor(t *testing.T) {
	t.Setenv("REGISTRY_URL", "http://registry/export")
	t.Setenv("REGISTRY_TOKEN_FILE", "/token")
	t.Setenv("TARGET_NAMESPACE", "agentgateway-system")
	t.Setenv("POD_NAME", "sync-0")
	t.Setenv("POD_NAMESPACE", "agentgateway-system")
	t.Setenv("MIN_RESOURCES", "0")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("zero resource floor was accepted")
	}
}
