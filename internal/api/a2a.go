package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/a2a"
	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// v0AgentCard serves the A2A Agent Card for the latest version of an agent.
func (s *Server) v0AgentCard(w http.ResponseWriter, r *http.Request) {
	s.agentCard(w, r, "")
}

// v0AgentCardTag serves the A2A Agent Card for a pinned agent version.
func (s *Server) v0AgentCardTag(w http.ResponseWriter, r *http.Request) {
	s.agentCard(w, r, chi.URLParam(r, "tag"))
}

// agentCard renders and serves an A2A-compliant Agent Card. The registry stays
// off the A2A request path: it returns the descriptor (capabilities + the
// agent's own service `url`); the consumer then talks A2A directly to that url.
// Skill references in the agent spec resolve to real registry Skill objects in
// the SAME namespace, honouring the caller's read visibility.
func (s *Server) agentCard(w http.ResponseWriter, r *http.Request, tag string) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	if kind != v1alpha1.KindAgent {
		writeErr(w, http.StatusBadRequest, "agent card is only defined for agents; use /v0/agents/{name}/card")
		return
	}
	name := chi.URLParam(r, "name")
	// Resolve the agent's namespace across the namespaces the caller can read
	// (mirrors the /v0 get path); previously this defaulted to DefaultNamespace,
	// so an agent published into any other namespace 404'd on its card and on
	// .well-known/agent-card.json even though it was listed in the catalog.
	ns := s.resolveNamespace(r, kind, name)

	agent, err := s.store.Get(r.Context(), kind, ns, name, tag)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := identity(r)
	if !auth.CanRead(id, agent) {
		// Don't reveal artifacts the caller can't read.
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	// Stamp arn/digest/ref (+ signature when signing is on) so the card's
	// provenance extension is complete and verifiable.
	agent = s.withIdentity(agent)

	// Resolve skill references against the registry's own Skill catalog, scoped
	// to the agent's namespace and the caller's visibility. A missing or
	// unreadable skill is silently skipped — the card never leaks or breaks.
	resolve := func(skillName string) (v1alpha1.Object, bool) {
		sk, err := s.store.Get(r.Context(), v1alpha1.KindSkill, ns, skillName, "")
		if err != nil || !auth.CanRead(id, sk) {
			return v1alpha1.Object{}, false
		}
		return sk, true
	}

	card, err := a2a.Card(agent, resolve, a2a.Options{RegistryURL: publicBaseURL(r)})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, card)
}

// publicBaseURL reconstructs the registry's externally visible base URL from
// the request, honouring a reverse proxy's forwarded scheme/host headers.
func publicBaseURL(r *http.Request) string {
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}
