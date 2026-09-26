package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

const keyringService = "agentic-registry"

type config struct {
	Registry        string `json:"registry"`
	Issuer          string `json:"issuer,omitempty"`
	ClientID        string `json:"client_id,omitempty"`
	Audience        string `json:"audience,omitempty"`
	CredentialStore string `json:"credential_store,omitempty"`
}

type oauthCredential struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
	TokenEndpoint string    `json:"token_endpoint"`
	ClientID      string    `json:"client_id"`
	Scope         string    `json:"scope,omitempty"`
}

type credentialStore interface {
	Get(string) (oauthCredential, error)
	Set(string, oauthCredential) error
	Delete(string) error
}

type keyringCredentialStore struct{}

func (keyringCredentialStore) Get(account string) (oauthCredential, error) {
	raw, err := keyring.Get(keyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return oauthCredential{}, os.ErrNotExist
	}
	if err != nil {
		return oauthCredential{}, err
	}
	var credential oauthCredential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil {
		return oauthCredential{}, fmt.Errorf("decode OS credential: %w", err)
	}
	return credential, nil
}

func (keyringCredentialStore) Set(account string, credential oauthCredential) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, account, string(raw))
}

func (keyringCredentialStore) Delete(account string) error {
	err := keyring.Delete(keyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

type fileCredentialStore struct{ path string }

func (s fileCredentialStore) Get(account string) (oauthCredential, error) {
	info, err := os.Lstat(s.path)
	if err != nil {
		return oauthCredential{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return oauthCredential{}, fmt.Errorf("credential fallback path must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return oauthCredential{}, fmt.Errorf("credential fallback file permissions must be 0600")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return oauthCredential{}, err
	}
	var records map[string]oauthCredential
	if err := json.Unmarshal(raw, &records); err != nil {
		return oauthCredential{}, err
	}
	record, ok := records[account]
	if !ok {
		return oauthCredential{}, os.ErrNotExist
	}
	return record, nil
}

func (s fileCredentialStore) Set(account string, credential oauthCredential) error {
	records := map[string]oauthCredential{}
	if info, statErr := os.Lstat(s.path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("credential fallback path is unsafe")
		}
		existing, err := os.ReadFile(s.path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(existing, &records); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	records[account] = credential
	return writePrivateJSON(s.path, records)
}

func (s fileCredentialStore) Delete(account string) error {
	records := map[string]oauthCredential{}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("credential fallback path is unsafe")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		return err
	}
	delete(records, account)
	return writePrivateJSON(s.path, records)
}

func configDir() string {
	if configured := strings.TrimSpace(os.Getenv("AGENTIC_CONFIG_HOME")); configured != "" {
		return configured
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentic")
}

func configPath() string      { return filepath.Join(configDir(), "config.json") }
func credentialsPath() string { return filepath.Join(configDir(), "credentials.json") }

func loadConfig() config {
	c := config{Registry: "http://localhost:8080"}
	if raw, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(raw, &c)
		var legacy map[string]json.RawMessage
		if json.Unmarshal(raw, &legacy) == nil && legacy["token"] != nil {
			fmt.Fprintln(os.Stderr, "warning: ignoring legacy plaintext token in config.json; run 'agentic auth login'")
		}
	}
	if registry := strings.TrimSpace(os.Getenv("AGENTIC_REGISTRY")); registry != "" {
		c.Registry = registry
	}
	return c
}

func saveConfig(c config) error { return writePrivateJSON(configPath(), c) }

func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentic-credentials-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func credentialAccount(registry string) string {
	digest := sha256.Sum256([]byte(strings.TrimRight(registry, "/")))
	return hex.EncodeToString(digest[:16])
}

func configuredStore(c config) credentialStore {
	switch c.CredentialStore {
	case "keyring":
		return keyringCredentialStore{}
	case "file":
		fmt.Fprintln(os.Stderr, "warning: OAuth credentials are using the explicitly enabled plaintext JSON fallback")
		return fileCredentialStore{path: credentialsPath()}
	default:
		return nil
	}
}

var ciTokenCache struct {
	sync.Mutex
	key       string
	token     string
	expiresAt time.Time
}

func resolveAccessToken(c config) (string, error) {
	if token := strings.TrimSpace(os.Getenv("AGENTIC_TOKEN")); token != "" {
		return token, nil
	}
	clientID := strings.TrimSpace(os.Getenv("AGENTIC_CLIENT_ID"))
	clientSecret := os.Getenv("AGENTIC_CLIENT_SECRET")
	tokenURL := strings.TrimSpace(os.Getenv("AGENTIC_TOKEN_URL"))
	if clientID != "" || clientSecret != "" || tokenURL != "" {
		if clientID == "" || clientSecret == "" || tokenURL == "" {
			return "", errors.New("AGENTIC_CLIENT_ID, AGENTIC_CLIENT_SECRET, and AGENTIC_TOKEN_URL must be set together")
		}
		scopes := strings.TrimSpace(os.Getenv("AGENTIC_SCOPES"))
		if scopes == "" {
			scopes = "openid urn:zitadel:iam:org:projects:roles urn:zitadel:iam:user:metadata registry:read registry:publish"
			audience := strings.TrimSpace(os.Getenv("AGENTIC_AUDIENCE"))
			if audience == "" {
				audience = c.Audience
			}
			if audience != "" {
				scopes += " urn:zitadel:iam:org:project:id:" + audience + ":aud"
			}
		}
		return cachedClientCredentialsToken(clientID, clientSecret, tokenURL, scopes)
	}
	store := configuredStore(c)
	if store == nil {
		return "", nil
	}
	record, err := store.Get(credentialAccount(c.Registry))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read OAuth credential: %w", err)
	}
	if record.AccessToken != "" && time.Until(record.ExpiresAt) > 30*time.Second {
		return record.AccessToken, nil
	}
	if record.RefreshToken == "" {
		return "", errors.New("OAuth session expired; run 'agentic auth login'")
	}
	refreshed, err := refreshOAuthCredential(http.DefaultClient, record)
	if err != nil {
		return "", err
	}
	if err := store.Set(credentialAccount(c.Registry), refreshed); err != nil {
		return "", fmt.Errorf("store refreshed OAuth credential: %w", err)
	}
	return refreshed.AccessToken, nil
}

func cachedClientCredentialsToken(clientID, secret, tokenURL, scopes string) (string, error) {
	keyDigest := sha256.Sum256([]byte(clientID + "\x00" + tokenURL + "\x00" + scopes + "\x00" + secret))
	key := hex.EncodeToString(keyDigest[:])
	ciTokenCache.Lock()
	defer ciTokenCache.Unlock()
	if ciTokenCache.key == key && time.Until(ciTokenCache.expiresAt) > 30*time.Second {
		return ciTokenCache.token, nil
	}
	response, err := requestOAuthToken(http.DefaultClient, tokenURL, clientID, secret, url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {scopes},
	})
	if err != nil {
		return "", err
	}
	ciTokenCache.key, ciTokenCache.token = key, response.AccessToken
	ciTokenCache.expiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	return response.AccessToken, nil
}

func refreshOAuthCredential(client *http.Client, record oauthCredential) (oauthCredential, error) {
	response, err := requestOAuthToken(client, record.TokenEndpoint, record.ClientID, "", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {record.RefreshToken},
		"client_id":     {record.ClientID},
	})
	if err != nil {
		return oauthCredential{}, fmt.Errorf("refresh OAuth session: %w", err)
	}
	if response.RefreshToken == "" {
		response.RefreshToken = record.RefreshToken
	}
	return credentialFromToken(response, record.TokenEndpoint, record.ClientID), nil
}

func cmdAuth(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agentic auth <login|status|logout>")
	}
	switch args[0] {
	case "login":
		return cmdAuthLogin(args[1:])
	case "status":
		return cmdAuthStatus(args[1:])
	case "logout":
		return cmdAuthLogout(args[1:])
	default:
		return fmt.Errorf("unknown auth command %q", args[0])
	}
}

type registryAuthConfig struct {
	Issuer   string   `json:"issuer"`
	ClientID string   `json:"client_id"`
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes"`
}

type oidcDiscovery struct {
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
}

func cmdAuthLogin(args []string) error {
	fs := flags(args)
	if _, supplied := fs["token"]; supplied {
		return errors.New("--token is disabled because process arguments are visible; use OAuth login or AGENTIC_TOKEN")
	}
	c := loadConfig()
	if registry := strings.TrimSpace(fs["registry"]); registry != "" {
		c.Registry = registry
	}
	public, err := loadRegistryAuthConfig(c.Registry)
	if err != nil {
		return err
	}
	if issuer := strings.TrimSpace(fs["issuer"]); issuer != "" {
		public.Issuer = issuer
	}
	if clientID := strings.TrimSpace(fs["client-id"]); clientID != "" {
		public.ClientID = clientID
	}
	if public.Issuer == "" || public.ClientID == "" {
		return errors.New("registry does not advertise OAuth issuer/client ID; pass --issuer and --client-id")
	}
	discovery, err := discoverOIDC(public.Issuer)
	if err != nil {
		return err
	}
	scope := strings.Join(public.Scopes, " ")
	var credential oauthCredential
	_, noBrowser := fs["no-browser"]
	if discovery.DeviceAuthorizationEndpoint != "" {
		credential, err = deviceAuthorizationLogin(discovery, public.ClientID, public.Audience, scope, !noBrowser)
	} else {
		credential, err = pkceLogin(discovery, public.ClientID, public.Audience, scope, !noBrowser)
	}
	if err != nil {
		return err
	}
	var store credentialStore = keyringCredentialStore{}
	c.CredentialStore = "keyring"
	if _, allowed := fs["allow-plaintext-token-store"]; allowed {
		fmt.Fprintln(os.Stderr, "warning: storing OAuth credentials in a 0600 JSON file because plaintext fallback was explicitly enabled")
		store = fileCredentialStore{path: credentialsPath()}
		c.CredentialStore = "file"
	}
	if err := store.Set(credentialAccount(c.Registry), credential); err != nil {
		if c.CredentialStore == "keyring" {
			return fmt.Errorf("OS credential store unavailable: %w (retry with --allow-plaintext-token-store only if you accept the risk)", err)
		}
		return err
	}
	c.Issuer, c.ClientID, c.Audience = public.Issuer, public.ClientID, public.Audience
	if err := saveConfig(c); err != nil {
		_ = store.Delete(credentialAccount(c.Registry))
		return err
	}
	fmt.Printf("signed in to %s; credentials stored in %s\n", c.Registry, c.CredentialStore)
	return nil
}

func cmdAuthStatus(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: agentic auth status")
	}
	c := loadConfig()
	token, err := resolveAccessToken(c)
	if err != nil {
		return err
	}
	state := "signed out"
	if token != "" {
		state = "signed in"
	}
	fmt.Printf("registry: %s\nstatus: %s\ncredential store: %s\n", c.Registry, state, valueOr(c.CredentialStore, "none"))
	return nil
}

func cmdAuthLogout(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: agentic auth logout")
	}
	c := loadConfig()
	if store := configuredStore(c); store != nil {
		if err := store.Delete(credentialAccount(c.Registry)); err != nil {
			return err
		}
	}
	c.CredentialStore = ""
	if err := saveConfig(c); err != nil {
		return err
	}
	fmt.Printf("signed out from %s\n", c.Registry)
	return nil
}

func loadRegistryAuthConfig(registry string) (registryAuthConfig, error) {
	var config registryAuthConfig
	endpoint := strings.TrimRight(registry, "/") + "/v0/auth/config"
	if err := getJSON(http.DefaultClient, endpoint, &config); err != nil {
		return config, fmt.Errorf("load Registry OAuth configuration: %w", err)
	}
	return config, nil
}

func discoverOIDC(issuer string) (oidcDiscovery, error) {
	var discovery oidcDiscovery
	endpoint := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	if err := getJSON(http.DefaultClient, endpoint, &discovery); err != nil {
		return discovery, fmt.Errorf("discover OAuth provider: %w", err)
	}
	if discovery.TokenEndpoint == "" || discovery.AuthorizationEndpoint == "" {
		return discovery, errors.New("OAuth discovery document is incomplete")
	}
	for _, endpoint := range []string{
		discovery.AuthorizationEndpoint,
		discovery.TokenEndpoint,
		discovery.DeviceAuthorizationEndpoint,
	} {
		if endpoint != "" {
			if err := validateOAuthEndpoint(endpoint); err != nil {
				return discovery, fmt.Errorf("unsafe OAuth discovery endpoint: %w", err)
			}
		}
	}
	return discovery, nil
}

func getJSON(client *http.Client, endpoint string, output any) error {
	if err := validateOAuthEndpoint(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OAuth metadata returned %s", response.Status)
	}
	return json.Unmarshal(raw, output)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func requestOAuthToken(client *http.Client, endpoint, clientID, secret string, form url.Values) (tokenResponse, error) {
	var token tokenResponse
	if err := validateOAuthEndpoint(endpoint); err != nil {
		return token, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return token, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if secret != "" {
		req.SetBasicAuth(clientID, secret)
	}
	response, err := client.Do(req)
	if err != nil {
		return token, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return token, err
	}
	if err := json.Unmarshal(raw, &token); err != nil {
		return token, errors.New("OAuth token endpoint returned invalid JSON")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || token.Error != "" {
		message := token.Error
		if token.ErrorDescription != "" {
			message += ": " + token.ErrorDescription
		}
		if message == "" {
			message = response.Status
		}
		return token, errors.New(message)
	}
	if token.AccessToken == "" {
		return token, errors.New("OAuth token response omitted access_token")
	}
	if token.ExpiresIn <= 0 {
		token.ExpiresIn = 300
	}
	return token, nil
}

func credentialFromToken(token tokenResponse, tokenEndpoint, clientID string) oauthCredential {
	return oauthCredential{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ExpiresAt:     time.Now().Add(time.Duration(token.ExpiresIn) * time.Second),
		TokenEndpoint: tokenEndpoint, ClientID: clientID, Scope: token.Scope,
	}
}

func deviceAuthorizationLogin(discovery oidcDiscovery, clientID, audience, scope string, launch bool) (oauthCredential, error) {
	form := url.Values{"client_id": {clientID}, "scope": {scope}}
	if audience != "" {
		form.Set("audience", audience)
	}
	req, err := http.NewRequest(http.MethodPost, discovery.DeviceAuthorizationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthCredential{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return oauthCredential{}, err
	}
	defer response.Body.Close()
	var device struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&device); err != nil {
		return oauthCredential{}, errors.New("device authorization endpoint returned invalid JSON")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || device.DeviceCode == "" || device.VerificationURI == "" {
		return oauthCredential{}, errors.New("device authorization request was rejected")
	}
	verification := device.VerificationURIComplete
	if verification == "" {
		verification = device.VerificationURI
	}
	fmt.Printf("Open %s\nEnter code: %s\n", device.VerificationURI, device.UserCode)
	if launch {
		_ = openBrowser(verification)
	}
	interval := time.Duration(device.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		token, tokenErr := requestOAuthToken(http.DefaultClient, discovery.TokenEndpoint, clientID, "", url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {device.DeviceCode},
			"client_id":   {clientID},
		})
		if tokenErr == nil {
			return credentialFromToken(token, discovery.TokenEndpoint, clientID), nil
		}
		if strings.HasPrefix(tokenErr.Error(), "authorization_pending") {
			continue
		}
		if strings.HasPrefix(tokenErr.Error(), "slow_down") {
			interval += 5 * time.Second
			continue
		}
		return oauthCredential{}, tokenErr
	}
	return oauthCredential{}, errors.New("device authorization expired")
}

func pkceLogin(discovery oidcDiscovery, clientID, audience, scope string, launch bool) (oauthCredential, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return oauthCredential{}, err
	}
	defer listener.Close()
	redirectURI := "http://" + listener.Addr().String() + "/callback"
	state, err := randomURLToken(32)
	if err != nil {
		return oauthCredential{}, err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return oauthCredential{}, err
	}
	challengeDigest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeDigest[:])
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	if audience != "" {
		query.Set("audience", audience)
	}
	authorizeURL := discovery.AuthorizationEndpoint + "?" + query.Encode()
	type callback struct{ code, state, oauthError string }
	callbackCh := make(chan callback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "<!doctype html><title>Agentic Registry</title><p>Authentication received. You may close this window.</p>")
		select {
		case callbackCh <- callback{code: r.URL.Query().Get("code"), state: r.URL.Query().Get("state"), oauthError: r.URL.Query().Get("error")}:
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())
	fmt.Printf("Open %s\n", authorizeURL)
	if launch {
		_ = openBrowser(authorizeURL)
	}
	select {
	case result := <-callbackCh:
		if result.oauthError != "" {
			return oauthCredential{}, fmt.Errorf("authorization failed: %s", result.oauthError)
		}
		if result.state != state || result.code == "" {
			return oauthCredential{}, errors.New("authorization callback state mismatch")
		}
		token, err := requestOAuthToken(http.DefaultClient, discovery.TokenEndpoint, clientID, "", url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {result.code},
			"redirect_uri":  {redirectURI},
			"client_id":     {clientID},
			"code_verifier": {verifier},
		})
		if err != nil {
			return oauthCredential{}, err
		}
		return credentialFromToken(token, discovery.TokenEndpoint, clientID), nil
	case <-time.After(5 * time.Minute):
		return oauthCredential{}, errors.New("authorization timed out")
	}
}

func randomURLToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validateOAuthEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("OAuth endpoint must be an absolute URL without user info")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost") {
		return nil
	}
	return errors.New("OAuth endpoint must use HTTPS outside loopback")
}

func openBrowser(target string) error {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(configDir(), ".oauth-redirect-*.html")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return err
	}
	document := `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=` + html.EscapeString(target) + `"><title>Agentic Registry sign in</title>`
	if _, err := io.WriteString(file, document); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	go func() {
		time.Sleep(30 * time.Second)
		_ = os.Remove(name)
	}()
	fileURL := (&url.URL{Scheme: "file", Path: name}).String()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", fileURL)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", fileURL)
	default:
		command = exec.Command("xdg-open", fileURL)
	}
	return command.Start()
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
