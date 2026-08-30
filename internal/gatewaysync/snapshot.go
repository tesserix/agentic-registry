package gatewaysync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "agentic-registry"
)

var allowedResources = map[schema.GroupVersionKind]schema.GroupVersionResource{
	{Group: "agentgateway.dev", Version: "v1alpha1", Kind: "AgentgatewayBackend"}: {
		Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaybackends",
	},
	{Group: "agentgateway.dev", Version: "v1alpha1", Kind: "AgentgatewayPolicy"}: {
		Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaypolicies",
	},
	{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}: {
		Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes",
	},
}

// Resource is one validated Kubernetes object from a Registry snapshot.
type Resource struct {
	Object *unstructured.Unstructured
	GVR    schema.GroupVersionResource
}

// Snapshot is a complete, content-addressed AgentGateway desired state.
type Snapshot struct {
	ETag          string
	Digest        string
	ResourceCount int
	Resources     []Resource
}

// RegistryClientOptions configures the Registry snapshot boundary.
type RegistryClientOptions struct {
	URL             string
	TokenFile       string
	TargetNamespace string
	MinResources    int
	MaxBodyBytes    int64
	HTTPClient      *http.Client
}

// RegistryClient fetches and validates AgentGateway snapshots.
type RegistryClient struct {
	url             string
	tokenFile       string
	targetNamespace string
	minResources    int
	maxBodyBytes    int64
	httpClient      *http.Client
}

// NewRegistryClient validates options and constructs a Registry client.
func NewRegistryClient(options RegistryClientOptions) (*RegistryClient, error) {
	parsed, err := url.Parse(options.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, errors.New("registry URL must be an http(s) URL without user info")
	}
	if options.TokenFile == "" {
		return nil, errors.New("registry token file is required")
	}
	if options.TargetNamespace == "" {
		return nil, errors.New("target namespace is required")
	}
	if options.MinResources < 1 {
		return nil, errors.New("minimum resource count must be positive")
	}
	if options.MaxBodyBytes < 1 {
		return nil, errors.New("maximum body size must be positive")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if options.HTTPClient != nil {
		copy := *options.HTTPClient
		client = &copy
		if client.Timeout <= 0 {
			client.Timeout = 30 * time.Second
		}
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &RegistryClient{
		url:             options.URL,
		tokenFile:       options.TokenFile,
		targetNamespace: options.TargetNamespace,
		minResources:    options.MinResources,
		maxBodyBytes:    options.MaxBodyBytes,
		httpClient:      client,
	}, nil
}

// Fetch returns a verified snapshot. modified is false for a valid 304.
func (c *RegistryClient) Fetch(ctx context.Context, etag string) (snapshot Snapshot, modified bool, err error) {
	token, err := c.readToken()
	if err != nil {
		return Snapshot{}, false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("build registry request: %w", err)
	}
	request.Header.Set("Accept", "application/yaml")
	request.Header.Set("Authorization", "Bearer "+token)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("fetch registry snapshot: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		count, digest, responseETag, err := c.metadata(response.Header)
		if err != nil {
			return Snapshot{}, false, fmt.Errorf("validate not-modified response: %w", err)
		}
		if etag == "" || responseETag != etag {
			return Snapshot{}, false, errors.New("registry returned an invalid not-modified response")
		}
		return Snapshot{ETag: etag, Digest: digest, ResourceCount: count}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, false, fmt.Errorf("registry snapshot returned HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxBodyBytes+1))
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("read registry snapshot: %w", err)
	}
	if int64(len(body)) > c.maxBodyBytes {
		return Snapshot{}, false, fmt.Errorf("registry snapshot exceeds %d bytes", c.maxBodyBytes)
	}
	return c.validate(body, response.Header)
}

func (c *RegistryClient) readToken() (string, error) {
	value, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("read registry token: %w", err)
	}
	if len(value) > 8192 {
		return "", errors.New("registry token exceeds 8192 bytes")
	}
	token := strings.TrimSpace(string(value))
	if token == "" {
		return "", errors.New("registry token is empty")
	}
	return token, nil
}

func (c *RegistryClient) validate(body []byte, header http.Header) (Snapshot, bool, error) {
	count, digest, etag, err := c.metadata(header)
	if err != nil {
		return Snapshot{}, false, err
	}
	actualDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	if digest != actualDigest {
		return Snapshot{}, false, errors.New("registry snapshot digest mismatch")
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(body), 4096)
	resources := make([]Resource, 0, count)
	seen := make(map[string]struct{}, count)
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return Snapshot{}, false, fmt.Errorf("decode registry snapshot: %w", err)
		}
		if len(object.Object) == 0 {
			continue
		}
		gvk := object.GroupVersionKind()
		gvr, ok := allowedResources[gvk]
		if !ok {
			return Snapshot{}, false, fmt.Errorf("registry snapshot contains disallowed resource %s", gvk.String())
		}
		if object.GetNamespace() != c.targetNamespace {
			return Snapshot{}, false, fmt.Errorf("registry snapshot resource %s/%s targets namespace %q", object.GetKind(), object.GetName(), object.GetNamespace())
		}
		if object.GetName() == "" {
			return Snapshot{}, false, fmt.Errorf("registry snapshot contains unnamed %s", object.GetKind())
		}
		if object.GetLabels()[managedByLabel] != managedByValue {
			return Snapshot{}, false, fmt.Errorf("registry snapshot resource %s/%s lacks registry ownership", object.GetKind(), object.GetName())
		}
		if _, exists := object.Object["status"]; exists {
			return Snapshot{}, false, fmt.Errorf("registry snapshot resource %s/%s contains status", object.GetKind(), object.GetName())
		}
		key := gvr.String() + "/" + object.GetName()
		if _, exists := seen[key]; exists {
			return Snapshot{}, false, fmt.Errorf("registry snapshot contains duplicate resource %s/%s", object.GetKind(), object.GetName())
		}
		seen[key] = struct{}{}
		resources = append(resources, Resource{Object: object, GVR: gvr})
	}
	if len(resources) != count {
		return Snapshot{}, false, fmt.Errorf("registry snapshot decoded %d resources, header declared %d", len(resources), count)
	}
	return Snapshot{ETag: etag, Digest: digest, ResourceCount: count, Resources: resources}, true, nil
}

func (c *RegistryClient) metadata(header http.Header) (int, string, string, error) {
	count, err := strconv.Atoi(header.Get("X-Agentgateway-Resource-Count"))
	if err != nil {
		return 0, "", "", errors.New("registry snapshot has an invalid resource count")
	}
	if count < c.minResources {
		return 0, "", "", fmt.Errorf("registry snapshot has %d resources, minimum is %d", count, c.minResources)
	}
	digest := header.Get("X-Agentgateway-Resource-Digest")
	encoded := strings.TrimPrefix(digest, "sha256:")
	if len(encoded) != sha256.Size*2 {
		return 0, "", "", errors.New("registry snapshot has an invalid digest")
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return 0, "", "", errors.New("registry snapshot has an invalid digest")
	}
	etag := `"` + digest + `"`
	if header.Get("ETag") != etag {
		return 0, "", "", errors.New("registry snapshot ETag mismatch")
	}
	return count, digest, etag, nil
}
