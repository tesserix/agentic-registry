package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/activation"
	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const activationConditionBodyLimit = 1 << 16

type activationConditionInput struct {
	Type               activation.ConditionType   `json:"type"`
	Status             activation.ConditionStatus `json:"status"`
	Reason             string                     `json:"reason"`
	ObservedGeneration int64                      `json:"observedGeneration"`
	RegistryDigest     string                     `json:"registryDigest"`
	ArtifactDigest     string                     `json:"artifactDigest"`
}

func (s *Server) v0PutActivationCondition(w http.ResponseWriter, r *http.Request) {
	input, err := decodeActivationCondition(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid activation condition")
		return
	}

	namespace, name, tag := s.namespace(r), chi.URLParam(r, "name"), chi.URLParam(r, "tag")
	object, err := s.store.Get(r.Context(), v1alpha1.KindMCPServer, namespace, name, tag)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load MCP server")
		return
	}
	if !auth.CanRead(identity(r), object) && !auth.CanWrite(identity(r), object) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	actor := activation.ActorForCondition(input.Type)
	if actor == "" || !auth.CanObserveActivation(identity(r), object, string(actor)) {
		writeErr(w, http.StatusForbidden, "insufficient permission")
		return
	}
	status, err := s.store.ObserveActivation(r.Context(), namespace, name, tag, activation.Observation{
		Type: input.Type, Status: input.Status, Actor: actor, Reason: input.Reason,
		ObservedGeneration: input.ObservedGeneration, RegistryDigest: input.RegistryDigest,
		ArtifactDigest: input.ArtifactDigest, RequestID: w.Header().Get("X-Request-ID"),
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "activation condition does not match the current MCP server")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func decodeActivationCondition(body io.Reader) (activationConditionInput, error) {
	var input activationConditionInput
	decoder := json.NewDecoder(io.LimitReader(body, activationConditionBodyLimit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return input, errors.New("multiple JSON values")
	}
	return input, nil
}
