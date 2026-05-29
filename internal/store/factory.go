package store

import (
	"context"
	"fmt"

	"github.com/tesserix/agentic-registry/internal/config"
)

// New constructs the configured Store. "memory" needs no dependencies;
// "postgres" connects using cfg.DatabaseURL (injected from a secret manager).
func New(ctx context.Context, cfg config.Config) (Store, error) {
	switch cfg.StoreBackend {
	case "", "memory":
		return NewMemory(), nil
	case "postgres":
		if cfg.DatabaseURL == "" {
			return nil, fmt.Errorf("store: DATABASE_URL required for postgres backend")
		}
		return NewPostgres(ctx, cfg.DatabaseURL)
	default:
		return nil, fmt.Errorf("store: unknown backend %q", cfg.StoreBackend)
	}
}
