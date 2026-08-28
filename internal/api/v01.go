package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// mountV01 registers the MCP-Registry-compatible Generic Registry API. The
// MCPServer.spec IS the server.json document, so entries are natively portable
// to/from the official MCP registry. We additionally serve registry-managed
// fields under a response-level _meta plane.
func (s *Server) mountV01(r chi.Router) {
	r.Route("/v0.1", func(r chi.Router) {
		r.Get("/health", s.health)
		r.Get("/servers", s.mcpListServers)
		r.Post("/publish", s.mcpPublish)
		// serverName is reverse-DNS with a slash, so capture the rest of the path.
		r.Get("/servers/{serverName}/versions", s.mcpListVersions)
		r.Get("/servers/{serverName}/versions/{version}", s.mcpGetVersion)
		r.Patch("/servers/{serverName}/versions/{version}/status", s.mcpSetStatus)
	})
}

const mcpMetaKey = "io.modelcontextprotocol.registry/official"

// mcpEnvelope wraps a server.json with the registry-managed _meta plane.
func mcpEnvelope(o v1alpha1.Object) map[string]interface{} {
	server := map[string]interface{}{}
	for k, v := range o.Spec {
		server[k] = v
	}
	// Ensure the interop-required fields are present from metadata.
	if _, ok := server["name"]; !ok {
		server["name"] = o.Metadata.Name
	}
	if _, ok := server["version"]; !ok {
		server["version"] = o.Metadata.Tag
	}
	status := "active"
	if o.Status != nil {
		if st, ok := o.Status["status"].(string); ok {
			status = st
		}
	}
	meta := map[string]interface{}{
		"status":   status,
		"isLatest": o.Metadata.Tag == v1alpha1.DefaultTag,
	}
	if o.Metadata.CreatedAt != nil {
		meta["publishedAt"] = o.Metadata.CreatedAt.Format(time.RFC3339)
	}
	if o.Metadata.UpdatedAt != nil {
		meta["updatedAt"] = o.Metadata.UpdatedAt.Format(time.RFC3339)
	}
	server["_meta"] = map[string]interface{}{mcpMetaKey: meta}
	return server
}

func (s *Server) mcpListServers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	opts := store.ListOptions{
		Kind:           v1alpha1.KindMCPServer,
		Namespace:      "all",
		Search:         q.Get("search"),
		LatestOnly:     true,
		IncludeDeleted: q.Get("include_deleted") == "true",
		Limit:          limit,
		Cursor:         q.Get("cursor"),
		CanRead:        readPredicate(r),
	}
	if us := q.Get("updated_since"); us != "" {
		if t, err := time.Parse(time.RFC3339, us); err == nil {
			opts.UpdatedSince = &t
			opts.IncludeDeleted = true // incremental sync needs tombstones
		} else {
			writeErr(w, http.StatusBadRequest, "updated_since must be RFC3339")
			return
		}
	}
	res, err := s.store.List(r.Context(), opts)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	servers := make([]map[string]interface{}, 0, len(res.Items))
	for _, o := range res.Items {
		servers = append(servers, mcpEnvelope(o))
	}
	meta := map[string]interface{}{"count": len(servers)}
	if res.NextCursor != "" {
		meta["nextCursor"] = res.NextCursor
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"servers": servers, "metadata": meta})
}

func (s *Server) serverName(r *http.Request) string {
	name, _ := url.PathUnescape(chi.URLParam(r, "serverName"))
	return name
}

// serverNamespace resolves the namespace an MCPServer lives in. mcpListServers
// browses every readable namespace (Namespace:"all"), but get/versions/status
// historically hardcoded DefaultNamespace — so a server published into any other
// namespace 404'd on detail/versions and a status PATCH silently mutated (or
// missed) the wrong namespace. Mirror the /v0 resolveNamespace behavior: honor
// an explicit ?namespace= and otherwise locate the server by name across the
// namespaces the caller can read, falling back to DefaultNamespace.
func (s *Server) serverNamespace(r *http.Request, name string) string {
	return s.resolveNamespace(r, v1alpha1.KindMCPServer, name)
}

func (s *Server) mcpListVersions(w http.ResponseWriter, r *http.Request) {
	name := s.serverName(r)
	ns := s.serverNamespace(r, name)
	tags, err := s.store.ListTags(r.Context(), v1alpha1.KindMCPServer, ns, name)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	servers := make([]map[string]interface{}, 0, len(tags))
	for _, tag := range tags {
		o, err := s.store.Get(r.Context(), v1alpha1.KindMCPServer, ns, name, tag)
		if err != nil || !auth.CanRead(identity(r), o) {
			continue
		}
		servers = append(servers, mcpEnvelope(o))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"servers": servers, "metadata": map[string]interface{}{"count": len(servers)}})
}

func (s *Server) mcpGetVersion(w http.ResponseWriter, r *http.Request) {
	name := s.serverName(r)
	ns := s.serverNamespace(r, name)
	version := chi.URLParam(r, "version")
	if version == "latest" {
		version = ""
	}
	o, err := s.store.Get(r.Context(), v1alpha1.KindMCPServer, ns, name, version)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "server version not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !auth.CanRead(identity(r), o) {
		writeErr(w, http.StatusNotFound, "server version not found")
		return
	}
	writeJSON(w, http.StatusOK, mcpEnvelope(o))
}

// mcpPublish accepts a server.json (ServerDetail) and stores it as an MCPServer
// whose spec IS the server.json. version becomes the tag.
func (s *Server) mcpPublish(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	obj, err := decodeObject(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// If the body is a bare server.json (no envelope), wrap it.
	if obj.Kind == "" && obj.Spec == nil {
		var raw map[string]interface{}
		if jerr := decodeJSONBytes(body, &raw); jerr != nil {
			writeErr(w, http.StatusBadRequest, jerr.Error())
			return
		}
		obj = v1alpha1.Object{Kind: v1alpha1.KindMCPServer, Spec: raw}
		if name, ok := raw["name"].(string); ok {
			obj.Metadata.Name = name
		}
		if ver, ok := raw["version"].(string); ok {
			obj.Metadata.Tag = ver
		}
	}
	obj.Kind = v1alpha1.KindMCPServer
	s.applyOne(w, r, obj, http.StatusCreated)
}

func (s *Server) mcpSetStatus(w http.ResponseWriter, r *http.Request) {
	name := s.serverName(r)
	ns := s.serverNamespace(r, name)
	version := chi.URLParam(r, "version")
	var body struct {
		Status        string `json:"status"`
		StatusMessage string `json:"statusMessage,omitempty"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	switch body.Status {
	case "active", "deprecated", "deleted":
	default:
		writeErr(w, http.StatusBadRequest, "status must be active|deprecated|deleted")
		return
	}
	o, err := s.store.Get(r.Context(), v1alpha1.KindMCPServer, ns, name, version)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "server version not found")
		return
	}
	if err == nil && !auth.CanPublish(identity(r), o) {
		writeErr(w, http.StatusForbidden, "insufficient permission")
		return
	}
	if err := s.store.SetStatus(r.Context(), v1alpha1.KindMCPServer, ns, name, version, body.Status); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
