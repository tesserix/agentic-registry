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
		m := NewMemory()
		m.immutableTags = cfg.ImmutableTags
		m.autoVersion = cfg.AutoVersion
		return m, nil
	case "postgres":
		if cfg.DatabaseURL == "" {
			return nil, fmt.Errorf("store: DATABASE_URL required for postgres backend")
		}
		p, err := NewPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		p.immutableTags = cfg.ImmutableTags
		p.autoVersion = cfg.AutoVersion
		return p, nil
	default:
		return nil, fmt.Errorf("store: unknown backend %q", cfg.StoreBackend)
	}
}
