package gatewaysync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestDynamicKubernetesAppliesWithServerSideOwnership(t *testing.T) {
	t.Parallel()

	desired := testBackend("desired", false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method: got %s, want PATCH", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/apply-patch+yaml" {
			t.Errorf("content type: got %q", got)
		}
		if got := r.URL.Query().Get("fieldManager"); got != "agentgateway-registry-sync" {
			t.Errorf("field manager: got %q", got)
		}
		if got := r.URL.Query().Get("force"); got != "true" {
			t.Errorf("force: got %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var object map[string]any
		if err := json.Unmarshal(body, &object); err != nil {
			t.Errorf("decode apply body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(desired.Object)
	}))
	t.Cleanup(server.Close)

	dynamicClient, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewDynamicKubernetes(dynamicClient, "agentgateway-registry-sync")
	if err != nil {
		t.Fatal(err)
	}
	resource := Resource{Object: desired, GVR: allowedResources[desired.GroupVersionKind()]}
	applied, err := client.Apply(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	if applied.GetName() != desired.GetName() {
		t.Fatalf("applied name: got %q", applied.GetName())
	}
}

func TestDynamicKubernetesPrunesOnlySelectedObjectVersion(t *testing.T) {
	t.Parallel()

	gvr := schema.GroupVersionResource{Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaybackends"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if got := r.URL.Query().Get("labelSelector"); got != managedByLabel+"="+managedByValue {
				t.Errorf("label selector: got %q", got)
			}
			_, _ = io.WriteString(w, `{"apiVersion":"agentgateway.dev/v1alpha1","kind":"AgentgatewayBackendList","items":[]}`)
		case http.MethodDelete:
			var options struct {
				Preconditions struct {
					UID             string `json:"uid"`
					ResourceVersion string `json:"resourceVersion"`
				} `json:"preconditions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
				t.Errorf("decode delete options: %v", err)
			}
			if options.Preconditions.UID != "object-uid" || options.Preconditions.ResourceVersion != "42" {
				t.Errorf("delete preconditions: got %#v", options.Preconditions)
			}
			_, _ = io.WriteString(w, `{"apiVersion":"v1","kind":"Status","status":"Success"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	dynamicClient, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewDynamicKubernetes(dynamicClient, "agentgateway-registry-sync")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListManaged(context.Background(), gvr, "agentgateway-system"); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), gvr, "agentgateway-system", "stale", types.UID("object-uid"), "42"); err != nil {
		t.Fatal(err)
	}
}
