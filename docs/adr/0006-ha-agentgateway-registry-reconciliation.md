# ADR-0006: HA AgentGateway Registry reconciliation

Status: accepted · 2026-08-30

## Context and design envelope

Agentic Registry is the desired-state owner for production
`AgentgatewayBackend`, `AgentgatewayPolicy`, and `HTTPRoute` resources, but a
five-minute CronJob currently copies that state into Kubernetes. That creates a
five-minute change delay, has no immediate drift repair, and offers only Job
history as status. The AgentGateway data plane already retains its last valid
xDS state when either control plane is unavailable.

Production currently has 52 Registry-owned resources (24 backends, 13 routes,
and 15 policies). The exported snapshot is below 1 MiB. The 36-month envelope
is fewer than 1,000 resources and a 5 MiB maximum snapshot. Registry changes
are expected below one per second at peak and reads exceed writes by more than
20:1. One leader polling every 30 seconds adds 0.033 Registry requests/second.
A changed or five-minute safety reconciliation is expected to stay below 10
Kubernetes API requests/second at the design limit.

The control-plane objective is 99.9% monthly availability and p99 below 30
seconds from a committed Registry change to an accepted gateway resource.
Kubernetes drift should begin repair within five seconds of an informer event,
with a full five-minute safety pass. Data-plane availability has an RTO of zero
for a control-plane outage because the last accepted Kubernetes/xDS state is
retained. The reconciliation RPO is the last digest-verified Registry snapshot;
an unapplied Registry write remains durable and is retried after recovery.

The protected assets are tool/model routing and the authorization, rate-limit,
and observability policy attached to it. Threat actors include an
unauthenticated network caller, a compromised workload, a tenant deploy key,
and a compromised dependency. Trust is crossed at the authenticated Registry
export and again at the Kubernetes API. The controller bounds and validates the
entire snapshot before mutation and holds namespace-scoped RBAC for exactly the
three managed kinds plus its leader-election Lease.

## Decision

Ship `agentgateway-sync` as a second binary in the Agentic Registry image and
run two replicas in `agentgateway-system`:

- Kubernetes Lease leader election permits one writer and a hot standby across
  zones. Lease loss terminates the unhealthy replica so Kubernetes can replace
  it; expected failover is within the 15-second lease duration.
- The leader uses an authenticated conditional GET every 30 seconds. Registry
  returns a strong SHA-256 ETag, count, and digest. A `304` is accepted only
  when all metadata matches the cached snapshot.
- A snapshot is rejected atomically if it exceeds 5 MiB, falls below the
  configured permanent resource floor, has a count or digest mismatch, contains
  a duplicate or status field, targets another namespace, lacks the Registry
  ownership label, or contains any other GVK.
- The last verified snapshot is retained in controller memory and in the live
  Kubernetes objects. Registry failure never translates into an empty desired
  state. Informer events can repair drift from this cache while Registry is
  unavailable; Kubernetes and AgentGateway xDS remain the durable serving
  cache, so Valkey is not a control-plane dependency.
- Changed snapshots use server-side apply with one field manager. All desired
  backends must report `Accepted=True`; routes must report both
  `Accepted=True` and `ResolvedRefs=True`. Only then may the controller prune a
  stale object carrying `app.kubernetes.io/managed-by=agentic-registry`.
  Deletes carry the UID and resource-version observed during the guarded list.
- Duplicate polls and events are coalesced. Apply is idempotent. A crash during
  apply can leave extra stale objects but cannot prune before acceptance; the
  next leader safely replays the complete snapshot.
- Each replica exposes liveness, readiness, JSON status, and Prometheus-format
  metrics. Alerts cover Registry staleness, reconciliation staleness/errors,
  drift, and absence of exactly one elected leader.

Consistency is deliberately eventual for a new Registry write (30 seconds
p99 target) and read-after-repair for a Kubernetes drift event. Existing data
plane requests remain strongly tied to the last accepted xDS configuration and
never depend synchronously on Registry, this controller, or a cache service.

## Alternatives considered

1. Keep the five-minute CronJob. Rejected as the active path because it cannot
   meet the latency or drift-repair objective. It remains suspended, not
   deleted, as rollback.
2. Publish Registry changes through a queue or Valkey. Rejected because the
   snapshot is small, polling load is negligible, and another stateful system
   creates replay, backup, and split-authority failure modes without improving
   correctness.
3. Introduce a CRD and a full custom operator framework. Rejected initially
   because the Registry export is already the desired-state API and only three
   namespaced GVKs are managed. A bounded controller using client-go provides
   leader election, informers, and reconciliation without a second public API.
4. Let every Registry replica write Kubernetes. Rejected because concurrent
   writers increase API load and complicate failure ownership. One Lease leader
   plus a ready standby meets the availability target.

## Rollout and rollback

Deploy both replicas in `shadow` mode while the CronJob remains active. Shadow
mode authenticates, validates, watches, and reports desired/actual drift but is
incapable of apply or delete. Promote through a second GitOps change that sets
mode `active` and suspends the CronJob. Verify snapshot digest/count, all
backend/route acceptance conditions, one leader, two ready replicas, MCP and
ADK traffic, and alert ingestion before declaring cutover complete.

Rollback is one Git revert: return the controller to `shadow` or `disabled`
and unsuspend the existing CronJob. Stable resource names and the same
Registry ownership label let its tested server-side-apply/prune path resume
without data-plane endpoint changes. The CronJob is not deleted until a later
decision with production history proves the controller rollback unnecessary.

## Consequences

Registry changes normally program AgentGateway in tens of seconds and live
drift is repaired without waiting for the next CronJob. The serving path gains
no new synchronous dependency. The steady incremental cost is two small Go
pods (64 MiB requested and 128 MiB limited each), a Lease, informer watches,
and one conditional Registry request every 30 seconds. At current size, a full
safety pass every five minutes is negligible relative to gateway-controller
traffic.

Registry API and PostgreSQL availability remain separate gaps: each currently
has one replica, so this decision makes the synchronization tier HA but does
not by itself make Registry storage HA. That work requires its own tested
database failover and backup decision.
