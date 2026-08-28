package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/probe"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const probeBodyLimit = 1 << 18

// observation is what a capability prober saw. Conditions are deliberately not
// accepted from the caller: the registry derives them by comparing the
// observation against the declaration, so a prober cannot assert readiness for
// a server it never reached.
type observation struct {
	Reachable       bool     `json:"reachable"`
	Tools           []string `json:"tools"`
	ProtocolVersion string   `json:"protocolVersion"`
	Error           string   `json:"error"`
}

// v0PutMCPServerStatus records one probe result against an MCP server's status.
func (s *Server) v0PutMCPServerStatus(w http.ResponseWriter, r *http.Request) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	if kind != v1alpha1.KindMCPServer {
		writeErr(w, http.StatusBadRequest, "probe status is only defined for MCP servers")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, probeBodyLimit+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read request body")
		return
	}
	if len(body) > probeBodyLimit {
		writeErr(w, http.StatusRequestEntityTooLarge, "probe result too large")
		return
	}
	var obs observation
	if err := json.Unmarshal(body, &obs); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	ns, name := s.namespace(r), chi.URLParam(r, "name")
	obj, err := s.store.Get(r.Context(), kind, ns, name, "")
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !auth.CanPublish(identity(r), obj) {
		writeErr(w, http.StatusForbidden, "insufficient permission")
		return
	}

	status := probe.StatusFor(obj.Spec, probe.Observation{
		Reachable:       obs.Reachable,
		Tools:           obs.Tools,
		ProtocolVersion: obs.ProtocolVersion,
		Error:           obs.Error,
		ProbedAt:        time.Now().UTC(),
	})
	if err := s.store.MergeStatus(r.Context(), kind, ns, name, obj.Metadata.Tag, status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "record probe result")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
