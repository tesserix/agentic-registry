package activation

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func NewForMCPServer(object v1alpha1.Object, now time.Time) (Status, error) {
	if object.Kind != v1alpha1.KindMCPServer {
		return Status{}, errors.New("activation status is only defined for MCP servers")
	}
	artifactDigest, ok := publicationArtifactDigest(object.Spec)
	if !ok {
		return Status{}, errors.New("MCP server is missing immutable publication artifact digest")
	}
	return New(
		fmt.Sprintf("mcpservers/%s/%s@%s", object.Metadata.Namespace, object.Metadata.Name, object.Metadata.Tag),
		object.Digest(), artifactDigest, desiredState(object), 1, now,
	), nil
}

func DecodeDocument(document map[string]any) (Status, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return Status{}, fmt.Errorf("marshal activation status: %w", err)
	}
	var status Status
	if err := json.Unmarshal(encoded, &status); err != nil {
		return Status{}, fmt.Errorf("decode activation status: %w", err)
	}
	if status.SchemaVersion != "v1alpha1" || status.Generation < 1 || status.Ref == "" || status.RegistryDigest == "" || status.ArtifactDigest == "" || status.ObservedAt.IsZero() {
		return Status{}, errors.New("activation status is invalid")
	}
	return status, nil
}

func Document(status Status) (map[string]any, error) {
	encoded, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("marshal activation status: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decode activation status: %w", err)
	}
	return document, nil
}

func publicationArtifactDigest(spec map[string]any) (string, bool) {
	extension, ok := spec["x-tesserix"].(map[string]any)
	if !ok {
		return "", false
	}
	publication, ok := extension["publication"].(map[string]any)
	if !ok {
		return "", false
	}
	artifact, ok := publication["artifact"].(map[string]any)
	if !ok {
		return "", false
	}
	digest, ok := artifact["digest"].(string)
	return digest, ok && digest != ""
}

func desiredState(object v1alpha1.Object) DesiredState {
	status, _ := object.Status["status"].(string)
	switch status {
	case "deprecated":
		return Deprecated
	case "deleted", "retired":
		return Retired
	default:
		return Published
	}
}
