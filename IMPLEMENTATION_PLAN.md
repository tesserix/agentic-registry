# Agentic Registry — Implementation Plan

> Open-source, self-hostable, **gateway-agnostic** registry & marketplace for
> agentic artifacts: **Skills, Tools, MCP Servers, Prompts, Workflows, Blueprints**
> (plus `Agent` for devai interop).

## 0. Thesis

A registry that is a **catalog / control plane** — *not* a proxy and *not* an auth
layer — is a deliberately-vacated gap in the ecosystem:

- **Portkey** keeps its registry (prompts, model catalog, configs) proprietary and
  open-sources only the *proxy*.
- **solo.io aregistry** couples its registry tightly to `agentgateway`.
- The **official MCP Registry** is explicitly *not* built for self-hosting.

Agentic Registry fills that gap: an **open, self-hostable, gateway-neutral** control
plane. The hard architectural invariant that every decision protects:

> The registry **stores and serves metadata + bytes and answers discovery queries**.
> It **never** authenticates traffic, mints tokens, proxies requests, or runs the
> artifacts it catalogs. Every gateway integration is an *adapter that reads from
> the registry*, never a coupling baked into it.

## 1. Decisions (grounded in prior-art research)

| Area | Decision | Rationale |
|------|----------|-----------|
| Storage | **Postgres** (envelope as JSONB, GIN-indexed labels) + **GCS** for bytes, content-addressed by SHA-256 | Artifacts are KB-sized & metadata-heavy; labels/RBAC/visibility/search must live in Postgres anyway. OCI/ORAS deferred to v2. |
| Backend | **Go** + chi router; OpenAPI doc committed | Registry is infra (metadata CRUD, selector eval, digest math, JWKS) not AI. OCI/cosign/k8s-selector libs are all Go. Single distroless static binary. |
| API group | `registry.agentic.dev/v1alpha1` (neutral) | Normalize `registry.solo.io/v1alpha1` & `ar.dev/v1alpha1` **on ingest** so devai's existing seeds publish unchanged. |
| Frontend | React + Vite + Tailwind 4, **devai design tokens** | Visual sibling of devai Mission Control (Paper·Ink·Slate). |
| Auth | **Out of the data path** — trusted `X-Forwarded-*` from a gateway OR JWKS-verified Bearer JWT | Registry issues no tokens, validates no traffic. |
| Secrets | **GCP Secret Manager only** — never DB, never code | DB creds via External Secrets; GCS via Workload Identity; provider keys are references, not values. |

## 2. Resource model — one envelope, seven kinds

Every artifact is a Kubernetes-style envelope:

```yaml
apiVersion: registry.agentic.dev/v1alpha1
kind: Skill            # Skill | Tool | MCPServer | Prompt | Workflow | Blueprint | Agent
metadata:
  name: senior-developer
  namespace: default   # maps to a tenant scope
  tag: latest          # OCI-style; same-tag apply atomically replaces
  labels: { domain: code-review, language: go }
  visibility: public   # public | internal | private
spec: { ... }          # kind-specific
status: { ... }        # server-managed
```

- **Tag-based versioning** (not semver-in-path), `latest` default, `content_hash`
  (SHA-256) idempotent re-apply.
- `MCPServer.spec` **is** `server.json` (reverse-DNS one-slash name, packages/remotes,
  transport sub-objects) → native MCP-Registry interop.
- `Project` (devai legacy kind) accepted on ingest as a namespace/label **collection**,
  not a deployable artifact.

## 3. Data model — three orthogonal axes

1. **Scope (ownership):** `tenant_id` (required) → `org_id` → `team_id`. Users join
   teams; teams live in orgs (Docker Hub model). Reverse-DNS namespaces, ownership-verified.
2. **Visibility:** `public | internal | private` (internal = whole tenant). Public ⇒
   anonymous read.
3. **Labels:** freeform user labels + server-managed **immutable system labels**
   (`tenant`, `org`, `visibility`) publishers cannot spoof.

**Access decision (the security boundary):**
```
can_read(id, a) = a.visibility==public
               OR (a.visibility==internal AND id.tenant==a.tenant)
               OR rbac_grant(read|write|admin, id, a)
```
Roles are cumulative: `read < write < admin`.

**Discovery** uses the Kubernetes `LabelSelector` verbatim — `matchLabels` +
`matchExpressions`, operators `In | NotIn | Exists | DoesNotExist` only, AND-only.
Both JSON-body and URL string forms (`?labelSelector=domain in (code-review),language=go`).

> **Invariant:** the visibility/RBAC filter is applied **before** the label selector.
> The selector is discovery convenience, never a security boundary.

## 4. API surface

### devai-compat `/v0/*` (must not break)
```
GET  /v0/health
GET  /v0/{skills|prompts|servers|mcpservers|agents|tools|workflows|blueprints}[?namespace=&labelSelector=]
GET  /v0/{plural}/{name}            # latest tag
GET  /v0/{plural}/{name}/{tag}
GET  /v0/{plural}/{name}/tags
POST /v0/{plural}                   # publish single resource
POST /v0/apply                      # multi-doc YAML batch (kubectl-apply-like)
DELETE /v0/apply
```
`servers` and `mcpservers` are aliases for the `MCPServer` kind. apiVersion group
normalized on ingest.

### MCP interop `/v0.1/*` (Generic Registry API, verbatim)
```
GET   /v0.1/servers?cursor=&limit=&search=&updated_since=&version=&include_deleted=
GET   /v0.1/servers/{serverName}/versions
GET   /v0.1/servers/{serverName}/versions/{version}     # 'latest' allowed
POST  /v0.1/publish                                      # Bearer JWT; body = server.json
PATCH /v0.1/servers/{serverName}/versions/{version}/status   # active|deprecated|deleted
GET   /v0.1/health   ;   GET /metrics
```
Cursor pagination via `metadata.nextCursor`; incremental sync via `updated_since`.

### Prompt render (Portkey-style, keeps registry off the request path)
```
POST /v0/prompts/{name}/render   → { model, messages, params, tools }
```
Logic-less **Mustache** templating (no code-exec in tenant templates). Immutable
numbered versions + movable labels (`production`/`staging`/`@latest`).

### Built-in MCP discovery server
Exposes `list_skills/get_skill`, `list_servers/get_server`, `list_agents/get_agent`,
`search_registry(query)` as MCP tools so agentic IDEs discover registry contents
through MCP itself. Catalog, not proxy.

## 5. Gateway decoupling

- **Auth:** accept a pre-validated identity from any OIDC gateway via trusted
  `X-Forwarded-User/Email/Groups` (trusted only over mTLS / known gateway), OR verify
  a Bearer JWT against a configurable JWKS/issuer. Maps `groups` → internal roles.
  Never issues tokens.
- **Connectors (out-of-tree adapters):** translate registry state → a gateway's native
  config. `adapters/agentgateway` (binds/listeners/routes/MCPBackends),
  `adapters/portkey` (config JSON / model-catalog refs). The registry only *emits*
  config; the proxy consumes it.

## 6. devai integration

A new `registry` **adapter family** in `devai/src/devai/adapters/registry/` mirroring
`adapters/memory/`: `factory.py` + `AdapterRegistry.register()`, impls `tesserix`
(ours), `solo_aregistry`, `portkey`, `mcp_registry`. devai swaps backend by env var;
the registry stays ignorant of devai.

## 7. Secrets integration (no secrets in DB or code)

| Secret | Source | Delivery |
|--------|--------|----------|
| Postgres password | GCP Secret Manager `prod-agentic-registry-db` | External Secret → K8s Secret → env `DATABASE_URL` |
| GCS access | Workload Identity (GCP SA `app-secrets-agentic-registry-prod@…`) | no key files |
| JWKS / OIDC issuer | non-secret config | env `AUTH_JWKS_URL`, `AUTH_ISSUER` |
| Provider API keys (catalog) | GCP Secret Manager | stored as **references** in registry; values never persisted |

## 8. Infra & deploy

- Distroless Go image (`base-distroless-static`), `docker-compose.yml` for local.
- Helm chart `tesserix-k8s/charts/apps/agentic-registry`, ArgoCD app, **new GCP
  namespace `agentic-registry`**, in-cluster Postgres (CNPG pattern), External Secrets.
- Schema SQL lives **only** in `tesserix-k8s/charts/apps/db-schema-bootstrap/schemas/agentic-registry/`.
- Memory-only resources (no CPU limits/requests), KEDA memory-trigger ScaledObject.
- Deploy via the `public → CI → private` flow.

## 9. Milestones

1. Repo scaffold + OSS guardrails + this plan ✅
2. Backend core: envelope, store (memory+postgres), `/v0/*`, label selector, auth
3. MCP interop `/v0.1/*` + `server.json` + built-in MCP discovery + prompt render
4. React marketplace UI (devai tokens)
5. CLI `agentic` (init/build/push/apply/login)
6. devai `registry` adapter family
7. Dockerfile + Helm + ArgoCD + GCP deploy + seed devai's registry-seeds

## 10. Deferred to v2

OCI/ORAS pull protocol · cosign admission gating · SBOM/SLSA provenance · vector
search · ratings/downloads/trending · in-registry Runtime/Deployment lifecycle ·
Terraform provider.
