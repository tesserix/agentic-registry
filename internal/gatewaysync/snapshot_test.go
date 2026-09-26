package gatewaysync

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRegistryClientFetchesVerifiedSnapshot(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata:
  name: devai-test
  namespace: agentgateway-system
  labels:
    app.kubernetes.io/managed-by: agentic-registry
spec:
  a2a:
    host: test.devai.svc.cluster.local
    port: 8080
`)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-read-token" {
			t.Errorf("authorization: got %q", got)
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("ETag", `"`+digest+`"`)
		w.Header().Set("X-Agentgateway-Resource-Count", "1")
		w.Header().Set("X-Agentgateway-Resource-Digest", digest)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-read-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewRegistryClient(RegistryClientOptions{
		URL:             server.URL,
		TokenFile:       tokenFile,
		TargetNamespace: "agentgateway-system",
		MinResources:    1,
		MaxBodyBytes:    1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, modified, err := client.Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Fatal("first fetch reported not modified")
	}
	if snapshot.Digest != digest {
		t.Fatalf("digest: got %q, want %q", snapshot.Digest, digest)
	}
	if snapshot.ETag != `"`+digest+`"` {
		t.Fatalf("etag: got %q", snapshot.ETag)
	}
	if len(snapshot.Resources) != 1 {
		t.Fatalf("resources: got %d", len(snapshot.Resources))
	}
	resource := snapshot.Resources[0]
	if resource.Object.GetKind() != "AgentgatewayBackend" || resource.Object.GetName() != "devai-test" {
		t.Fatalf("resource: got %s/%s", resource.Object.GetKind(), resource.Object.GetName())
	}
}

func TestRegistryClientAcceptsVerifiedNotModifiedSnapshot(t *testing.T) {
	t.Parallel()

	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("snapshot")))
	etag := `"` + digest + `"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != etag {
			t.Errorf("if-none-match: got %q, want %q", got, etag)
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Agentgateway-Resource-Count", "27")
		w.Header().Set("X-Agentgateway-Resource-Digest", digest)
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(server.Close)

	client := newTestRegistryClient(t, server.URL, 27, 1<<20)
	snapshot, modified, err := client.Fetch(context.Background(), etag)
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Fatal("conditional fetch reported modified")
	}
	if snapshot.ETag != etag || snapshot.Digest != digest || snapshot.ResourceCount != 27 {
		t.Fatalf("snapshot metadata: got %#v", snapshot)
	}
}

func newTestRegistryClient(t *testing.T, endpoint string, minResources int, maxBodyBytes int64) *RegistryClient {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-read-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewRegistryClient(RegistryClientOptions{
		URL:             endpoint,
		TokenFile:       tokenFile,
		TargetNamespace: "agentgateway-system",
		MinResources:    minResources,
		MaxBodyBytes:    maxBodyBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRegistryClientRejectsInvalidSnapshots(t *testing.T) {
	t.Parallel()

	valid := `apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata:
  name: devai-test
  namespace: agentgateway-system
  labels:
    app.kubernetes.io/managed-by: agentic-registry
spec: {}
`
	tests := []struct {
		name           string
		body           string
		count          int
		minResources   int
		maxBodyBytes   int64
		digestOverride string
		wantError      string
	}{
		{
			name:           "digest mismatch",
			body:           valid,
			count:          1,
			minResources:   1,
			maxBodyBytes:   1 << 20,
			digestOverride: "sha256:" + strings.Repeat("0", 64),
			wantError:      "digest mismatch",
		},
		{
			name:         "resource count below floor",
			body:         valid,
			count:        1,
			minResources: 2,
			maxBodyBytes: 1 << 20,
			wantError:    "minimum is 2",
		},
		{
			name:         "oversized response",
			body:         valid,
			count:        1,
			minResources: 1,
			maxBodyBytes: int64(len(valid) - 1),
			wantError:    "exceeds",
		},
		{
			name:         "wrong namespace",
			body:         strings.Replace(valid, "namespace: agentgateway-system", "namespace: other", 1),
			count:        1,
			minResources: 1,
			maxBodyBytes: 1 << 20,
			wantError:    `targets namespace "other"`,
		},
		{
			name: "disallowed kind",
			body: `apiVersion: v1
kind: ConfigMap
metadata:
  name: devai-test
  namespace: agentgateway-system
  labels:
    app.kubernetes.io/managed-by: agentic-registry
`,
			count:        1,
			minResources: 1,
			maxBodyBytes: 1 << 20,
			wantError:    "disallowed resource",
		},
		{
			name:         "missing ownership label",
			body:         strings.Replace(valid, "  labels:\n    app.kubernetes.io/managed-by: agentic-registry\n", "", 1),
			count:        1,
			minResources: 1,
			maxBodyBytes: 1 << 20,
			wantError:    "lacks registry ownership",
		},
		{
			name:         "duplicate resource",
			body:         valid + "---\n" + valid,
			count:        2,
			minResources: 1,
			maxBodyBytes: 1 << 20,
			wantError:    "duplicate resource",
		},
		{
			name:         "status in desired state",
			body:         valid + "status:\n  accepted: true\n",
			count:        1,
			minResources: 1,
			maxBodyBytes: 1 << 20,
			wantError:    "contains status",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(test.body)
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
			if test.digestOverride != "" {
				digest = test.digestOverride
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("ETag", `"`+digest+`"`)
				w.Header().Set("X-Agentgateway-Resource-Count", fmt.Sprint(test.count))
				w.Header().Set("X-Agentgateway-Resource-Digest", digest)
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)

			client := newTestRegistryClient(t, server.URL, test.minResources, test.maxBodyBytes)
			_, _, err := client.Fetch(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error: got %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestRegistryClientRefusesRedirects(t *testing.T) {
	t.Parallel()

	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Add(1)
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-read-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewRegistryClient(RegistryClientOptions{
		URL:             redirect.URL,
		TokenFile:       tokenFile,
		TargetNamespace: "agentgateway-system",
		MinResources:    1,
		MaxBodyBytes:    1 << 20,
		HTTPClient:      &http.Client{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Fetch(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("error: got %v, want HTTP 307", err)
	}
	if followed.Load() != 0 {
		t.Fatal("registry client followed a redirect")
	}
}

func TestNewRegistryClientRejectsNonHTTPURL(t *testing.T) {
	t.Parallel()

	_, err := NewRegistryClient(RegistryClientOptions{
		URL:             "file:///var/run/secrets/registry/API_KEY",
		TokenFile:       "/token",
		TargetNamespace: "agentgateway-system",
		MinResources:    1,
		MaxBodyBytes:    1024,
	})
	if err == nil {
		t.Fatal("non-HTTP Registry URL was accepted")
	}
}
