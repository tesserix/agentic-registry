package gatewaysync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatusHTTPHandlerExposesReadinessStateAndMetrics(t *testing.T) {
	t.Parallel()

	status := NewStatus(ModeShadow)
	status.setReady(true)
	status.SetLeader(true)
	status.recordRegistrySuccess(time.Unix(100, 0), Snapshot{
		ETag:          `"sha256:test"`,
		Digest:        "sha256:test",
		ResourceCount: 52,
	})
	status.recordReconcileSuccess(time.Unix(101, 0), Result{Desired: 52, Actual: 52, Drift: 0})
	handler := NewStatusHTTPHandler(status)

	for _, path := range []string{"/healthz", "/readyz"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status: got %d", path, response.Code)
		}
	}

	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/status", nil))
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("status endpoint: got %d", statusResponse.Code)
	}
	var state State
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Mode != ModeShadow || !state.Leader || state.ResourceCount != 52 {
		t.Fatalf("state: got %#v", state)
	}

	metricsResponse := httptest.NewRecorder()
	handler.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, metric := range []string{
		"agentgateway_sync_leader 1",
		"agentgateway_sync_ready 1",
		"agentgateway_sync_desired_resources 52",
		"agentgateway_sync_actual_resources 52",
		"agentgateway_sync_drift_resources 0",
		"agentgateway_sync_last_success_timestamp_seconds 101",
	} {
		if !strings.Contains(metricsResponse.Body.String(), metric) {
			t.Errorf("metrics missing %q:\n%s", metric, metricsResponse.Body.String())
		}
	}
}

func TestStatusHTTPHandlerReportsNotReady(t *testing.T) {
	t.Parallel()

	handler := NewStatusHTTPHandler(NewStatus(ModeActive))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status: got %d", response.Code)
	}
}
