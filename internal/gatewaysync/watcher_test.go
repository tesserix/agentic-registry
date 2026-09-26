package gatewaysync

import "testing"

func TestDriftRelevantChangeIgnoresStatusButDetectsDesiredFields(t *testing.T) {
	t.Parallel()

	old := testBackend("desired", false)
	statusOnly := old.DeepCopy()
	statusOnly.Object["status"] = map[string]any{
		"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}},
	}
	statusOnly.SetResourceVersion("2")
	if driftRelevantChange(old, statusOnly) {
		t.Fatal("status-only update was classified as desired-state drift")
	}

	specChange := statusOnly.DeepCopy()
	specChange.Object["spec"].(map[string]any)["a2a"].(map[string]any)["host"] = "changed.example"
	specChange.SetGeneration(2)
	if !driftRelevantChange(statusOnly, specChange) {
		t.Fatal("spec update was not classified as desired-state drift")
	}

	ownershipChange := statusOnly.DeepCopy()
	ownershipChange.SetLabels(map[string]string{})
	if !driftRelevantChange(statusOnly, ownershipChange) {
		t.Fatal("ownership-label removal was not classified as desired-state drift")
	}
}
