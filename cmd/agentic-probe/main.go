// Command agentic-probe observes what every routed MCP server actually serves
// and records it on the registry artifact's status. It runs on a schedule, in
// the cluster, and reaches servers only through the gateway — so a route the
// export renders but the gateway cannot answer is reported unreachable rather
// than assumed healthy. Credentials arrive via the environment, populated from
// a secret manager.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tesserix/agentic-registry/internal/probe"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	registryEndpoint := os.Getenv("REGISTRY_ENDPOINT")
	gatewayURL := os.Getenv("GATEWAY_URL")
	namespaces := splitList(os.Getenv("TENANT_NAMESPACES"))
	if registryEndpoint == "" || gatewayURL == "" || len(namespaces) == 0 {
		log.Error("REGISTRY_ENDPOINT, GATEWAY_URL and TENANT_NAMESPACES are required")
		os.Exit(2)
	}

	timeout := 30 * time.Second
	if raw := os.Getenv("PROBE_TIMEOUT"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			timeout = parsed
		}
	}

	runner := probe.Runner{
		Registry: probe.HTTPRegistry{
			Endpoint: strings.TrimSuffix(registryEndpoint, "/"),
			Token:    os.Getenv("REGISTRY_DEPLOY_KEY"),
			HTTP:     &http.Client{Timeout: timeout},
		},
		GatewayURL: strings.TrimSuffix(gatewayURL, "/"),
		HTTP:       &http.Client{Timeout: timeout},
	}
	if id := os.Getenv("MCP_CLIENT_ID"); id != "" {
		runner.Token = probe.ClientCredentials{
			TokenURL:     os.Getenv("MCP_TOKEN_URL"),
			ClientID:     id,
			ClientSecret: os.Getenv("MCP_CLIENT_SECRET"),
			Scope:        os.Getenv("MCP_SCOPE"),
			HTTP:         &http.Client{Timeout: timeout},
		}.Token
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	summary, err := runner.Run(ctx, namespaces)
	if err != nil {
		log.Error("probe run failed", "err", err, "probed", summary.Probed)
		os.Exit(1)
	}
	log.Info("probe run complete",
		"probed", summary.Probed,
		"unreachable", summary.Unreachable,
		"statusWriteFailures", summary.Failed)
	// An unreachable server is a finding to alert on, not a broken run; a status
	// write that never landed means the finding was lost, which is.
	if summary.Failed > 0 {
		os.Exit(1)
	}
}

func splitList(raw string) []string {
	out := []string{}
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
