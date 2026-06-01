package seed

import (
	"context"
	"testing"

	"github.com/tesserix/agentic-registry/internal/store"
)

// The embedded starter catalog must apply cleanly into an empty store. This
// also guards against accidental cross-kind name collisions in the curated
// data: the store now enforces namespace-scoped name uniqueness across kinds,
// so a duplicate name between (say) a Skill and an MCPServer would fail here.
func TestSeedCatalogAppliesWithoutCollision(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	n, err := IfEmpty(ctx, m)
	if err != nil {
		t.Fatalf("seeding the embedded catalog failed: %v", err)
	}
	if n == 0 {
		t.Fatal("expected the starter catalog to apply at least one artifact")
	}

	// Idempotent: a second run is a no-op (store already populated).
	again, err := IfEmpty(ctx, m)
	if err != nil {
		t.Fatalf("second seed run errored: %v", err)
	}
	if again != 0 {
		t.Fatalf("seeding should be skipped on a populated store, applied %d", again)
	}
}
