package api

import (
	"encoding/json"
	"net/http"

	"github.com/tesserix/agentic-registry/internal/auth"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeErr emits a structured error envelope so clients (and the dashboard) can
// render a meaningful state rather than guessing from a bare status code.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]interface{}{"code": status, "message": msg},
	})
}

// readPredicate builds the visibility/RBAC pre-filter for the request's caller.
// It is passed to the store so filtering happens before the label selector and
// before pagination.
func readPredicate(r *http.Request) func(v1alpha1.Object) bool {
	id := auth.FromContext(r.Context())
	return func(o v1alpha1.Object) bool { return auth.CanRead(id, o) }
}

func identity(r *http.Request) auth.Identity { return auth.FromContext(r.Context()) }
