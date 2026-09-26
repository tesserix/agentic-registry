// Package identity delegates tenant and OAuth credential lifecycle to the
// platform identity control plane. Registry remains an OAuth resource server:
// it neither mints credentials nor persists their secrets or verifiers.
package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

var ErrNotConfigured = errors.New("identity control plane is not configured")

type Tenant struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Namespace string `json:"namespace"`
	State     string `json:"state"`
}

type OnboardingRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type Credential struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	ClientID   string    `json:"client_id"`
	Scopes     []string  `json:"scopes,omitempty"`
	Namespaces []string  `json:"namespaces,omitempty"`
	Kinds      []string  `json:"kinds,omitempty"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

type CredentialSecret struct {
	Credential
	ClientSecret string `json:"client_secret"`
}

func (c *CredentialSecret) UnmarshalJSON(raw []byte) error {
	var response struct {
		Credential   Credential `json:"credential"`
		ClientSecret string     `json:"client_secret"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	c.Credential = response.Credential
	c.ClientSecret = response.ClientSecret
	return nil
}

func (c CredentialSecret) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Credential
		ClientSecret string `json:"client_secret"`
	}{Credential: c.Credential, ClientSecret: c.ClientSecret})
}

type CreateCredentialRequest struct {
	Name         string   `json:"name"`
	Scopes       []string `json:"scopes"`
	LifetimeDays int      `json:"lifetime_days"`
	Namespaces   []string `json:"namespaces"`
	Kinds        []string `json:"kinds"`
}

type ControlPlane interface {
	Onboard(context.Context, string, string, OnboardingRequest) (Tenant, error)
	ListCredentials(context.Context, string, string) ([]Credential, error)
	CreateCredential(context.Context, string, string, string, CreateCredentialRequest) (CredentialSecret, error)
	RotateCredential(context.Context, string, string, string, string) (CredentialSecret, error)
	RevokeCredential(context.Context, string, string, string) error
}

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

type HTTPControlPlane struct {
	base   string
	client *http.Client
}

func NewHTTPControlPlane(base string, client *http.Client) (*HTTPControlPlane, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return nil, fmt.Errorf("identity control plane URL must be an absolute HTTP(S) URL without user info")
	}
	internalService := strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".svc.cluster.local")
	if parsed.Scheme == "http" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && !internalService {
		return nil, fmt.Errorf("identity control plane URL must use HTTPS outside loopback")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPControlPlane{base: strings.TrimRight(parsed.String(), "/"), client: client}, nil
}

func (c *HTTPControlPlane) Onboard(ctx context.Context, token, idempotencyKey string, input OnboardingRequest) (Tenant, error) {
	var tenant Tenant
	err := c.do(ctx, http.MethodPost, "/v1/registry/tenants", token, "", idempotencyKey, input, &tenant)
	return tenant, err
}

func (c *HTTPControlPlane) ListCredentials(ctx context.Context, token, tenant string) ([]Credential, error) {
	var response struct {
		Credentials []Credential `json:"credentials"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/registry/credentials", token, tenant, "", nil, &response)
	return response.Credentials, err
}

func (c *HTTPControlPlane) CreateCredential(ctx context.Context, token, tenant, idempotencyKey string, input CreateCredentialRequest) (CredentialSecret, error) {
	var credential CredentialSecret
	err := c.do(ctx, http.MethodPost, "/v1/registry/credentials", token, tenant, idempotencyKey, input, &credential)
	return credential, err
}

func (c *HTTPControlPlane) RotateCredential(ctx context.Context, token, tenant, id, idempotencyKey string) (CredentialSecret, error) {
	var credential CredentialSecret
	path := "/v1/registry/credentials/" + url.PathEscape(id) + "/rotate"
	err := c.do(ctx, http.MethodPost, path, token, tenant, idempotencyKey, struct{}{}, &credential)
	return credential, err
}

func (c *HTTPControlPlane) RevokeCredential(ctx context.Context, token, tenant, id string) error {
	path := "/v1/registry/credentials/" + url.PathEscape(id)
	return c.do(ctx, http.MethodDelete, path, token, tenant, "", nil, nil)
}

func (c *HTTPControlPlane) do(
	ctx context.Context,
	method, path, token, tenant, idempotencyKey string,
	input, output any,
) error {
	if strings.TrimSpace(token) == "" {
		return &APIError{Status: http.StatusUnauthorized, Code: "unauthenticated", Message: "actor access token is required"}
	}
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode identity request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("build identity request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tenant != "" {
		req.Header.Set("X-Tesserix-Expected-Tenant", tenant)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("identity control plane unavailable: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read identity response: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return fmt.Errorf("identity response exceeds 1 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var public struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Detail  string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &public)
		if public.Message == "" {
			public.Message = public.Detail
		}
		if public.Message == "" {
			public.Message = http.StatusText(response.StatusCode)
		}
		return &APIError{Status: response.StatusCode, Code: public.Code, Message: public.Message}
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode identity response: %w", err)
	}
	return nil
}

type DisabledControlPlane struct{}

func (DisabledControlPlane) Onboard(context.Context, string, string, OnboardingRequest) (Tenant, error) {
	return Tenant{}, ErrNotConfigured
}
func (DisabledControlPlane) ListCredentials(context.Context, string, string) ([]Credential, error) {
	return nil, ErrNotConfigured
}
func (DisabledControlPlane) CreateCredential(context.Context, string, string, string, CreateCredentialRequest) (CredentialSecret, error) {
	return CredentialSecret{}, ErrNotConfigured
}
func (DisabledControlPlane) RotateCredential(context.Context, string, string, string, string) (CredentialSecret, error) {
	return CredentialSecret{}, ErrNotConfigured
}
func (DisabledControlPlane) RevokeCredential(context.Context, string, string, string) error {
	return ErrNotConfigured
}
