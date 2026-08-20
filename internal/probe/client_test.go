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

// fakeMCP answers initialize/tools/list the way a streamable-HTTP MCP server
// does, recording what it was sent.
type fakeMCP struct {
	sse      bool
	sessions []string
	methods  []string
	auth     string
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Method string `json:"method"`
		ID     any    `json:"id"`
	}
	_ = json.Unmarshal(body, &req)
	f.methods = append(f.methods, req.Method)
	f.auth = r.Header.Get("Authorization")
	f.sessions = append(f.sessions, r.Header.Get("Mcp-Session-Id"))

	var result string
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-1")
		result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}`
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
	if obs.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion: got %q", obs.ProtocolVersion)
	}
	if fake.auth != "Bearer token-abc" {
		t.Errorf("probe must present its token: got %q", fake.auth)
	}
	if len(fake.methods) < 2 || fake.methods[0] != "initialize" {
		t.Errorf("handshake order: got %v", fake.methods)
	}
	// The session the server handed out has to come back on tools/list.
	if last := fake.sessions[len(fake.sessions)-1]; last != "sess-1" {
		t.Errorf("session id not echoed: got %q", last)
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
