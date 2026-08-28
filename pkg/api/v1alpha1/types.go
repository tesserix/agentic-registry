// Package v1alpha1 defines the on-the-wire resource model for Agentic Registry.
//
// Every artifact — Skill, Tool, MCPServer, Prompt, Workflow, Blueprint, Agent —
// shares one Kubernetes-style envelope (apiVersion + kind + metadata + spec +
// status). This single contract flows unchanged from a YAML manifest, through
// the HTTP API, to a database row, eliminating per-kind DTO layers.
package v1alpha1

import "time"

// Group is our vendor-neutral API group. We accept the solo.io and ar.dev
// groups on ingest (see normalize.go) but always store and emit this one.
const (
	Group        = "registry.agentic.dev"
	Version      = "v1alpha1"
	GroupVersion = Group + "/" + Version
)

// Kind enumerates the artifact kinds the registry catalogs.
type Kind string

const (
	KindSkill     Kind = "Skill"
	KindTool      Kind = "Tool"
	KindMCPServer Kind = "MCPServer"
	KindPrompt    Kind = "Prompt"
	KindWorkflow  Kind = "Workflow"
	KindBlueprint Kind = "Blueprint"
	KindAgent     Kind = "Agent"
	KindDataset   Kind = "Dataset"
	KindEvalSuite Kind = "EvalSuite"
	// KindGatewayResource is one validated AgentGateway Kubernetes object.
	// It is stored as desired state and rendered only by the allowlisted
	// AgentGateway exporter; it is never a general Kubernetes manifest kind.
	KindGatewayResource Kind = "GatewayResource"
	// KindProject is a devai legacy kind. We accept it on ingest and persist
	// it as a namespace/label collection rather than a deployable artifact.
	KindProject Kind = "Project"
)

// Visibility is one of the three orthogonal access axes (with scope and labels).
type Visibility string

const (
	VisibilityPublic   Visibility = "public"
	VisibilityInternal Visibility = "internal" // visible to the whole owning tenant
	VisibilityPrivate  Visibility = "private"
)

// AllKinds is the canonical ordered list of catalogable kinds (excludes Project).
var AllKinds = []Kind{
	KindSkill, KindTool, KindMCPServer, KindPrompt,
	KindWorkflow, KindBlueprint, KindAgent, KindDataset, KindEvalSuite,
	KindGatewayResource,
}

// pluralByKind maps a Kind to its REST collection name. MCPServer has two
// aliases: "mcpservers" (solo aregistry's real plural) and "servers" (devai's
// client name). Both resolve to KindMCPServer; we emit "mcpservers".
var pluralByKind = map[Kind]string{
	KindSkill:           "skills",
	KindTool:            "tools",
	KindMCPServer:       "mcpservers",
	KindPrompt:          "prompts",
	KindWorkflow:        "workflows",
	KindBlueprint:       "blueprints",
	KindAgent:           "agents",
	KindDataset:         "datasets",
	KindEvalSuite:       "evalsuites",
	KindGatewayResource: "gatewayresources",
	KindProject:         "projects",
}

var kindByPlural = func() map[string]Kind {
	m := map[string]Kind{}
	for k, p := range pluralByKind {
		m[p] = k
	}
	m["servers"] = KindMCPServer // devai alias
	m["mcp-servers"] = KindMCPServer
	m["eval-suites"] = KindEvalSuite
	return m
}()

// Plural returns the REST collection name for a kind ("" if unknown).
func Plural(k Kind) string { return pluralByKind[k] }

// KindForPlural resolves a collection name to a Kind, accepting the "servers"
// alias. ok is false for unknown plurals.
func KindForPlural(plural string) (Kind, bool) {
	k, ok := kindByPlural[plural]
	return k, ok
}

// ObjectMeta carries identity, scope, discovery labels, and timestamps.
type ObjectMeta struct {
	Name      string            `json:"name" yaml:"name"`
	Namespace string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Tag       string            `json:"tag,omitempty" yaml:"tag,omitempty"`
	UID       string            `json:"uid,omitempty" yaml:"uid,omitempty"`
	Labels    map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	// Annotations are narrative metadata. Approved discovery namespaces are
	// indexed through a secret-safe projection; arbitrary annotations are not.
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	// Visibility lives on metadata so it round-trips with the artifact.
	Visibility Visibility `json:"visibility,omitempty" yaml:"visibility,omitempty"`

	// Scope (ownership). TenantID is required once multi-tenancy is enabled; it
	// defaults to the namespace when omitted. OrgID/TeamID are optional.
	TenantID string `json:"tenantId,omitempty" yaml:"tenantId,omitempty"`
	OrgID    string `json:"orgId,omitempty" yaml:"orgId,omitempty"`
	TeamID   string `json:"teamId,omitempty" yaml:"teamId,omitempty"`

	// Server-managed; ignored on ingest.
	ContentHash       string     `json:"contentHash,omitempty" yaml:"-"`
	CreatedAt         *time.Time `json:"createdAt,omitempty" yaml:"-"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty" yaml:"-"`
	DeletionTimestamp *time.Time `json:"deletionTimestamp,omitempty" yaml:"-"`

	// Derived identity — computed at the read boundary (WithIdentity), never
	// stored or accepted on ingest. The artifact-repository surface:
	//   ARN       canonical, version-agnostic resource name
	//   Digest    immutable content fingerprint ("sha256:<hex>")
	//   Ref       pull reference for this version (…/name@tag)
	//   DigestRef immutable, content-addressed pull reference (…/name@sha256:…)
	ARN       string `json:"arn,omitempty" yaml:"-"`
	Digest    string `json:"digest,omitempty" yaml:"-"`
	Ref       string `json:"ref,omitempty" yaml:"-"`
	DigestRef string `json:"digestRef,omitempty" yaml:"-"`

	// Signature is the registry's base64 Ed25519 signature over Digest, and
	// SignedBy is the signing key's id. Both are attached at the read boundary
	// (server-side attestation) and never accepted on ingest.
	Signature string `json:"signature,omitempty" yaml:"-"`
	SignedBy  string `json:"signedBy,omitempty" yaml:"-"`
}

// Object is the universal envelope. spec/status are kept as free-form maps so a
// single type serves every kind and stores cleanly as JSONB.
type Object struct {
	APIVersion string                 `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind                   `json:"kind" yaml:"kind"`
	Metadata   ObjectMeta             `json:"metadata" yaml:"metadata"`
	Spec       map[string]interface{} `json:"spec,omitempty" yaml:"spec,omitempty"`
	Status     map[string]interface{} `json:"status,omitempty" yaml:"status,omitempty"`
}

// DefaultNamespace is used when metadata.namespace is empty.
const DefaultNamespace = "default"

// DefaultTag is the implicit tag for content-registry kinds.
const DefaultTag = "latest"

// Normalized returns a copy with apiVersion canonicalized, namespace/tag/
// visibility/tenant defaulted, and system labels stamped. It does not mutate
// the receiver.
func (o Object) Normalized() Object {
	out := o
	out.APIVersion = GroupVersion // we always store our canonical group

	// Derived identity fields are server-computed on read — never trust them
	// from a publisher (prevents spoofed ARNs/digests).
	out.Metadata.ARN = ""
	out.Metadata.Digest = ""
	out.Metadata.Ref = ""
	out.Metadata.DigestRef = ""
	out.Metadata.Signature = ""
	out.Metadata.SignedBy = ""

	if out.Metadata.Namespace == "" {
		out.Metadata.Namespace = DefaultNamespace
	}
	if out.Metadata.Tag == "" {
		out.Metadata.Tag = DefaultTag
	}
	if out.Metadata.Visibility == "" {
		out.Metadata.Visibility = VisibilityPrivate // safe default
	}
	if out.Metadata.TenantID == "" {
		out.Metadata.TenantID = out.Metadata.Namespace
	}

	// Stamp immutable system labels (publishers cannot spoof these).
	labels := map[string]string{}
	for k, v := range out.Metadata.Labels {
		labels[k] = v
	}
	labels["registry.agentic.dev/tenant"] = out.Metadata.TenantID
	labels["registry.agentic.dev/visibility"] = string(out.Metadata.Visibility)
	if out.Metadata.OrgID != "" {
		labels["registry.agentic.dev/org"] = out.Metadata.OrgID
	}
	out.Metadata.Labels = labels
	return out
}
