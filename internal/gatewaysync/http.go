package gatewaysync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewStatusHTTPHandler exposes health, readiness, status, and Prometheus metrics.
func NewStatusHTTPHandler(status *Status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !status.Snapshot().Ready {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status.Snapshot()); err != nil {
			http.Error(w, "encode status", http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(prometheusMetrics(status.Snapshot())))
	})
	return mux
}

func prometheusMetrics(state State) string {
	var output strings.Builder
	writeGauge := func(name, help string, value any) {
		fmt.Fprintf(&output, "# HELP %s %s\n# TYPE %s gauge\n%s %v\n", name, help, name, name, value)
	}
	writeCounter := func(name, help string, value uint64) {
		fmt.Fprintf(&output, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, value)
	}
	writeGauge("agentgateway_sync_leader", "Whether this replica owns the reconciliation lease.", boolFloat(state.Leader))
	writeGauge("agentgateway_sync_ready", "Whether this replica initialized successfully.", boolFloat(state.Ready))
	writeGauge("agentgateway_sync_desired_resources", "Resources in the last verified Registry snapshot.", state.DesiredResources)
	writeGauge("agentgateway_sync_actual_resources", "Registry-owned resources observed in Kubernetes.", state.ActualResources)
	writeGauge("agentgateway_sync_drift_resources", "Desired resources missing, stale, or divergent.", state.DriftResources)
	writeGauge("agentgateway_sync_last_registry_success_timestamp_seconds", "Unix time of the last successful Registry fetch.", unixSeconds(state.LastRegistrySuccess))
	writeGauge("agentgateway_sync_last_success_timestamp_seconds", "Unix time of the last successful reconciliation.", unixSeconds(state.LastReconcileSuccess))
	writeCounter("agentgateway_sync_registry_errors_total", "Registry snapshot fetch and validation errors.", state.RegistryErrorsTotal)
	writeCounter("agentgateway_sync_reconcile_errors_total", "Kubernetes reconciliation errors.", state.ReconcileErrorsTotal)
	writeCounter("agentgateway_sync_reconcile_successes_total", "Successful Kubernetes reconciliations.", state.ReconcileSuccessesTotal)
	fmt.Fprintf(&output, "# HELP agentgateway_sync_mode_info Active controller mode.\n# TYPE agentgateway_sync_mode_info gauge\nagentgateway_sync_mode_info{mode=%q} 1\n", state.Mode)
	return output.String()
}

func boolFloat(value bool) int {
	if value {
		return 1
	}
	return 0
}

func unixSeconds(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}
