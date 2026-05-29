<div align="center">

# Agentic Registry

**An open, self-hostable, gateway-neutral registry & marketplace for agentic artifacts.**

Skills · Tools · MCP Servers · Prompts · Workflows · Blueprints

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![CI](https://github.com/tesserix/agentic-registry/actions/workflows/ci.yml/badge.svg)](https://github.com/tesserix/agentic-registry/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/tesserix/agentic-registry/badge)](https://scorecard.dev/viewer/?uri=github.com/tesserix/agentic-registry)

</div>

## Why

Today's registries each leave a gap:

- **Portkey** open-sources the *proxy* but keeps the registry proprietary.
- **solo.io aregistry** couples the registry to its own gateway.
- The **official MCP Registry** isn't built for self-hosting.

Agentic Registry is the missing piece: an **open, self-hostable control plane** you can
run yourself and point **any** agentic gateway at.

> **It is a catalog, not a proxy.** It stores and serves metadata + bytes and answers
> discovery queries. It never authenticates traffic, mints tokens, proxies requests, or
> runs the artifacts it catalogs. Gateways integrate via *adapters that read from it*.

## What you get

- **7 artifact kinds** under one Kubernetes-style envelope, OCI-style tags, content-hash
  idempotency.
- **Label-selector discovery** — the Kubernetes `LabelSelector` grammar, verbatim.
- **Multi-tenant** — tenant → org → team scope × `public/internal/private` visibility ×
  cumulative RBAC.
- **Interop**: a devai-compatible `/v0/*` API *and* the official MCP-Registry
  `/v0.1/*` Generic Registry API (`server.json` native).
- **Prompt render** — fetch a ready-to-send `{model, messages, params, tools}` payload;
  send it to whatever gateway you like.
- **Built-in MCP discovery server** so agentic IDEs browse the catalog through MCP.
- **Marketplace UI** + `agentic` CLI (`init`/`build`/`push`/`apply`/`login`).

## Quick start

```bash
# Local, zero dependencies (in-memory store):
make run                       # API on http://localhost:8080
curl localhost:8080/v0/health

# Full stack (Postgres + API + UI):
docker compose -f deploy/docker-compose.yml up
```

### Deploy to your own cluster

Everything you need to copy and run is in [`k8s/`](k8s/) —
[`k8s/secrets.example.yaml`](k8s/secrets.example.yaml) is the single Secret to
fill in. See [`k8s/README.md`](k8s/README.md) for the copy-and-go steps and
[`docs/deploy.md`](docs/deploy.md) for local-kind, Helm, and ArgoCD paths.

Publish an artifact:

```bash
agentic apply -f examples/skill.yaml
agentic login --jwks-url https://idp.example.com/.well-known/jwks.json
```

## Architecture

```
publishers ──CLI/API──▶ ┌──────────────────────────┐
devai ──registry adapter▶│   agentic-registry (Go)  │──▶ Postgres (catalog, JSONB+GIN)
gateways ──discovery────▶│  /v0/* (devai-compat)    │──▶ object store (bytes, SHA-256)
MCP aggregators ─/v0.1/─▶│  /v0.1/* (MCP interop)   │
UI ─────────────────────▶│  built-in MCP discovery  │
                         └──────────────────────────┘
  identity comes FROM an external gateway (trusted headers) OR a JWKS-verified JWT.
```

See [`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) and [`docs/`](docs/).

## License

[Apache-2.0](LICENSE).
