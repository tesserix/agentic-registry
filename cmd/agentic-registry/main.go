// Command agentic-registry runs the registry HTTP server. It wires config →
// store → authenticator → router and serves until interrupted. Secrets (the
// database URL) arrive via the environment, populated by the deployment from a
// secret manager — never read from disk or compiled in.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tesserix/agentic-registry/internal/api"
	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/internal/seed"
	"github.com/tesserix/agentic-registry/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		// Emits source.{file,line,function} on every record, which is what lets
		// an error in the observability UI be traced back to the exact line that
		// produced it. Paired with -trimpath in the Dockerfile the path is
		// module-relative and maps onto a file in this repo at the commit the
		// running image was built from.
		AddSource: true,
	}))
	cfg := config.Load()

	st, err := store.New(context.Background(), cfg)
	if err != nil {
		log.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()

	// Populate a fresh store with the embedded starter catalog (local/dev only).
	if cfg.SeedExamples {
		if n, err := seed.IfEmpty(context.Background(), st); err != nil {
			log.Warn("seed failed", "err", err)
		} else if n > 0 {
			log.Info("seeded starter catalog", "artifacts", n)
		}
	}

	authn, err := auth.New(cfg)
	if err != nil {
		log.Error("auth init failed", "err", err)
		os.Exit(1)
	}

	handler := api.New(st, authn, cfg)
	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// ReadHeaderTimeout/ReadTimeout bound how long a client may take to send
		// the request (Slowloris defense); WriteTimeout bounds the response; and
		// IdleTimeout reaps idle keep-alive connections. The MCP discovery and
		// render endpoints are all fast, so generous-but-finite values are safe.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Info("agentic-registry listening",
			"addr", cfg.Addr, "store", cfg.StoreBackend, "auth", cfg.AuthMode, "version", config.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
}
