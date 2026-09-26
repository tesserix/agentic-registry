package activation

import (
	"encoding/json"
	"testing"
	"time"
)

const (
	registryDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	artifactDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestObserveRejectsStaleIdentityWithoutChangingStatus(t *testing.T) {
	now := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	status := New("mcpservers/devai/orders@1.0.0", registryDigest, artifactDigest, Published, 3, now)

	_, err := status.Observe(Observation{
		Type:               DeploymentReady,
		Status:             ConditionTrue,
		Actor:              GatewayReconciler,
		Reason:             "Accepted",
		ObservedGeneration: 2,
		RegistryDigest:     registryDigest,
		ArtifactDigest:     artifactDigest,
		RequestID:          "activation-1",
		ObservedAt:         now.Add(time.Second),
	})
	if err == nil {
		t.Fatal("expected stale generation rejection")
	}
	if len(status.Conditions) != 1 || status.Phase != PublishedPhase {
		t.Fatalf("stale observation changed status: %#v", status)
	}
}

func TestObservePreservesTransitionTimeForSameCondition(t *testing.T) {
	now := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	status := New("mcpservers/devai/orders@1.0.0", registryDigest, artifactDigest, Published, 3, now)
	first, err := status.Observe(Observation{
		Type: DeploymentReady, Status: ConditionTrue, Actor: GatewayReconciler,
		Reason: "Accepted", ObservedGeneration: 3, RegistryDigest: registryDigest,
		ArtifactDigest: artifactDigest, RequestID: "activation-1", ObservedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := first.Observe(Observation{
		Type: DeploymentReady, Status: ConditionTrue, Actor: GatewayReconciler,
		Reason: "Accepted", ObservedGeneration: 3, RegistryDigest: registryDigest,
		ArtifactDigest: artifactDigest, RequestID: "activation-2", ObservedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Conditions[1].LastTransitionTime != first.Conditions[1].LastTransitionTime {
		t.Fatalf("same condition changed transition time: %s -> %s", first.Conditions[1].LastTransitionTime, second.Conditions[1].LastTransitionTime)
	}
}

func TestObserveDerivesActiveOnlyAfterEveryReadinessCondition(t *testing.T) {
	now := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	status := New("mcpservers/devai/orders@1.0.0", registryDigest, artifactDigest, Published, 3, now)
	for index, observation := range []Observation{
		{Type: DeploymentReady, Status: ConditionTrue, Actor: GatewayReconciler, Reason: "Accepted"},
		{Type: ProbeReady, Status: ConditionTrue, Actor: ProtocolProber, Reason: "Succeeded"},
		{Type: Healthy, Status: ConditionTrue, Actor: ProtocolProber, Reason: "Succeeded"},
	} {
		var err error
		status, err = status.Observe(withIdentity(observation, now.Add(time.Duration(index+1)*time.Second)))
		if err != nil {
			t.Fatal(err)
		}
	}
	if status.Phase != Probed {
		t.Fatalf("phase before route readiness = %q, want %q", status.Phase, Probed)
	}

	active, err := status.Observe(withIdentity(Observation{
		Type: RouteReady, Status: ConditionTrue, Actor: GatewayReconciler, Reason: "Accepted",
	}, now.Add(4*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if active.Phase != Active {
		t.Fatalf("phase = %q, want %q", active.Phase, Active)
	}
	if active.ActiveAt == nil || !active.ActiveAt.Equal(now.Add(4*time.Second)) {
		t.Fatalf("activeAt = %v, want %s", active.ActiveAt, now.Add(4*time.Second))
	}
}

func TestStatusMarshalsAsActivationStatusV1alpha1(t *testing.T) {
	now := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	status := New("mcpservers/devai/orders@1.0.0", registryDigest, artifactDigest, Published, 3, now)
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"schemaVersion", "ref", "registryDigest", "artifactDigest", "generation", "desiredState", "phase", "publishedAt", "activeAt", "observedAt", "conditions"} {
		if _, ok := document[field]; !ok {
			t.Fatalf("activation document omits %q: %s", field, encoded)
		}
	}
	if document["schemaVersion"] != "v1alpha1" {
		t.Fatalf("schemaVersion = %#v", document["schemaVersion"])
	}
}

func withIdentity(observation Observation, observedAt time.Time) Observation {
	observation.ObservedGeneration = 3
	observation.RegistryDigest = registryDigest
	observation.ArtifactDigest = artifactDigest
	observation.RequestID = "activation-observation"
	observation.ObservedAt = observedAt
	return observation
}
