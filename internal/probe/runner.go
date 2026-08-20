package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/tesserix/agentic-registry/adapters/agentgateway"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// Registry is the catalog side of a probe run: which servers exist, and where
// their observations are recorded.
type Registry interface {
	ListMCPServers(ctx context.Context, namespace string) ([]v1alpha1.Object, error)
	PutStatus(ctx context.Context, namespace, name string, obs Observation) error
}

// Runner probes every routed MCP server in a set of tenant namespaces.
type Runner struct {
	Registry   Registry
	GatewayURL string
	HTTP       *http.Client
	Now        func() time.Time
	// Token returns the bearer the probe presents to the gateway. Nil probes
	// anonymously, which is only useful in tests.
	Token func(ctx context.Context) (string, error)
}

// Summary reports one run.
type Summary struct {
	Probed      int
	Unreachable int
	Failed      int // status writes that did not land
}

// Run probes each namespace's servers over the same paths the gateway serves,
// so a route the export renders but the gateway cannot answer shows up as
// unreachable rather than silently healthy.
func (r Runner) Run(ctx context.Context, namespaces []string) (Summary, error) {
	var summary Summary
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}

	bearer := ""
	if r.Token != nil {
		token, err := r.Token(ctx)
		if err != nil {
			return summary, fmt.Errorf("acquire probe token: %w", err)
		}
		bearer = token
	}

	for _, ns := range namespaces {
		servers, err := r.Registry.ListMCPServers(ctx, ns)
		if err != nil {
			return summary, fmt.Errorf("list %s: %w", ns, err)
		}
		routes, err := agentgateway.BuildRoutes(servers, agentgateway.Options{DefaultTenant: ns})
		if err != nil {
			return summary, fmt.Errorf("render routes for %s: %w", ns, err)
		}
		for _, route := range routes {
			obs := Probe(ctx, client, r.GatewayURL+route.Path, bearer, now())
			summary.Probed++
			if !obs.Reachable {
				summary.Unreachable++
			}
			if err := r.Registry.PutStatus(ctx, ns, route.ServerName, obs); err != nil {
				summary.Failed++
			}
		}
	}
	return summary, nil
}

// HTTPRegistry talks to the agentic-registry v0 API with a deploy key.
type HTTPRegistry struct {
	Endpoint string
	Token    string
	HTTP     *http.Client
}

func (h HTTPRegistry) ListMCPServers(ctx context.Context, namespace string) ([]v1alpha1.Object, error) {
	endpoint := fmt.Sprintf("%s/v0/mcpservers?namespace=%s&limit=500", h.Endpoint, url.QueryEscape(namespace))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	body, err := h.do(req)
	if err != nil {
		return nil, err
	}
	var servers []v1alpha1.Object
	if err := json.Unmarshal(body, &servers); err != nil {
		return nil, fmt.Errorf("decode mcpservers: %w", err)
	}
	return servers, nil
}

func (h HTTPRegistry) PutStatus(ctx context.Context, namespace, name string, obs Observation) error {
	payload, err := json.Marshal(map[string]interface{}{
		"reachable":       obs.Reachable,
		"tools":           obs.Tools,
		"protocolVersion": obs.ProtocolVersion,
		"error":           obs.Error,
	})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/v0/mcpservers/%s/status?namespace=%s",
		h.Endpoint, url.PathEscape(name), url.QueryEscape(namespace))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = h.do(req)
	return err
}

func (h HTTPRegistry) do(req *http.Request) ([]byte, error) {
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("registry %s %s: http %d: %s", req.Method, req.URL.Path, resp.StatusCode, bytes.TrimSpace(body))
	}
	return body, nil
}
