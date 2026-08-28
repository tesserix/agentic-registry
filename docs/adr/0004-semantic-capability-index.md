# ADR-0004 — Registry-owned semantic capability index

Status: accepted · 2026-08-28

## Context

Agents and gateways need to discover Tools, Skills, Agents, MCP Servers,
Prompts, Workflows, Blueprints, Datasets, and Evaluation Suites from intent and
interface metadata. Sending the full catalog to a model wastes context and
hard-coded routing drifts as the catalog changes.

The registry is the authorization and lifecycle boundary for these artifacts.
Duplicating its catalog into DevAI memory or a separate Qdrant collection would
create an eventually consistent metadata store, a second tenancy boundary, and
another backup/recovery path. The production Postgres store already supports a
pgvector column and HNSW cosine index on the authoritative artifact row.

Search must not index prompt bodies, system instructions, MCP endpoints or
launch configuration, environment variables, headers, credential references,
or publisher-supplied secrets. Search is discovery only: a hit must never grant
tool execution or agent authority.

## Design envelope and SLO

This decision targets up to 100,000 live artifacts over 36 months, a 100:1
search-to-write ratio, 50 search requests/second at peak, and safe documents
bounded to 12 KB. The search objective is p99 below 300 ms at the registry API
and 99.9% monthly availability; callers must include their own network budget
for the additional gateway hop. These are design limits, not measurements, and
must be revisited with query plans and production telemetry before the catalog
or peak rate exceeds them.

At that envelope, 256-dimensional float vectors consume about 100 MB before
row and HNSW overhead. The decision adds no managed service, backup target, or
network boundary; its incremental cost is Postgres storage, index maintenance,
and startup CPU for the embedding refresh. Search latency and error rate use
the registry's existing RED telemetry and alert ownership.

## Decision

Agent Registry owns semantic discovery and pgvector is the default vector
backend.

- `internal/discovery` builds one bounded, secret-safe document for every
  artifact kind. It includes names, titles, descriptions, tags, approved
  discovery annotations, capability relationships, and Tool input-schema
  property names/types/descriptions. Sensitive metadata values are redacted;
  unapproved annotations and executable/runtime fields are excluded.
- `internal/embed` hashes that safe document into the existing 256-dimensional
  normalized vector. The default remains deterministic and dependency-free,
  providing lexical, fuzzy, and metadata-aware ranking. The pgvector schema can
  accept a learned embedder later without moving catalog ownership.
- Existing vectors are rebuilt on registry startup so a safe-document schema
  change cannot leave older rows ranked using stale fields. Apply continues to
  refresh the vector transactionally with the authoritative row.
- `GET /v0/search?...&view=stub` returns ranked, authorized discovery stubs,
  not artifact bodies. `kinds` filters canonical kind names or gateway plural
  aliases, and `limit` bounds the result. Each stub carries identity metadata,
  safe annotations/attributes, and an exact versioned `fetchPath` for
  progressive disclosure. The existing artifact response remains the default
  view for compatibility.
- The built-in MCP `search_registry` tool uses the same store ranking, kind
  filter, safe stub projection, and exact fetch path. DevAI and MCP Hub delegate
  to this registry endpoint instead of maintaining their own vector copy.
- Dataset and EvalSuite participate in the same search contract as the existing
  catalog kinds.
- RBAC/visibility filtering remains before selectors, projection, and response
  pagination. DevAI additionally checks its opaque owner label because its
  service identity may have broader registry read access than an end user.
- Search results are candidates only. Governed execution still fetches and
  authorizes the exact object and resolves agent composition through
  `/v0/agents/{name}/{tag}/resolved`.

## Failure behavior

- pgvector unavailable or disabled: the shared safe document is used for the
  in-memory substring fallback, so correctness and secret exclusion stay the
  same while ranking quality degrades.
- Registry unavailable: gateways return a discovery error; they do not promote
  a stale hit into execution authority.
- Malformed or unknown kind filters are rejected. Empty queries and unbounded
  limits are rejected by gateway/MCP consumers.
- A misplaced secret-shaped value is redacted before it reaches either the
  vector or discovery stub. Exact authorized fetches may still return the
  authoritative artifact body as designed.

## Consequences

There is one catalog, one authorization policy, and one vector lifecycle.
Qdrant is intentionally not added: at the forecast registry scale, pgvector is
well within capacity and avoids a cross-datastore synchronization failure mode.
If operational evidence later requires Qdrant, it must remain a rebuildable
index behind the same registry-owned safe document and RBAC contract, never the
metadata source of truth.

The rollout is additive and has no schema migration. Rollback is a revert to
the previous registry and DevAI images through GitOps; authoritative artifacts
are unchanged. A rolled-back registry may temporarily rank vectors produced by
the newer projection less accurately, but exact fetch and authorization remain
correct, and re-applying artifacts rebuilds the prior vectors.
