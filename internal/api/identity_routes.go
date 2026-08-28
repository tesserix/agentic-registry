package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/auth"
	identityplane "github.com/tesserix/agentic-registry/internal/identity"
)

const maxIdentityRequestBytes = 64 << 10

var (
	tenantSlugPattern     = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
	credentialIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
	credentialNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
)

func (s *Server) v0Onboard(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if !id.Authenticated || strings.TrimSpace(id.Subject) == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if id.TenantID != "" {
		writeErr(w, http.StatusConflict, "identity already belongs to a tenant")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 200 {
		writeErr(w, http.StatusBadRequest, "Idempotency-Key must be between 8 and 200 characters")
		return
	}
	var input identityplane.OnboardingRequest
	if err := decodeIdentityJSON(r, &input); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Slug = strings.TrimSpace(input.Slug)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if !tenantSlugPattern.MatchString(input.Slug) {
		writeErr(w, http.StatusBadRequest, "slug must be a lowercase DNS label")
		return
	}
	if len(input.DisplayName) < 1 || len(input.DisplayName) > 100 {
		writeErr(w, http.StatusBadRequest, "display_name must be between 1 and 100 characters")
		return
	}
	tenant, err := s.identity.Onboard(r.Context(), auth.AccessToken(r), idempotencyKey, input)
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, tenant)
}

func (s *Server) v0ListAPICredentials(w http.ResponseWriter, r *http.Request) {
	id, token, ok := credentialActor(w, r)
	if !ok {
		return
	}
	credentials, err := s.identity.ListCredentials(r.Context(), token, id.TenantID)
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": credentials})
}

func (s *Server) v0CreateAPICredential(w http.ResponseWriter, r *http.Request) {
	id, token, ok := credentialActor(w, r)
	if !ok {
		return
	}
	idempotencyKey, ok := mutationID(w, r)
	if !ok {
		return
	}
	var input identityplane.CreateCredentialRequest
	if err := decodeIdentityJSON(r, &input); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if !credentialNamePattern.MatchString(input.Name) {
		writeErr(w, http.StatusBadRequest, "name must be 1-64 letters, numbers, spaces, dots, underscores, or hyphens")
		return
	}
	if input.LifetimeDays == 0 {
		input.LifetimeDays = 30
	}
	if input.LifetimeDays < 1 || input.LifetimeDays > 90 {
		writeErr(w, http.StatusBadRequest, "lifetime_days must be between 1 and 90")
		return
	}
	if len(input.Scopes) == 0 {
		input.Scopes = []string{auth.ScopeRead, auth.ScopePublish}
	}
	if err := validateCredentialScopes(input.Scopes); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateCredentialRestrictions(input.Namespaces, input.Kinds); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	credential, err := s.identity.CreateCredential(r.Context(), token, id.TenantID, idempotencyKey, input)
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusCreated, credential)
}

func validateCredentialRestrictions(namespaces, kinds []string) error {
	if len(namespaces) == 0 || len(namespaces) > 20 {
		return errors.New("namespaces must contain between 1 and 20 entries")
	}
	for _, namespace := range namespaces {
		if !tenantSlugPattern.MatchString(namespace) {
			return fmt.Errorf("namespace %q is not a lowercase DNS label", namespace)
		}
	}
	allowedKinds := map[string]bool{
		"Agent": true, "MCPServer": true, "Tool": true, "Skill": true,
		"Prompt": true, "Workflow": true, "Blueprint": true, "Dataset": true, "EvalSuite": true,
	}
	if len(kinds) == 0 || len(kinds) > len(allowedKinds) {
		return errors.New("kinds must contain at least one supported artifact kind")
	}
	for _, kind := range kinds {
		if !allowedKinds[kind] {
			return fmt.Errorf("kind %q is not supported", kind)
		}
	}
	return nil
}

func (s *Server) v0RotateAPICredential(w http.ResponseWriter, r *http.Request) {
	id, token, ok := credentialActor(w, r)
	if !ok {
		return
	}
	idempotencyKey, ok := mutationID(w, r)
	if !ok {
		return
	}
	credentialID := chi.URLParam(r, "credentialID")
	if !credentialIDPattern.MatchString(credentialID) {
		writeErr(w, http.StatusBadRequest, "invalid credential ID")
		return
	}
	credential, err := s.identity.RotateCredential(r.Context(), token, id.TenantID, credentialID, idempotencyKey)
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, credential)
}

func (s *Server) v0RevokeAPICredential(w http.ResponseWriter, r *http.Request) {
	id, token, ok := credentialActor(w, r)
	if !ok {
		return
	}
	credentialID := chi.URLParam(r, "credentialID")
	if !credentialIDPattern.MatchString(credentialID) {
		writeErr(w, http.StatusBadRequest, "invalid credential ID")
		return
	}
	if err := s.identity.RevokeCredential(r.Context(), token, id.TenantID, credentialID); err != nil {
		writeIdentityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func credentialActor(w http.ResponseWriter, r *http.Request) (auth.Identity, string, bool) {
	id := identity(r)
	if !id.Authenticated || strings.TrimSpace(id.TenantID) == "" {
		writeErr(w, http.StatusUnauthorized, "authenticated tenant identity required")
		return auth.Identity{}, "", false
	}
	token := auth.AccessToken(r)
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "delegated actor access token required")
		return auth.Identity{}, "", false
	}
	return id, token, true
}

func mutationID(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 8 || len(key) > 200 {
		writeErr(w, http.StatusBadRequest, "Idempotency-Key must be between 8 and 200 characters")
		return "", false
	}
	return key, true
}

func validateCredentialScopes(scopes []string) error {
	allowed := map[string]bool{auth.ScopeRead: true, auth.ScopePublish: true, auth.ScopeDelete: true}
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if !allowed[scope] {
			return fmt.Errorf("scope %q is not allowed", scope)
		}
		if seen[scope] {
			return fmt.Errorf("scope %q is duplicated", scope)
		}
		seen[scope] = true
	}
	return nil
}

func decodeIdentityJSON(r *http.Request, output any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxIdentityRequestBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func writeIdentityError(w http.ResponseWriter, err error) {
	var upstream *identityplane.APIError
	if errors.As(err, &upstream) {
		status := upstream.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		code := upstream.Code
		if code == "" {
			code = publicErrorCode(status)
		}
		writeCodedErr(w, status, code, upstream.Message)
		return
	}
	if errors.Is(err, identityplane.ErrNotConfigured) {
		writeCodedErr(w, http.StatusServiceUnavailable, "identity_unavailable", "identity control plane is not configured")
		return
	}
	writeCodedErr(w, http.StatusBadGateway, "identity_unavailable", "identity control plane unavailable")
}
