package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

const (
	agentgatewayRegistryNamespace = "devai"
	agentgatewayTargetNamespace   = "agentgateway-system"
	agentgatewayBodyLimit         = 1 << 20
)

var agentgatewayKinds = map[string]string{
	"backends": "AgentgatewayBackend",
	"policies": "AgentgatewayPolicy",
	"routes":   "HTTPRoute",
}

var agentgatewayKindSlugs = map[string]string{
	"AgentgatewayBackend": "backend",
	"AgentgatewayPolicy":  "policy",
	"HTTPRoute":           "route",
}

func (s *Server) requireAgentgatewayAdmin(w http.ResponseWriter, r *http.Request) bool {
	if auth.CanAdmin(identity(r)) {
		return true
	}
	writeErr(w, http.StatusForbidden, "AgentGateway administrator permission required")
	return false
}

func (s *Server) v0AgentgatewayList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentgatewayAdmin(w, r) {
		return
	}
	res, err := s.store.List(r.Context(), store.ListOptions{
		Kind:       v1alpha1.KindGatewayResource,
		Namespace:  agentgatewayRegistryNamespace,
		LatestOnly: true,
		Limit:      1000,
		CanRead:    readPredicate(r),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list AgentGateway resources")
		return
	}
	items := make([]map[string]interface{}, 0, len(res.Items))
	for _, artifact := range res.Items {
		resource, err := gatewayResourceForOutput(artifact.Spec)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "decode AgentGateway desired state")
			return
		}
		items = append(items, resource)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (s *Server) v0AgentgatewayPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentgatewayAdmin(w, r) {
		return
	}
	expectedKind, ok := agentgatewayKinds[chi.URLParam(r, "resourceType")]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown AgentGateway resource type")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, agentgatewayBodyLimit+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read request body")
		return
	}
	if len(body) > agentgatewayBodyLimit {
		writeErr(w, http.StatusRequestEntityTooLarge, "AgentGateway resource exceeds 1 MiB")
		return
	}
	var resource map[string]interface{}
	if err := json.Unmarshal(body, &resource); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	kind, _ := resource["kind"].(string)
	metadata, _ := resource["metadata"].(map[string]interface{})
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	name, _ := metadata["name"].(string)
	if kind != expectedKind || name != chi.URLParam(r, "name") {
		writeErr(w, http.StatusBadRequest, "resource kind and name must match the request path")
		return
	}
	metadata["namespace"] = agentgatewayTargetNamespace
	resource["metadata"] = metadata
	artifact := v1alpha1.Object{
		APIVersion: v1alpha1.GroupVersion,
		Kind:       v1alpha1.KindGatewayResource,
		Metadata: v1alpha1.ObjectMeta{
			Name:       gatewayArtifactName(kind, name),
			Namespace:  agentgatewayRegistryNamespace,
			Visibility: v1alpha1.VisibilityPrivate,
		},
		Spec: resource,
	}
	if err := artifact.Validate(); err != nil {
		writeValidationErr(w, err)
		return
	}
	result, created, err := s.store.Apply(r.Context(), artifact)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store AgentGateway desired state")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	stored := s.withIdentity(result)
	w.Header().Set("ETag", `"`+stored.Metadata.Digest+`"`)
	writeJSON(w, status, resource)
}

func (s *Server) v0AgentgatewayImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, agentgatewayBodyLimit+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read request body")
		return
	}
	if len(body) > agentgatewayBodyLimit {
		writeErr(w, http.StatusRequestEntityTooLarge, "AgentGateway import exceeds 1 MiB")
		return
	}
	var list struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid Kubernetes List JSON")
		return
	}
	if len(list.Items) == 0 {
		writeErr(w, http.StatusBadRequest, "AgentGateway import contains no resources")
		return
	}
	artifacts := make([]v1alpha1.Object, 0, len(list.Items))
	seen := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		kind, _ := item["kind"].(string)
		apiVersion, _ := item["apiVersion"].(string)
		metadata, _ := item["metadata"].(map[string]interface{})
		name, _ := metadata["name"].(string)
		resource := map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": agentgatewayTargetNamespace,
			},
			"spec": item["spec"],
		}
		artifact := v1alpha1.Object{
			APIVersion: v1alpha1.GroupVersion,
			Kind:       v1alpha1.KindGatewayResource,
			Metadata: v1alpha1.ObjectMeta{
				Name:       gatewayArtifactName(kind, name),
				Namespace:  agentgatewayRegistryNamespace,
				Visibility: v1alpha1.VisibilityPrivate,
			},
			Spec: resource,
		}
		if err := artifact.Validate(); err != nil {
			writeValidationErr(w, err)
			return
		}
		if !auth.CanWrite(identity(r), artifact.Normalized()) {
			writeErr(w, http.StatusForbidden, "tenant writer permission required for AgentGateway import")
			return
		}
		if seen[artifact.Metadata.Name] {
			writeErr(w, http.StatusBadRequest, "duplicate AgentGateway resource in import")
			return
		}
		seen[artifact.Metadata.Name] = true
		artifacts = append(artifacts, artifact)
	}
	for _, artifact := range artifacts {
		if _, _, err := s.store.Apply(r.Context(), artifact); err != nil {
			writeErr(w, http.StatusInternalServerError, "store AgentGateway import")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"count": len(artifacts)})
}

func (s *Server) v0AgentgatewayDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentgatewayAdmin(w, r) {
		return
	}
	kind, ok := agentgatewayKinds[chi.URLParam(r, "resourceType")]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown AgentGateway resource type")
		return
	}
	artifactName := gatewayArtifactName(kind, chi.URLParam(r, "name"))
	_, err := s.store.Get(r.Context(), v1alpha1.KindGatewayResource,
		agentgatewayRegistryNamespace, artifactName, v1alpha1.DefaultTag)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load AgentGateway desired state")
		return
	}
	if err := s.store.Delete(r.Context(), v1alpha1.KindGatewayResource,
		agentgatewayRegistryNamespace, artifactName, v1alpha1.DefaultTag); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete AgentGateway desired state")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func gatewayArtifactName(kind, name string) string {
	return agentgatewayKindSlugs[kind] + "--" + name
}

func gatewayResourceForOutput(spec map[string]interface{}) (map[string]interface{}, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	var resource map[string]interface{}
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, err
	}
	metadata, _ := resource["metadata"].(map[string]interface{})
	metadata["namespace"] = agentgatewayTargetNamespace
	resource["metadata"] = metadata
	return resource, nil
}

func gatewayResourceForExport(spec map[string]interface{}) (map[string]interface{}, error) {
	resource, err := gatewayResourceForOutput(spec)
	if err != nil {
		return nil, err
	}
	metadata := resource["metadata"].(map[string]interface{})
	metadata["labels"] = map[string]interface{}{
		"app.kubernetes.io/managed-by": "agentic-registry",
		"registry.agentic.dev/source":  "gateway-resource",
	}
	metadata["annotations"] = map[string]interface{}{
		"argocd.argoproj.io/compare-options": "IgnoreExtraneous",
		"argocd.argoproj.io/sync-options":    "Prune=false",
	}
	resource["metadata"] = metadata
	return resource, nil
}
