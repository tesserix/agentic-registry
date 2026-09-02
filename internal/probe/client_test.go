package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeMCP answers server/discover and tools/list the way a streamable-HTTP MCP server
// does, recording what it was sent.
type fakeMCP struct {
	sse              bool
	methods          []string
	auth             string
	protocolHeaders  []string
	methodHeaders    []string
	requestProtocols []string
	clientNames      []string
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Method string `json:"method"`
		ID     any    `json:"id"`
		Params struct {
			Meta struct {
				ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
				ClientInfo      struct {
					Name string `json:"name"`
				} `json:"io.modelcontextprotocol/clientInfo"`
			} `json:"_meta"`
		} `json:"params"`
	}
	_ = json.Unmarshal(body, &req)
	f.methods = append(f.methods, req.Method)
	f.auth = r.Header.Get("Authorization")
	f.protocolHeaders = append(f.protocolHeaders, r.Header.Get("MCP-Protocol-Version"))
	f.methodHeaders = append(f.methodHeaders, r.Header.Get("MCP-Method"))
	f.requestProtocols = append(f.requestProtocols, req.Params.Meta.ProtocolVersion)
	f.clientNames = append(f.clientNames, req.Params.Meta.ClientInfo.Name)
	if r.Header.Get("Mcp-Session-Id") != "" {
		http.Error(w, "sessions are forbidden", http.StatusBadRequest)
		return
	}

	var result string
	switch req.Method {
	case "server/discover":
		result = `{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}`
	case "tools/list":
		result = `{"tools":[{"name":"track_delivery"},{"name":"get_order_status"}]}`
	default:
		w.WriteHeader(http.StatusAccepted)
		return
	}
	payload := `{"jsonrpc":"2.0","id":1,"result":` + result + `}`
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message\ndata: "+payload+"\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, payload)
}

func TestProbe_CollectsToolsOverJSON(t *testing.T) {
	fake := &fakeMCP{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	obs := Probe(context.Background(), srv.Client(), srv.URL, "token-abc", time.Now())

	if !obs.Reachable {
		t.Fatalf("want reachable, got error %q", obs.Error)
	}
	if len(obs.Tools) != 2 || obs.Tools[0] != "track_delivery" {
		t.Errorf("tools: got %v", obs.Tools)
	}
	if obs.ProtocolVersion != "2026-07-28" {
		t.Errorf("protocolVersion: got %q", obs.ProtocolVersion)
	}
	if fake.auth != "Bearer token-abc" {
		t.Errorf("probe must present its token: got %q", fake.auth)
	}
	if len(fake.methods) != 2 || fake.methods[0] != "server/discover" || fake.methods[1] != "tools/list" {
		t.Errorf("stateless probe order: got %v", fake.methods)
	}
	for i, method := range fake.methods {
		if fake.protocolHeaders[i] != "2026-07-28" || fake.requestProtocols[i] != "2026-07-28" {
			t.Errorf("request %d protocol metadata: header=%q body=%q", i, fake.protocolHeaders[i], fake.requestProtocols[i])
		}
		if fake.methodHeaders[i] != method {
			t.Errorf("request %d MCP-Method=%q want %q", i, fake.methodHeaders[i], method)
		}
		if fake.clientNames[i] != "agentic-registry-prober" {
			t.Errorf("request %d clientInfo.name=%q", i, fake.clientNames[i])
		}
	}
}

func TestProbe_RejectsServerWithoutModernStatelessRevision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"supportedVersions":["2025-11-25"],"capabilities":{},"serverInfo":{"name":"legacy","version":"1"}}}`)
	}))
	defer srv.Close()

	obs := Probe(context.Background(), srv.Client(), srv.URL, "t", time.Now())
	if obs.Reachable || !strings.Contains(obs.Error, "2026-07-28") {
		t.Fatalf("legacy-only server must not pass production probe: %+v", obs)
	}
}

func TestProbe_CollectsToolsOverSSEFraming(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{sse: true})
	defer srv.Close()

	obs := Probe(context.Background(), srv.Client(), srv.URL, "t", time.Now())
	if !obs.Reachable || len(obs.Tools) != 2 {
		t.Fatalf("sse response not parsed: %+v", obs)
	}
}

func TestProbe_UnreachableServerReportsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	obs := Probe(context.Background(), srv.Client(), srv.URL, "t", time.Now())
	if obs.Reachable {
		t.Fatal("503 must not count as reachable")
	}
	if !strings.Contains(obs.Error, "503") {
		t.Errorf("error must carry the status: got %q", obs.Error)
	}
}

func TestProbe_ForbiddenIsReportedNotSilentlyEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	obs := Probe(context.Background(), srv.Client(), srv.URL, "t", time.Now())
	if obs.Reachable || !strings.Contains(obs.Error, "403") {
		t.Errorf("got %+v", obs)
	}
}

func TestProbe_JSONRPCErrorIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no such method"}}`)
	}))
	defer srv.Close()

	obs := Probe(context.Background(), srv.Client(), srv.URL, "t", time.Now())
	if obs.Reachable || !strings.Contains(obs.Error, "no such method") {
		t.Errorf("got %+v", obs)
	}
}
