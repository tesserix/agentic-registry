package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// ---------------------------------------------------------------------------
// Resource identity — the artifact-repository surface: a content-addressed
// digest (immutable fingerprint), an ARN (canonical, version-agnostic name),
// and a pull ref. All are derived; none are accepted on ingest.
// ---------------------------------------------------------------------------

// Digest is the immutable content fingerprint, OCI-style: "sha256:<hex>". It
// prefers the stored ContentHash and recomputes only if absent.
func (o Object) Digest() string {
	h := o.Metadata.ContentHash
	if h == "" {
		h = o.ContentHash()
	}
	return "sha256:" + h
}

// ShortDigest returns the first 12 hex chars of the digest for compact display.
func (o Object) ShortDigest() string {
	h := o.Metadata.ContentHash
	if h == "" {
		h = o.ContentHash()
	}
	if len(h) > 12 {
		h = h[:12]
	}
	return "sha256:" + h
}

func (o Object) identityParts() (tenant, ns, name string) {
	ns = o.Metadata.Namespace
	if ns == "" {
		ns = DefaultNamespace
	}
	tenant = o.Metadata.TenantID
	if tenant == "" {
		tenant = ns
	}
	return tenant, ns, o.Metadata.Name
}

// ARN is the version-agnostic canonical resource name, modeled on AWS ARNs:
//
//	arn:agentic:registry:<tenant>:<plural>/<namespace>/<name>
//
// It identifies the artifact across all of its versions (pin a version with the
// Ref/Digest). Stable for auditing, IAM-style policies, and cross-references.
func (o Object) ARN() string {
	tenant, ns, name := o.identityParts()
	return fmt.Sprintf("arn:agentic:registry:%s:%s/%s/%s", tenant, Plural(o.Kind), ns, name)
}

// Ref is the human pull reference for a specific version:
//
//	<plural>/<namespace>/<name>@<tag>
func (o Object) Ref() string {
	_, ns, name := o.identityParts()
	tag := o.Metadata.Tag
	if tag == "" {
		tag = DefaultTag
	}
	return fmt.Sprintf("%s/%s/%s@%s", Plural(o.Kind), ns, name, tag)
}

// DigestRef is the immutable, content-addressed pull reference:
//
//	<plural>/<namespace>/<name>@sha256:<hex>
func (o Object) DigestRef() string {
	_, ns, name := o.identityParts()
	return fmt.Sprintf("%s/%s/%s@%s", Plural(o.Kind), ns, name, o.Digest())
}

// WithIdentity returns a copy with the derived identity fields (arn, digest,
// ref, digestRef) populated for API responses. Called at the read boundary.
func (o Object) WithIdentity() Object {
	out := o
	out.Metadata.ARN = o.ARN()
	out.Metadata.Digest = o.Digest()
	out.Metadata.Ref = o.Ref()
	out.Metadata.DigestRef = o.DigestRef()
	return out
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
