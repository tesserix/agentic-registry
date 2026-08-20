package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// protocolVersion is the MCP revision the probe negotiates. A server that
// speaks a different one answers with its own, which is recorded as observed.
const protocolVersion = "2025-06-18"

const maxProbeResponse = 4 << 20

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Probe performs one MCP handshake against url and reports the tool surface it
// observed. Every failure mode is an unreachable observation rather than an
// error, so one broken server cannot fail a whole probe run.
func Probe(ctx context.Context, client *http.Client, url, bearer string, at time.Time) Observation {
	session := ""

	init, err := call(ctx, client, url, bearer, &session, "initialize", map[string]interface{}{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "agentic-registry-prober", "version": "1"},
	})
	if err != nil {
		return Observation{Error: err.Error(), ProbedAt: at}
	}
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(init, &initResult)

	if err := notify(ctx, client, url, bearer, session); err != nil {
		return Observation{Error: err.Error(), ProbedAt: at}
	}

	list, err := call(ctx, client, url, bearer, &session, "tools/list", map[string]interface{}{})
	if err != nil {
		return Observation{ProtocolVersion: initResult.ProtocolVersion, Error: err.Error(), ProbedAt: at}
	}
	var listResult struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(list, &listResult); err != nil {
		return Observation{ProtocolVersion: initResult.ProtocolVersion, Error: "tools/list: " + err.Error(), ProbedAt: at}
	}
	tools := make([]string, 0, len(listResult.Tools))
	for _, t := range listResult.Tools {
		tools = append(tools, t.Name)
	}
	return Observation{
		Reachable:       true,
		Tools:           tools,
		ProtocolVersion: initResult.ProtocolVersion,
		ProbedAt:        at,
	}
}

func call(ctx context.Context, client *http.Client, url, bearer string, session *string, method string, params map[string]interface{}) (json.RawMessage, error) {
	body, err := post(ctx, client, url, bearer, session, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	payload, err := decodeFrame(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, resp.Error.Message)
	}
	return resp.Result, nil
}

func notify(ctx context.Context, client *http.Client, url, bearer, session string) error {
	_, err := post(ctx, client, url, bearer, &session, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	if err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	return nil
}

func post(ctx context.Context, client *http.Client, url, bearer string, session *string, payload map[string]interface{}) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if session != nil && *session != "" {
		req.Header.Set("Mcp-Session-Id", *session)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if session != nil && *session == "" {
		if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
			*session = id
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxProbeResponse))
}

// decodeFrame accepts both transports a streamable-HTTP server may answer
// with: a bare JSON body, or one SSE event carrying the same JSON.
func decodeFrame(body []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	if trimmed[0] == '{' {
		return trimmed, nil
	}
	for _, line := range strings.Split(string(trimmed), "\n") {
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			return []byte(strings.TrimSpace(data)), nil
		}
	}
	return nil, fmt.Errorf("no JSON payload in response")
}
