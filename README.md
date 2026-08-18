<div align="center">

# Agentic Registry

**An open, self-hostable, gateway-neutral registry & marketplace for agentic artifacts.**

Skills · Tools · MCP Servers · Prompts · Workflows · Blueprints · Agents

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![CI](https://github.com/tesserix/agentic-registry/actions/workflows/ci.yml/badge.svg)](https://github.com/tesserix/agentic-registry/actions/workflows/ci.yml)
[![CodeQL](https://github.com/tesserix/agentic-registry/actions/workflows/codeql.yml/badge.svg)](https://github.com/tesserix/agentic-registry/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/tesserix/agentic-registry/badge)](https://scorecard.dev/viewer/?uri=github.com/tesserix/agentic-registry)
[![Go Report Card](https://goreportcard.com/badge/github.com/tesserix/agentic-registry)](https://goreportcard.com/report/github.com/tesserix/agentic-registry)

[Quick start](#quick-start) · [How it works](#how-it-works) · [The catalog](#the-catalog) · [API](#api-surface) · [CLI](#the-agentic-cli) · [Self-host](#self-hosting) · [Security](#security-model)

</div>

---

## What is this?

Agentic Registry is the **control plane for your agentic artifacts** — one place to publish,
version, discover, and govern the building blocks your agents are made of: **skills, tools,
MCP servers, prompts, workflows, blueprints, and agents**.

It is deliberately **a catalog, not a proxy.**

> It stores and serves **metadata + bytes** and answers **discovery queries**. It **never**
> authenticates end-user traffic, mints tokens, proxies requests, or runs the artifacts it
> catalogs. Every gateway integration is an *adapter that reads from the registry* — never a
> coupling baked into it.

That single invariant is what makes it **gateway-neutral**: point Portkey, agentgateway,
kagent, an MCP aggregator, or your own runtime at the same registry and each reads what it
needs.

## Why it exists

Every existing registry leaves a gap:

| Project | The gap |
|---------|---------|
| **Portkey** | Open-sources the *proxy* — keeps the registry (prompts, model catalog, configs) proprietary. |
| **solo.io aregistry** | Couples the registry tightly to its own `agentgateway`. |
| **Official MCP Registry** | Explicitly *not* built for self-hosting. |

Agentic Registry is the missing piece: an **open, self-hostable control plane** you run
yourself and point **any** agentic gateway at.

## Features

- **One envelope, seven kinds** — every artifact is a Kubernetes-style `apiVersion/kind/metadata/spec`
  document. Skills, Tools, MCP Servers, Prompts, Workflows, Blueprints, and Agents all share the
  same shape, so one publish/discover flow covers everything.
- **Content-addressed & idempotent** — bytes are stored by **SHA-256**; re-applying the same
  content is a no-op. OCI-style **tags** (`latest` by default) instead of semver-in-path.
- **Label-selector discovery** — the Kubernetes `LabelSelector` grammar, *verbatim*
  (`matchLabels` + `matchExpressions`, `In | NotIn | Exists | DoesNotExist`).
- **Multi-tenant by design** — `tenant → org → team` ownership × `public | internal | private`
  visibility × cumulative `read < write < admin` RBAC.
- **Two interop dialects** — a devai-compatible **`/v0/*`** API *and* the official MCP-Registry
  **`/v0.1/*`** Generic Registry API (`server.json` is the MCPServer `spec`, natively).
- **Prompt render** — `POST /v0/prompts/{name}/render` returns a ready-to-send
  `{ model, messages, params, tools }` payload built with logic-less Mustache templating. Send
  it to whatever gateway you like.
- **Signed responses** — the registry signs what it serves; `agentic verify` checks the signature.
- **A2A agent cards** — agents expose a `.well-known/agent-card.json` for Agent-to-Agent discovery.
- **Gateway adapters** — export live registry state to a runtime's native config
  (`agentgateway`, `kagent`) without the registry ever being on the request path.
- **Built-in MCP discovery server** — agentic IDEs browse the catalog *through MCP itself*
  (`list_skills`, `get_server`, `search_registry`, …).
- **Marketplace UI + `agentic` CLI** — browse in the web app; `init`/`apply`/`push`/`pull`/`render`
  from the terminal.

---

## How it works

Publishers push artifacts in; gateways and IDEs read them out. The registry sits in the
middle as a catalog — never on the live request path.

```
                        ┌───────────────────────────────────────────┐
  publishers ──CLI/API─▶│            agentic-registry (Go)            │
  (devai, you, CI)      │                                             │──▶ Postgres
                        │  ingest → normalize envelope → SHA-256      │     (catalog: JSONB
  ┌── discovery ───────▶│  store bytes → index labels                │      + GIN-indexed labels)
  │                     │                                             │
  │  gateways           │  /v0/*    devai-compat dialect              │──▶ object store
  │  IDEs (via MCP)     │  /v0.1/*  MCP Generic Registry dialect      │     (bytes, content-addressed)
  │  MCP aggregators    │  built-in MCP discovery server              │
  │                     │  prompt render · A2A cards · adapters       │
  └─────────────────────└───────────────────────────────────────────┘
        identity comes FROM the edge: trusted X-Forwarded-* from a gateway,
        OR a JWKS-verified Bearer JWT. The registry issues no tokens.
```

**The lifecycle of one artifact:**

1. **Author** — write a manifest (or `agentic init Skill my-skill` to scaffold one).
2. **Publish** — `agentic apply -f skill.yaml`. The server normalizes the apiVersion group,
   computes a content hash, stores the bytes, and indexes the labels.
3. **Version** — same name + new tag = a new immutable revision. Same content = idempotent no-op.
   The full revision timeline is append-only.
4. **Discover** — gateways/IDEs query by kind + label selector. **Visibility/RBAC is applied
   *before* the selector** — the selector is discovery convenience, never a security boundary.
5. **Consume** — a gateway pulls config via an **adapter**, an IDE browses via the **MCP discovery
   server**, or a runtime fetches a rendered prompt. The registry just serves; it never runs anything.

---

## The catalog

### Seven kinds, one envelope

```yaml
apiVersion: registry.agentic.dev/v1alpha1
kind: Skill                 # Skill | Tool | MCPServer | Prompt | Workflow | Blueprint | Agent
metadata:
  name: senior-developer
  namespace: default        # maps to a tenant scope
  tag: latest               # OCI-style; same tag re-applies atomically
  labels: { domain: code-review, language: go }
  visibility: public        # public | internal | private
spec: { ... }               # kind-specific body
status: { ... }             # server-managed (content hash, timestamps, signature)
```

| Kind | What it catalogs |
|------|------------------|
| **Skill** | A reusable agent capability (prompt + metadata + source pointer). |
| **Tool** | A callable function/tool definition. |
| **MCPServer** | An MCP server — the `spec` **is** a `server.json` (packages/remotes, transports), so it round-trips with the official MCP Registry. |
| **Prompt** | A versioned, renderable prompt template (Mustache) with movable labels (`production`/`staging`). |
| **Workflow** | A multi-step orchestration of skills/tools. |
| **Blueprint** | A composable bundle/recipe of the above. |
| **Agent** | A devai-interop agent definition; exposes an A2A agent card. |

### Three orthogonal axes

The data model separates **who owns it**, **who can see it**, and **how you find it**:

1. **Scope (ownership)** — `tenant_id` (required) → `org_id` → `team_id`. Users join teams;
   teams live in orgs (the Docker Hub model). Reverse-DNS namespaces, ownership-verified.
2. **Visibility** — `public | internal | private`. `internal` = the whole tenant; `public`
   ⇒ anonymous read, no token required.
3. **Labels** — freeform user labels for discovery, plus **server-managed immutable system
   labels** (`tenant`, `org`, `visibility`) that publishers *cannot* spoof.

### The access decision (the security boundary)

```
can_read(artifact, caller) =
      artifact.visibility == public
   OR (artifact.visibility == internal AND caller.tenant == artifact.tenant)
   OR rbac_grant(read|write|admin, artifact, caller)
```

Roles are cumulative: `read < write < admin`.

---

## Quick start

```bash
# 1. Run it locally — zero dependencies (in-memory store):
make run                              # API on http://localhost:8080
curl localhost:8080/v0/health

# 2. Publish your first artifact:
go run ./cmd/agentic apply -f examples/skill.yaml

# 3. Discover it:
curl 'localhost:8080/v0/skills?labelSelector=domain%20in%20(code-review)'
```

Prefer containers? Bring up the full stack (Postgres + API + UI):

```bash
docker compose -f deploy/docker-compose.yml up
```

Pull the published image directly from GHCR (no auth — it's public):

```bash
docker pull ghcr.io/tesserix/agentic-registry:latest
```

---

## The `agentic` CLI

```text
agentic login --registry URL [--token TOKEN]
agentic init <kind> <name>              scaffold a manifest (Skill|Tool|MCPServer|Prompt|Workflow|Blueprint|Agent)
agentic apply -f <file.yaml>            publish a multi-doc YAML bundle (kubectl-apply style)
agentic push <file.yaml>                publish a single resource
agentic list <plural> [--selector S]    list a collection (table; -o json for raw)
agentic pull <plural> <name> [--tag T]  fetch one artifact (omit --tag for latest)
agentic versions <plural> <name>        list all published versions (tags)
agentic history <plural> <name>         show the append-only revision timeline
agentic search <query>                  cross-kind ranked search
agentic render <name> [-f vars.json]    render a Prompt with variables
agentic verify <plural> <name> [--tag T]  verify the registry's signature
agentic export <target> <agent>         render an agent for a runtime (kagent | agentgateway)
agentic status                          registry endpoint, health, signing key
```

Config lives in `~/.agentic/config.json`. Overridable via `AGENTIC_REGISTRY`,
`AGENTIC_TOKEN`, and `AGENTIC_INSECURE`.

---

## API surface

### `/v0/*` — devai-compatible dialect

```text
GET    /v0/health
GET    /v0/{plural}[?namespace=&labelSelector=]    list a kind (skills|tools|mcpservers|prompts|workflows|blueprints|agents)
POST   /v0/{plural}                                 publish a single resource
GET    /v0/{plural}/{name}                          latest tag
GET    /v0/{plural}/{name}/{tag}                    a specific tag
GET    /v0/{plural}/{name}/tags                      list tags
GET    /v0/{plural}/{name}/revisions                 append-only revision timeline
DELETE /v0/{plural}/{name}/{tag}                     delete one version
POST   /v0/apply                                     multi-doc YAML batch (apply)
DELETE /v0/apply                                     multi-doc YAML batch (delete)
POST   /v0/prompts/{name}/render                     → { model, messages, params, tools }
GET    /v0/search?q=                                 cross-kind ranked search
GET    /v0/signing-key                               public key the registry signs with
GET    /v0/agents/{name}/.well-known/agent-card.json  A2A agent card
GET    /v0/{plural}/{name}/resolved                  fully-resolved artifact (refs expanded)
GET    /v0/export/agentgateway                       export catalog as agentgateway config
GET    /v0/export/kagent                             export agents as kagent resources
```

`servers` and `mcpservers` are aliases for the `MCPServer` kind; the apiVersion group is
normalized on ingest.

### `/v0.1/*` — MCP Generic Registry interop (verbatim)

```text
GET    /v0.1/health
GET    /v0.1/servers?cursor=&limit=&search=&updated_since=&version=
GET    /v0.1/servers/{serverName}/versions
GET    /v0.1/servers/{serverName}/versions/{version}     ('latest' allowed)
POST   /v0.1/publish                                      Bearer JWT; body = server.json
PATCH  /v0.1/servers/{serverName}/versions/{version}/status   active | deprecated | deleted
```

Cursor pagination via `metadata.nextCursor`; incremental sync via `updated_since`.

---

## Self-hosting

Everything you need to copy and run is in [`k8s/`](k8s/):

- [`k8s/secrets.example.yaml`](k8s/secrets.example.yaml) — the **single** Secret to fill in.
- [`k8s/README.md`](k8s/README.md) — copy-and-go steps.
- [`docs/deploy.md`](docs/deploy.md) — local-kind, Helm, and ArgoCD paths.

The server is a single distroless static Go binary. In production it pairs with Postgres
(catalog) and an object store (bytes); secrets come from a secret manager via External
Secrets — **never** the database, never the image.

More guides:

- [`docs/connecting-tools.md`](docs/connecting-tools.md) — wiring a gateway/tool to the registry.
- [`docs/cli.md`](docs/cli.md) — the full CLI reference.
- [`docs/a2a-agent-cards.md`](docs/a2a-agent-cards.md) — Agent-to-Agent discovery.

---

## Security model

The architecture *is* the security posture. The registry:

- **does not** mint tokens, authenticate end-user traffic, or proxy requests;
- **stores no secrets** in its database — provider credentials are kept only as **references**
  to an external secret manager;
- accepts identity **from the edge** — a trusted `X-Forwarded-*` header from a gateway (over
  mTLS / a known gateway only), a Bearer JWT verified against a configurable JWKS/issuer,
  or a tenant-scoped deploy key whose SHA-256 digest is stored by the server;
- applies the **visibility/RBAC filter before any label selector**, so discovery can never
  widen access.

Found a vulnerability? See [SECURITY.md](SECURITY.md) — please report privately, do not open a
public issue. Reports about secret leakage, RBAC bypass, or namespace-ownership spoofing are
treated as high severity.

Machine publishers can use `AUTH_DEPLOY_KEYS` with comma-separated
`tenant=sha256-digest` entries. Repeating a tenant allows old and new keys to
overlap during rotation. Keep the raw key only in the publisher's secret store;
the registry compares its digest in constant time and grants only
`registry:read registry:write` within the configured tenant.

---

## Contributing & governance

Contributions are welcome — start with [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md).

- **Branch + PR** off `main`; every commit must be **signed off** (`git commit -s`, DCO).
- CI must be green: Go build/vet/test, web build, secret scan (gitleaks), and CodeQL.
- This project is maintained by a **single maintainer** ([@Sam123ben](https://github.com/Sam123ben));
  all merges and approvals to `main` go through them. `main` is protected and requires a
  passing review + checks.

The architectural invariant is non-negotiable: **any change that routes traffic, mints
tokens, or runs artifacts will be rejected.** The registry stays a catalog.

## License

[Apache-2.0](LICENSE). See also [NOTICE](NOTICE).
