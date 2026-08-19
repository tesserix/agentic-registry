# ADR-0003: AgentGateway desired-state ownership

## Context

Solo AgentGateway runs in XDS mode in production. Its embedded UI intentionally
exposes runtime traffic state as read-only; it cannot safely write configuration
because Kubernetes resources are the XDS source of truth. Agentic Registry
already owns MCP-derived `AgentgatewayBackend` and `HTTPRoute` objects, while
Helm owns model backends, LLM routes, and policies. A second writer would cause
Argo CD self-heal and the Registry reconciler to overwrite each other.

Expected control-plane load is below 5 requests/second, resources stay below
1 MiB, reads exceed writes by at least 20:1, and fewer than 1,000 resources are
expected at 12 and 36 months. The target is 99.9% monthly availability and p99
below 500 ms for Registry reads and writes. Reconciliation may be eventually
consistent within five minutes; the live gateway keeps serving its last valid
XDS snapshot when Registry or reconciliation is unavailable.

The assets are production model and tool routing, authentication policy, and
rate limits. Threat actors include unauthenticated internet clients,
authenticated but unapproved users, compromised product agents, and a forged
proxy identity. The trust boundary is the Zitadel-authenticated oauth2-proxy,
the Registry authorization layer, and the namespace-scoped reconciler. Every
boundary validates identity, object ownership, resource shape, and namespace.

## Options considered

1. Enable write controls in Solo's XDS UI. Rejected because the XDS write APIs
   do not exist and imperative edits would drift from the control plane.
2. Keep Helm ownership and have the admin UI edit Helm values. Rejected because
   interactive writes would require a Git credential and a multi-step GitOps
   workflow for every small route change.
3. Store an allowlisted AgentGateway desired state in Agentic Registry and have
   one namespace-scoped reconciler apply it. Chosen because Registry already
   owns MCP routes, provides version history, and keeps the data plane off the
   control-plane request path.

## Decision

Add a `GatewayResource` Registry artifact. Each artifact wraps exactly one of:

- `agentgateway.dev/v1alpha1`, `AgentgatewayBackend`
- `agentgateway.dev/v1alpha1`, `AgentgatewayPolicy`
- `gateway.networking.k8s.io/v1`, `HTTPRoute`

The server rejects every other GVK, forces namespace `agentgateway-system`,
rejects server-managed metadata and secret-shaped fields, bounds each request
to 1 MiB, and assigns stable artifact names derived from kind and Kubernetes
name. The public API exposes domain-specific list/upsert/delete endpoints and a
YAML export. Writes require a server-derived global Registry administrator;
only the two configured, verified Zitadel identities may receive that role.
Browser claims never directly grant it. Machine reconciliation uses the
existing tenant-scoped DevAI deploy key and receives no global administrator
permission.

The reconciler applies only the three allowlisted GVKs in
`agentgateway-system`. It labels its objects, prunes only objects carrying that
label, and never manages `Gateway`, `AgentgatewayParameters`, Secrets, RBAC, or
workloads. MCP resources remain generated from `MCPServer` artifacts and share
the same reconciler/export without changing their authoring API.

Admin UI writes synchronously to Registry and reports the accepted revision.
Kubernetes convergence is asynchronous. Duplicate PUTs are idempotent. A
repeated request creates no divergent live object; reconciliation uses stable
names and server-side apply. A Registry timeout fails the write closed. A
reconciler crash leaves the last applied resources and is retried by the next
CronJob; there is no cross-system transaction and no destructive compensation.

## Migration

1. Import the current live objects unchanged and apply them with
   `argocd.argoproj.io/sync-options: Prune=false` while Helm still renders the
   same specs.
2. Compare canonical live specs and acceptance conditions with the captured
   hashes. No ownership removal proceeds on a mismatch.
3. Remove the Helm-rendered backends, routes, and policies. A GitOps-managed
   handoff Job removes only their old Argo tracking annotations after the
   prune-disabled sync, leaving Registry as the sole writer.
4. Confirm Argo is Synced/Healthy, reconciliation is current, and the gateway
   serves model and MCP smoke requests before deleting the handoff Job.

Rollback is one Git revert: restore the Helm templates at the captured commit,
disable Registry pruning, and sync. Because the resource names and specs are
unchanged, Helm resumes ownership without changing data-plane endpoints.

## Consequences

The control plane gains one source of truth and a write-enabled Tesserix UI,
while Solo's traffic pages remain the runtime view. Configuration writes may be
up to five minutes behind Registry state. Registry history is the audit trail;
Kubernetes events and CronJob logs show reconciliation. The additional steady
cost is one short CronJob every five minutes and no new always-on service.

This does not make Gateways, credentials, namespaces, RBAC, or workloads
editable from the UI. Those remain GitOps-owned because expanding the
reconciler's blast radius is not required for model, route, policy, or MCP
administration.
