// Package api wires the HTTP surface: the devai-compatible /v0/* API, the
// MCP-Registry-compatible /v0.1/* API, the prompt render endpoint, and the
// built-in MCP discovery server. It is a thin layer over the Store; all access
// decisions are made via the auth package.
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/config"
	"github.com/tesserix/agentic-registry/internal/mcp"
	"github.com/tesserix/agentic-registry/internal/signing"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	store  store.Store
	cfg    config.Config
	signer *signing.Signer
	reqs   atomic.Int64
}

// New builds the chi router with all routes mounted.
func New(st store.Store, authn auth.Authenticator, cfg config.Config) http.Handler {
	s := &Server{store: st, cfg: cfg, signer: signing.New(cfg.SigningKey, cfg.SigningDev)}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(s.cors)
	r.Use(s.count)
	r.Use(auth.Middleware(authn))

	// Liveness / observability.
	r.Get("/healthz", s.health)
	r.Get("/metrics", s.metrics)

	// devai-compatible catalog API.
	s.mountV0(r)
	// MCP-Registry-compatible interop API.
	s.mountV01(r)
	// Built-in MCP discovery server (catalog-as-MCP, never a proxy). It shares
	// the signer so Agent Cards rendered over MCP carry the same registry
	// attestation as the HTTP path — a verifying consumer accepts either.
	r.Handle("/mcp", mcp.NewDiscoveryServer(st, s.signer))

	// Marketplace SPA (optional) — one image serves API + UI.
	if cfg.WebDir != "" {
		r.NotFound(spaHandler(cfg.WebDir))
	}

	return r
}

// spaHandler serves static files from dir, falling back to index.html for
// unknown paths so client-side routes (e.g. /skills/foo) resolve.
func spaHandler(dir string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return func(w http.ResponseWriter, r *http.Request) {
		// API paths never fall through here (they're routed above), so any
		// 404 reaching this is a UI route or static asset.
		if p := filepath.Join(dir, filepath.Clean(r.URL.Path)); fileExists(p) {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// withIdentity attaches the derived identity (arn/digest/ref) and, when signing
// is enabled, the registry's Ed25519 attestation over the digest. Applied to
// every object served to clients.
func (s *Server) withIdentity(o v1alpha1.Object) v1alpha1.Object {
	o = o.WithIdentity()
	if s.signer.Enabled() {
		o.Metadata.Signature = s.signer.Sign(o.Metadata.Digest)
		o.Metadata.SignedBy = s.signer.KeyID()
	}
	return o
}

// signingKey publishes the registry's public signing key so consumers can
// verify digest attestations.
func (s *Server) signingKey(w http.ResponseWriter, _ *http.Request) {
	if !s.signer.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":   true,
		"algorithm": "ed25519",
		"keyId":     s.signer.KeyID(),
		"publicKey": s.signer.PublicKeyB64(),
		"encoding":  "base64",
		"signs":     "digest", // signature is over the "sha256:<hex>" digest string
	})
}

// cors applies a configurable CORS policy. CORS_ORIGINS="*" echoes any Origin
// back (handy for local dev); otherwise an Origin is reflected only on an EXACT
// allow-list match. The previous substring check (strings.Contains) was too
// loose — e.g. "tesserix.app" would have matched an allowed
// "https://aregistry.tesserix.app". Credentials are never allowed, so a
// cross-origin caller can read only what an unauthenticated request returns.
func (s *Server) cors(next http.Handler) http.Handler {
	allowAll := false
	allowed := make(map[string]struct{}, len(s.cfg.CORSOrigins))
	for _, o := range s.cfg.CORSOrigins {
		switch o = strings.TrimSpace(o); o {
		case "":
		case "*":
			allowAll = true
		default:
			allowed[o] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Vary", "Origin")
			if _, ok := allowed[origin]; allowAll || ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) count(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.reqs.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	body := map[string]interface{}{
		"status":   "ok",
		"version":  config.Version,
		"platform": s.cfg.StoreBackend,
	}
	if err := s.store.Health(r.Context()); err != nil {
		body["status"] = "degraded"
		body["error"] = err.Error()
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// metrics serves a minimal Prometheus text exposition. We avoid the full
// client_golang dependency for v1; richer metrics land alongside the Postgres
// store.
func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(
		"# HELP agentic_registry_requests_total Total HTTP requests served.\n" +
			"# TYPE agentic_registry_requests_total counter\n" +
			"agentic_registry_requests_total " + itoa(s.reqs.Load()) + "\n",
	))
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
