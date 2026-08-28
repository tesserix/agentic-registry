package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/internal/discovery"
	"github.com/tesserix/agentic-registry/internal/render"
	"github.com/tesserix/agentic-registry/internal/selector"
	"github.com/tesserix/agentic-registry/internal/store"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// mountV0 registers the devai-compatible catalog API. Collection names accept
// the "servers" alias for MCPServer. apiVersion is normalized on ingest.
func (s *Server) mountV0(r chi.Router) {
	r.Route("/v0", func(r chi.Router) {
		r.Get("/health", s.health)

		// Batch apply / delete (kubectl-apply-like, multi-doc YAML).
		r.Post("/apply", s.v0Apply)
		r.Delete("/apply", s.v0DeleteApply)

		// Prompt render (Portkey-style; keeps the registry off the request path).
		r.Post("/prompts/{name}/render", s.v0Render)

		// Global, cross-kind discovery (powers the Cmd+K palette). Registered
		// before /{plural} so the static path wins over the collection param.
		r.Get("/search", s.v0Search)

		// Public signing key for verifying digest attestations.
		r.Get("/signing-key", s.signingKey)
		// Safe browser capability discovery. This exposes no token, group, scope,
		// or tenant data; the UI uses it only to hide mutation controls from
		// callers who are not global registry administrators.
		r.Get("/session", s.v0Session)

		// Runtime export — render the catalog into control-plane config the
		// in-cluster sync Jobs apply. agentgateway: all MCP servers → routing
		// (AgentgatewayBackend + HTTPRoute). kagent export is per-agent, mounted
		// in the collection route below.
		r.Get("/export/agentgateway", s.v0ExportAgentgateway)

		// Tesserix AgentGateway desired state. Solo's XDS UI remains the runtime
		// traffic view; these endpoints are the authenticated write boundary.
		r.Route("/agentgateway", func(r chi.Router) {
			r.Get("/resources", s.v0AgentgatewayList)
			r.Post("/import", s.v0AgentgatewayImport)
			r.Put("/{resourceType}/{name}", s.v0AgentgatewayPut)
			r.Delete("/{resourceType}/{name}", s.v0AgentgatewayDelete)
		})
		// kagent: every (optionally label-filtered) agent → Agent + RemoteMCPServer
		// CRs, applied by the kagent agent-sync Job.
		r.Get("/export/kagent", s.v0ExportKagentAll)

		// Per-collection CRUD.
		r.Route("/{plural}", func(r chi.Router) {
			r.Get("/", s.v0List)
			r.Post("/", s.v0Publish)
			r.Get("/{name}", s.v0GetLatest)
			r.Get("/{name}/tags", s.v0Tags)
			r.Get("/{name}/revisions", s.v0Revisions)
			// A2A Agent Card (Agent kind only). The .well-known suffix lets a
			// consumer that knows only the A2A convention resolve the card.
			r.Get("/{name}/card", s.v0AgentCard)
			r.Get("/{name}/.well-known/agent-card.json", s.v0AgentCard)
			r.Get("/{name}/{tag}/card", s.v0AgentCardTag)
			// Composition resolution (Agent kind only): the agent plus its
			// skills/tools/mcpServers/prompts fetched from the catalog. Powers
			// the runtime + the kagent/agentgateway export adapters.
			r.Get("/{name}/resolved", s.v0AgentResolved)
			r.Get("/{name}/{tag}/resolved", s.v0AgentResolvedTag)
			// kagent export (Agent kind only): the agent + a RemoteMCPServer per
			// resolved MCP dependency, as applyable kagent.dev YAML.
			r.Get("/{name}/export/kagent", s.v0ExportKagent)
			// Capability probe results (MCPServer kind only): what the server
			// actually serves, compared against what it declares.
			r.Put("/{name}/status", s.v0PutMCPServerStatus)
			r.Get("/{name}/{tag}", s.v0Get)
			r.Delete("/{name}/{tag}", s.v0Delete)
		})
	})
}

func (s *Server) v0Session(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": id.Authenticated,
		"email":         id.Email,
		"admin":         auth.CanAdmin(id),
	})
}

func (s *Server) kindFromPath(w http.ResponseWriter, r *http.Request) (v1alpha1.Kind, bool) {
	plural := chi.URLParam(r, "plural")
	kind, ok := v1alpha1.KindForPlural(plural)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown collection: "+plural)
		return "", false
	}
	return kind, true
}

func (s *Server) namespace(r *http.Request) string {
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		return ns
	}
	return v1alpha1.DefaultNamespace
}

// resolveNamespace picks the namespace for a name-scoped read (get/tags/
// revisions). An explicit ?namespace= is honored verbatim. When it is absent
// the registry has no single home for an artifact — it may live in any
// namespace (e.g. devai agents, MCP servers) — so resolve by name across every
// namespace the caller can read (the same set the catalog list browses) and
// return the match. This makes shared/bookmarked detail URLs that omit the
// namespace resolve instead of 404ing against "default". Falls back to
// DefaultNamespace when nothing matches, preserving prior behavior.
func (s *Server) resolveNamespace(r *http.Request, kind v1alpha1.Kind, name string) string {
	if ns := strings.TrimSpace(r.URL.Query().Get("namespace")); ns != "" {
		return ns
	}
	res, err := s.store.List(r.Context(), store.ListOptions{
		Kind:       kind,
		Namespace:  "", // across every readable namespace
		LatestOnly: true,
		CanRead:    readPredicate(r),
		Limit:      1 << 30,
	})
	if err == nil {
		for _, o := range res.Items {
			if o.Metadata.Name == name {
				return o.Metadata.Namespace
			}
		}
	}
	return v1alpha1.DefaultNamespace
}

// listNamespace resolves the namespace *filter* for browse/list/search requests.
// Unlike s.namespace (used for get/publish, which target one namespace), a list
// has no inherent namespace: an absent param means "browse every namespace the
// caller can read" — visibility/tenant RBAC (CanRead) is the real isolation
// boundary, namespace is just an optional grouping filter. We also fold the
// common unset-frontend sentinels ("all", "*", "undefined", "null") to the
// all-namespaces form so a UI that omits or stubs the param still gets results.
// The store treats "" / "all" as no namespace filter (memory.go, postgres.go).
func listNamespace(r *http.Request) string {
	switch ns := strings.TrimSpace(r.URL.Query().Get("namespace")); ns {
	case "", "all", "*", "undefined", "null":
		return ""
	default:
		return ns
	}
}

func (s *Server) v0List(w http.ResponseWriter, r *http.Request) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	sel, err := selector.Parse(r.URL.Query().Get("labelSelector"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "labelSelector: "+err.Error())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	res, err := s.store.List(r.Context(), store.ListOptions{
		Kind:       kind,
		Namespace:  listNamespace(r), // absent → all readable namespaces
		Selector:   sel,
		Search:     r.URL.Query().Get("search"),
		LatestOnly: true,
		Limit:      limit,
		Cursor:     r.URL.Query().Get("cursor"),
		CanRead:    readPredicate(r),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// devai's client expects a bare JSON array per collection.
	out := make([]v1alpha1.Object, 0, len(res.Items))
	for _, o := range res.Items {
		out = append(out, s.withIdentity(o))
	}
	if res.NextCursor != "" {
		w.Header().Set("X-Next-Cursor", res.NextCursor)
	}
	writeJSON(w, http.StatusOK, out)
}

// v0Search runs one cross-kind, ranked query for the command palette. With
// pgvector enabled the store returns cosine-ranked matches; otherwise it falls
// back to substring. Kind is left empty so every collection is searched at once.
func (s *Server) v0Search(w http.ResponseWriter, r *http.Request) {
	// Empty q = browse (list latest across every readable namespace) instead of
	// an empty result, so the marketplace/command palette shows the catalog on
	// open; a non-empty q runs the ranked (pgvector or substring) search.
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 512 {
		writeErr(w, http.StatusBadRequest, "q must be at most 512 characters")
		return
	}
	kinds, err := discovery.ParseKinds(r.URL.Query()["kinds"])
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	view := strings.TrimSpace(r.URL.Query().Get("view"))
	if view != "" && view != "artifact" && view != "stub" {
		writeErr(w, http.StatusBadRequest, "view must be artifact or stub")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	rbac := readPredicate(r)
	canRead := func(obj v1alpha1.Object) bool {
		return discovery.Allows(kinds, obj.Kind) && rbac(obj)
	}
	res, err := s.store.List(r.Context(), store.ListOptions{
		Namespace:  listNamespace(r), // absent → all readable namespaces
		Search:     q,                // "" → no search filter (browse)
		LatestOnly: true,
		Limit:      limit,
		CanRead:    canRead,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if view == "stub" {
		out := make([]discovery.Stub, 0, len(res.Items))
		for _, obj := range res.Items {
			out = append(out, discovery.BuildStub(s.withIdentity(obj)))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out := make([]v1alpha1.Object, 0, len(res.Items))
	for _, o := range res.Items {
		out = append(out, s.withIdentity(o))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) v0GetLatest(w http.ResponseWriter, r *http.Request) {
	s.getObject(w, r, "")
}

func (s *Server) v0Get(w http.ResponseWriter, r *http.Request) {
	s.getObject(w, r, chi.URLParam(r, "tag"))
}

func (s *Server) getObject(w http.ResponseWriter, r *http.Request, tag string) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	obj, err := s.store.Get(r.Context(), kind, s.resolveNamespace(r, kind, name), name, tag)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !auth.CanRead(identity(r), obj) {
		// Do not reveal existence of artifacts the caller cannot read.
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, s.withIdentity(obj))
}

func (s *Server) v0Tags(w http.ResponseWriter, r *http.Request) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	ns := s.resolveNamespace(r, kind, name)
	// Confirm read access via the latest tag before listing tags.
	latest, err := s.store.Get(r.Context(), kind, ns, name, "")
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err == nil && !auth.CanRead(identity(r), latest) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	tags, err := s.store.ListTags(r.Context(), kind, ns, name)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"name": name, "tags": tags})
}

// v0Revisions returns the artifact's append-only audit timeline (all tags).
func (s *Server) v0Revisions(w http.ResponseWriter, r *http.Request) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	ns := s.resolveNamespace(r, kind, name)
	// Confirm read access via the latest tag before exposing history.
	latest, err := s.store.Get(r.Context(), kind, ns, name, "")
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err == nil && !auth.CanRead(identity(r), latest) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	revs, err := s.store.ListRevisions(r.Context(), kind, ns, name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if revs == nil {
		revs = []store.Revision{}
	}
	writeJSON(w, http.StatusOK, revs)
}

func (s *Server) v0Publish(w http.ResponseWriter, r *http.Request) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	obj, err := decodeObject(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if obj.Kind == "" {
		obj.Kind = kind
	}
	s.applyOne(w, r, obj, http.StatusCreated)
}

// applyOne validates, authorizes, and upserts a single object.
func (s *Server) applyOne(w http.ResponseWriter, r *http.Request, obj v1alpha1.Object, okStatus int) {
	if err := obj.Validate(); err != nil {
		writeValidationErr(w, err)
		return
	}
	normalized := obj.Normalized()
	if !auth.CanWrite(identity(r), normalized) {
		writeErr(w, http.StatusForbidden, "insufficient permission to publish to tenant "+normalized.Metadata.TenantID)
		return
	}
	result, created, err := s.store.Apply(r.Context(), obj)
	if errors.Is(err, store.ErrTenantConflict) {
		// A different tenant owns this (kind,namespace,name,tag); refuse to let
		// the upsert reassign ownership.
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	if errors.Is(err, store.ErrImmutableTag) || errors.Is(err, store.ErrNameConflict) {
		// 409 with the full reason: which name/namespace collided and which
		// kind + ARN already owns it, so the publisher knows exactly why.
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := http.StatusOK
	if created {
		status = okStatus
	}
	writeJSON(w, status, s.withIdentity(result))
}

func (s *Server) v0Delete(w http.ResponseWriter, r *http.Request) {
	kind, ok := s.kindFromPath(w, r)
	if !ok {
		return
	}
	name, tag := chi.URLParam(r, "name"), chi.URLParam(r, "tag")
	obj, err := s.store.Get(r.Context(), kind, s.namespace(r), name, tag)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err == nil && !auth.CanWrite(identity(r), obj) {
		writeErr(w, http.StatusForbidden, "insufficient permission")
		return
	}
	if err := s.store.Delete(r.Context(), kind, s.namespace(r), name, tag); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// v0Apply ingests a multi-doc YAML stream of resources (kubectl-apply-like).
func (s *Server) v0Apply(w http.ResponseWriter, r *http.Request) {
	objs, err := decodeMultiDoc(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(objs) > maxApplyDocs {
		writeErr(w, http.StatusRequestEntityTooLarge,
			"too many documents in one apply: limit is "+strconv.Itoa(maxApplyDocs))
		return
	}
	id := identity(r)
	type applied struct {
		Kind      v1alpha1.Kind `json:"kind"`
		Name      string        `json:"name"`
		Namespace string        `json:"namespace"`
		Tag       string        `json:"tag"`
		Created   bool          `json:"created"`
		Error     string        `json:"error,omitempty"`
	}
	results := make([]applied, 0, len(objs))
	httpStatus := http.StatusOK
	for _, obj := range objs {
		a := applied{Kind: obj.Kind, Name: obj.Metadata.Name}
		if err := obj.Validate(); err != nil {
			a.Error = err.Error()
			httpStatus = http.StatusMultiStatus
			results = append(results, a)
			continue
		}
		normalized := obj.Normalized()
		a.Namespace, a.Tag = normalized.Metadata.Namespace, normalized.Metadata.Tag
		if !auth.CanWrite(id, normalized) {
			a.Error = "forbidden: cannot write tenant " + normalized.Metadata.TenantID
			httpStatus = http.StatusMultiStatus
			results = append(results, a)
			continue
		}
		res, created, err := s.store.Apply(r.Context(), obj)
		if err != nil {
			a.Error = err.Error()
			httpStatus = http.StatusMultiStatus
		} else {
			a.Created = created
			a.Namespace, a.Tag = res.Metadata.Namespace, res.Metadata.Tag
		}
		results = append(results, a)
	}
	writeJSON(w, httpStatus, map[string]interface{}{"applied": results, "count": len(results)})
}

func (s *Server) v0DeleteApply(w http.ResponseWriter, r *http.Request) {
	objs, err := decodeMultiDoc(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(objs) > maxApplyDocs {
		writeErr(w, http.StatusRequestEntityTooLarge,
			"too many documents in one apply: limit is "+strconv.Itoa(maxApplyDocs))
		return
	}
	id := identity(r)
	deleted := 0
	for _, obj := range objs {
		n := obj.Normalized()
		existing, gerr := s.store.Get(r.Context(), n.Kind, n.Metadata.Namespace, n.Metadata.Name, n.Metadata.Tag)
		if gerr != nil || !auth.CanWrite(id, existing) {
			continue
		}
		if s.store.Delete(r.Context(), n.Kind, n.Metadata.Namespace, n.Metadata.Name, n.Metadata.Tag) == nil {
			deleted++
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": deleted})
}

func (s *Server) v0Render(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req render.Request
	if err := decodeJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Resolve the prompt: name may carry a @version/@label suffix.
	base, ref := splitRef(name)
	obj, err := s.store.Get(r.Context(), v1alpha1.KindPrompt, s.namespace(r), base, ref)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "prompt not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !auth.CanRead(identity(r), obj) {
		writeErr(w, http.StatusNotFound, "prompt not found")
		return
	}
	out, err := render.Prompt(obj, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "render: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// splitRef splits "name@ref" into ("name","ref"); ref "" means latest.
func splitRef(s string) (string, string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}
