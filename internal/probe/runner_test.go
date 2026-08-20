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

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

type recordedStatus struct {
	namespace string
	name      string
	obs       Observation
}

type fakeRegistry struct {
	servers  map[string][]v1alpha1.Object
	recorded []recordedStatus
	putErr   error
}

func (f *fakeRegistry) ListMCPServers(_ context.Context, namespace string) ([]v1alpha1.Object, error) {
	return f.servers[namespace], nil
}

func (f *fakeRegistry) PutStatus(_ context.Context, namespace, name string, obs Observation) error {
	f.recorded = append(f.recorded, recordedStatus{namespace, name, obs})
	return f.putErr
}

func mcpServer(namespace, name, tenant string, tools ...string) v1alpha1.Object {
	obj := v1alpha1.Object{
		Kind:     v1alpha1.KindMCPServer,
		Metadata: v1alpha1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: map[string]interface{}{
			"name":    name,
			"remotes": []interface{}{map[string]interface{}{"type": "streamableHttp", "url": "http://" + name + ":8765/mcp"}},
		},
	}
	if tenant != "" {
		obj.Metadata.Labels = map[string]string{"mcp.tesserix.app/tenant": tenant}
	}
	if len(tools) > 0 {
		list := make([]interface{}, 0, len(tools))
		for _, t := range tools {
			list = append(list, t)
		}
		obj.Spec["tools"] = list
	}
	return obj
}

// The prober must dial the same tenant-scoped path the gateway serves, so a
// route that exists only in the export is never reported healthy.
func TestRunner_ProbesTheGatewayPathForEachServer(t *testing.T) {
	var paths []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		(&fakeMCP{}).ServeHTTP(w, r)
	}))
	defer gateway.Close()

	registry := &fakeRegistry{servers: map[string][]v1alpha1.Object{
		"devai":    {mcpServer("devai", "devai-mcp", "")},
		"homechef": {mcpServer("homechef", "homechef-mcp", "homechef")},
	}}
	runner := Runner{Registry: registry, GatewayURL: gateway.URL, HTTP: gateway.Client(), Now: fixedNow}

	summary, err := runner.Run(context.Background(), []string{"devai", "homechef"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Probed != 2 || summary.Unreachable != 0 {
		t.Errorf("summary: %+v", summary)
	}
	joined := strings.Join(paths, " ")
	for _, want := range []string{"/mcp/devai/devai-mcp", "/mcp/homechef/homechef-mcp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("probe never dialled %s (paths: %v)", want, paths)
		}
	}
	if len(registry.recorded) != 2 {
		t.Fatalf("want a status write per server, got %d", len(registry.recorded))
	}
}

func TestRunner_DirectoryEntriesAreNotProbed(t *testing.T) {
	gateway := httptest.NewServer(&fakeMCP{})
	defer gateway.Close()

	directory := mcpServer("devai", "catalog-slack-mcp", "")
	directory.Spec["catalog"] = true
	registry := &fakeRegistry{servers: map[string][]v1alpha1.Object{
		"devai": {directory, mcpServer("devai", "devai-mcp", "")},
	}}
	runner := Runner{Registry: registry, GatewayURL: gateway.URL, HTTP: gateway.Client(), Now: fixedNow}

	summary, err := runner.Run(context.Background(), []string{"devai"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Probed != 1 {
		t.Errorf("a directory entry has no platform credential and must not be probed: %+v", summary)
	}
	if registry.recorded[0].name != "devai-mcp" {
		t.Errorf("probed %s", registry.recorded[0].name)
	}
}

func TestRunner_UnreachableServerIsRecordedAndDoesNotStopTheRun(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "broken") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		(&fakeMCP{}).ServeHTTP(w, r)
	}))
	defer gateway.Close()

	registry := &fakeRegistry{servers: map[string][]v1alpha1.Object{
		"devai": {mcpServer("devai", "broken-mcp", ""), mcpServer("devai", "devai-mcp", "")},
	}}
	runner := Runner{Registry: registry, GatewayURL: gateway.URL, HTTP: gateway.Client(), Now: fixedNow}

	summary, err := runner.Run(context.Background(), []string{"devai"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Probed != 2 || summary.Unreachable != 1 {
		t.Errorf("summary: %+v", summary)
	}
	for _, rec := range registry.recorded {
		if rec.name == "broken-mcp" && rec.obs.Reachable {
			t.Error("a 502 must be recorded as unreachable")
		}
	}
}

func TestRunner_TokenIsFetchedOncePerRun(t *testing.T) {
	gateway := httptest.NewServer(&fakeMCP{})
	defer gateway.Close()

	calls := 0
	registry := &fakeRegistry{servers: map[string][]v1alpha1.Object{
		"devai": {mcpServer("devai", "a-mcp", ""), mcpServer("devai", "b-mcp", "")},
	}}
	runner := Runner{
		Registry:   registry,
		GatewayURL: gateway.URL,
		HTTP:       gateway.Client(),
		Now:        fixedNow,
		Token: func(context.Context) (string, error) {
			calls++
			return "tok", nil
		},
	}
	if _, err := runner.Run(context.Background(), []string{"devai"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("want one token exchange per run, got %d", calls)
	}
}

func TestHTTPRegistry_ListAndPutStatus(t *testing.T) {
	var putBody string
	var putPath string
	registrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			putBody, putPath = string(body), r.URL.Path+"?"+r.URL.RawQuery
			if r.Header.Get("Authorization") != "Bearer deploy-key" {
				t.Errorf("status write must carry the deploy key: %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode([]v1alpha1.Object{mcpServer("devai", "devai-mcp", "")})
	}))
	defer registrySrv.Close()

	client := HTTPRegistry{Endpoint: registrySrv.URL, Token: "deploy-key", HTTP: registrySrv.Client()}

	servers, err := client.ListMCPServers(context.Background(), "devai")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Metadata.Name != "devai-mcp" {
		t.Fatalf("list: %+v", servers)
	}

	if err := client.PutStatus(context.Background(), "devai", "devai-mcp", Observation{Reachable: true, Tools: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(putPath, "/v0/mcpservers/devai-mcp/status") || !strings.Contains(putPath, "namespace=devai") {
		t.Errorf("status path: %s", putPath)
	}
	if !strings.Contains(putBody, `"reachable":true`) {
		t.Errorf("status body: %s", putBody)
	}
}

func fixedNow() time.Time { return probedAt }
