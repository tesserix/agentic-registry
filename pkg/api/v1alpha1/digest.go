package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ContentHash returns the SHA-256 (hex) of the artifact's content-bearing
// fields: kind, name, namespace, tag, labels, spec. It deliberately excludes
// server-managed metadata (uid, timestamps) so a re-apply of identical content
// produces the same hash — enabling idempotent, no-op upserts.
func (o Object) ContentHash() string {
	type canonical struct {
		Kind      Kind                   `json:"kind"`
		Name      string                 `json:"name"`
		Namespace string                 `json:"namespace"`
		Tag       string                 `json:"tag"`
		Labels    map[string]string      `json:"labels"`
		Spec      map[string]interface{} `json:"spec"`
	}
	c := canonical{
		Kind:      o.Kind,
		Name:      o.Metadata.Name,
		Namespace: o.Metadata.Namespace,
		Tag:       o.Metadata.Tag,
		Labels:    o.Metadata.Labels,
		Spec:      o.Spec,
	}
	// json.Marshal sorts map keys, giving a stable encoding.
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SortedLabelKeys returns label keys in deterministic order (test/debug helper).
func SortedLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
