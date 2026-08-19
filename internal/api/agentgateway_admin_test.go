package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/internal/config"
)

func agentgatewayAdminServer(t *testing.T) http.Handler {
	t.Helper()
	srv, _ := testServerWith(t, config.Config{
		StoreBackend: "memory",
		AuthMode:     "trusted-header",
		TrustedProxy: true,
		AdminEmails:  []string{"samyak.rout@gmail.com", "mahesh.sangawar@gmail.com"},
		AdminRole:    "agentgateway.models",
	})
	return srv
}

func agentgatewayRequest(t *testing.T, srv http.Handler, method, path, email, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if email != "" {
		req.Header.Set("X-Forwarded-User", "zitadel-user")
		req.Header.Set("X-Forwarded-Email", email)
		req.Header.Set("X-Forwarded-Groups", "agentgateway.models")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

const openAIBackend = `{
  "apiVersion":"agentgateway.dev/v1alpha1",
  "kind":"AgentgatewayBackend",
  "metadata":{"name":"devai-openai"},
  "spec":{"ai":{"groups":[{"providers":[{"name":"openai","openai":{},"policies":{"auth":{"passthrough":{}},"tls":{}}}]}]}}
}`

func TestAgentgatewayAdminUpsertsAndListsAllowlistedResource(t *testing.T) {
	srv := agentgatewayAdminServer(t)
	put := agentgatewayRequest(t, srv, http.MethodPut,
		"/v0/agentgateway/backends/devai-openai", "samyak.rout@gmail.com", openAIBackend)
	if put.Code != http.StatusCreated {
		t.Fatalf("PUT status=%d body=%s", put.Code, put.Body.String())
	}

	list := agentgatewayRequest(t, srv, http.MethodGet,
		"/v0/agentgateway/resources", "samyak.rout@gmail.com", "")
	if list.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", list.Code, list.Body.String())
	}
	var body struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Kind != "AgentgatewayBackend" ||
		body.Items[0].Metadata.Name != "devai-openai" || body.Items[0].Metadata.Namespace != "agentgateway-system" {
		t.Fatalf("unexpected resources: %#v", body.Items)
	}
}

func TestAgentgatewayAdminRejectsUnapprovedHuman(t *testing.T) {
	srv := agentgatewayAdminServer(t)
	rec := agentgatewayRequest(t, srv, http.MethodPut,
		"/v0/agentgateway/backends/devai-openai", "attacker@example.com", openAIBackend)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestAgentgatewayAdminRejectsPathKindMismatch(t *testing.T) {
	srv := agentgatewayAdminServer(t)
	rec := agentgatewayRequest(t, srv, http.MethodPut,
		"/v0/agentgateway/policies/devai-openai", "samyak.rout@gmail.com", openAIBackend)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestAgentgatewayAdminDeleteIsIdempotent(t *testing.T) {
	srv := agentgatewayAdminServer(t)
	for i := 0; i < 2; i++ {
		rec := agentgatewayRequest(t, srv, http.MethodDelete,
			"/v0/agentgateway/backends/missing", "samyak.rout@gmail.com", "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
}

func TestAgentgatewayExportIncludesDesiredStateWithManagedLabels(t *testing.T) {
	srv := agentgatewayAdminServer(t)
	put := agentgatewayRequest(t, srv, http.MethodPut,
		"/v0/agentgateway/backends/devai-openai", "samyak.rout@gmail.com", openAIBackend)
	if put.Code != http.StatusCreated {
		t.Fatalf("PUT status=%d body=%s", put.Code, put.Body.String())
	}

	export := agentgatewayRequest(t, srv, http.MethodGet,
		"/v0/export/agentgateway?namespace=devai&targetNamespace=agentgateway-system", "samyak.rout@gmail.com", "")
	if export.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", export.Code, export.Body.String())
	}
	for _, want := range []string{
		"kind: AgentgatewayBackend",
		"name: devai-openai",
		"namespace: agentgateway-system",
		"app.kubernetes.io/managed-by: agentic-registry",
	} {
		if !strings.Contains(export.Body.String(), want) {
			t.Fatalf("export missing %q:\n%s", want, export.Body.String())
		}
	}
	if export.Header().Get("X-Agentgateway-Resource-Count") != "1" || export.Header().Get("X-Agentgateway-Resource-Digest") == "" {
		t.Fatalf("missing export integrity headers: %#v", export.Header())
	}
}

func TestAgentgatewayImportAllowsTenantDeployKeyWithoutGrantingHumanAdmin(t *testing.T) {
	digest := sha256.Sum256([]byte("migration-key"))
	srv, _ := testServerWith(t, config.Config{
		StoreBackend:  "memory",
		AuthMode:      "anonymous",
		AnonymousRole: "read",
		DeployKeys: []config.DeployKey{{
			TenantID: "devai",
			SHA256:   fmt.Sprintf("%x", digest),
		}},
	})
	listBody := `{"apiVersion":"v1","kind":"List","items":[` + openAIBackend + `]}`
	req := httptest.NewRequest(http.MethodPost, "/v0/agentgateway/import", strings.NewReader(listBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer migration-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("unexpected import body: %s", rec.Body.String())
	}

	admin := httptest.NewRequest(http.MethodPut, "/v0/agentgateway/backends/devai-openai", strings.NewReader(openAIBackend))
	admin.Header.Set("Content-Type", "application/json")
	admin.Header.Set("Authorization", "Bearer migration-key")
	adminRec := httptest.NewRecorder()
	srv.ServeHTTP(adminRec, admin)
	if adminRec.Code != http.StatusForbidden {
		t.Fatalf("tenant deploy key gained human admin access: status=%d", adminRec.Code)
	}

	export := httptest.NewRequest(http.MethodGet, "/v0/export/agentgateway?namespace=devai", nil)
	export.Header.Set("Authorization", "Bearer migration-key")
	exportRec := httptest.NewRecorder()
	srv.ServeHTTP(exportRec, export)
	if exportRec.Code != http.StatusOK || !strings.Contains(exportRec.Body.String(), "name: devai-openai") {
		t.Fatalf("imported resource not exported: status=%d body=%s", exportRec.Code, exportRec.Body.String())
	}
}
