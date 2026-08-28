package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCIClientCredentialsUseEnvironmentAndPOSTBodyOnly(t *testing.T) {
	var gotAuth, gotGrant, gotScope string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = r.ParseForm()
		gotGrant = r.Form.Get("grant_type")
		gotScope = r.Form.Get("scope")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "short-lived", "expires_in": 300})
	}))
	defer server.Close()
	t.Setenv("AGENTIC_TOKEN", "")
	t.Setenv("AGENTIC_CLIENT_ID", "ci-client")
	t.Setenv("AGENTIC_CLIENT_SECRET", "ci-secret")
	t.Setenv("AGENTIC_TOKEN_URL", server.URL)
	t.Setenv("AGENTIC_SCOPES", "registry:read registry:publish")

	token, err := resolveAccessToken(config{Registry: "https://registry.example"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "short-lived" || gotGrant != "client_credentials" || gotScope != "registry:read registry:publish" {
		t.Fatalf("token=%q grant=%q scope=%q", token, gotGrant, gotScope)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") || strings.Contains(gotAuth, "ci-secret") {
		t.Fatalf("client authentication must use HTTP Basic without plaintext header: %q", gotAuth)
	}
}

func TestPlaintextCredentialFallbackIsExplicitAndMode0600(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTIC_CONFIG_HOME", dir)
	store := fileCredentialStore{path: credentialsPath()}
	record := oauthCredential{RefreshToken: "refresh-secret", AccessToken: "access", ExpiresAt: time.Now().Add(time.Minute)}
	if err := store.Set("registry-account", record); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(credentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode=%o", info.Mode().Perm())
	}
	loaded, err := store.Get("registry-account")
	if err != nil || loaded.RefreshToken != "refresh-secret" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestLoadConfigIgnoresLegacyPlaintextToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTIC_CONFIG_HOME", dir)
	t.Setenv("AGENTIC_TOKEN", "")
	if err := os.MkdirAll(filepath.Dir(configPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(), []byte(`{"registry":"https://registry.example","token":"legacy-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig()
	if cfg.Registry != "https://registry.example" {
		t.Fatalf("registry=%q", cfg.Registry)
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "legacy-secret") || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("legacy token leaked into config: %s", raw)
	}
}

func TestRefreshFlowRotatesStoredRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("grant_type") != "refresh_token" {
			t.Fatalf("unexpected refresh form: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 300,
		})
	}))
	defer server.Close()
	record, err := refreshOAuthCredential(http.DefaultClient, oauthCredential{
		RefreshToken: "old-refresh", TokenEndpoint: server.URL, ClientID: "public-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.AccessToken != "new-access" || record.RefreshToken != "new-refresh" {
		t.Fatalf("record=%#v", record)
	}
}
