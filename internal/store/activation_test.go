package store

import (
	"context"
	"testing"
	"time"

	"github.com/tesserix/agentic-registry/internal/activation"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const activationArtifactDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestObserveActivationBindsObservationToStoredMCPVersion(t *testing.T) {
	memory := NewMemory()
	memory.now = func() time.Time { return time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	if _, _, err := memory.Apply(ctx, activationMCPServer()); err != nil {
		t.Fatal(err)
	}
	stored, err := memory.Get(ctx, v1alpha1.KindMCPServer, v1alpha1.DefaultNamespace, "orders", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	status, err := memory.ObserveActivation(ctx, v1alpha1.DefaultNamespace, "orders", "1.0.0", activation.Observation{
		Type:               activation.DeploymentReady,
		Status:             activation.ConditionTrue,
		Actor:              activation.GatewayReconciler,
		Reason:             "Accepted",
		ObservedGeneration: 1,
		RegistryDigest:     stored.Digest(),
		ArtifactDigest:     activationArtifactDigest,
		RequestID:          "gateway-activation-1",
		ObservedAt:         time.Date(2026, time.August, 31, 0, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Generation != 1 || status.Phase != activation.Deployed {
		t.Fatalf("unexpected activation status: %#v", status)
	}
	if status.RegistryDigest == "" || status.ArtifactDigest != activationArtifactDigest {
		t.Fatalf("activation identity was not derived from the stored version: %#v", status)
	}
}

func activationMCPServer() v1alpha1.Object {
	return v1alpha1.Object{
		Kind: v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{
			Name: "orders",
			Tag:  "1.0.0",
		},
		Spec: map[string]any{
			"name": "orders",
			"x-tesserix": map[string]any{
				"publication": map[string]any{
					"artifact": map[string]any{"digest": activationArtifactDigest},
				},
			},
		},
	}
}
