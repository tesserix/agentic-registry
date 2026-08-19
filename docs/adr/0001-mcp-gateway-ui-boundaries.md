# ADR 0001: Separate MCP Gateway UI and machine API boundaries

## Context

`mcp.tesserix.app` serves two different consumers: approved operators browsing
the MCP catalog and external agents invoking MCP servers. The registry is the
catalog source of truth, while AgentGateway is the runtime data plane. Mixing
the browser UI and machine API behind one authentication behavior either makes
the UI unusable or weakens the API boundary.

The initial catalog contains about 50 MCP servers and is expected to remain
below 500 entries for 36 months. Planning assumptions are 20 peak browser
requests per second, 256 KB maximum registry responses, a read/write ratio over
100:1, and 2x annual traffic growth. The target is 99.9% monthly availability,
p99 below 500 ms for catalog reads, and p99 below 1 s for resolved server detail.
The UI adds no datastore and negligible infrastructure cost beyond its existing
Agentic Registry deployment and an OAuth proxy.

Assets worth protecting are machine credentials, bearer tokens, MCP tool
capabilities, the AgentGateway admin surface, and registry management actions.
Threats include unauthenticated internet clients, a compromised agent, an
approved human account, and a compromised dependency. The browser OAuth proxy,
registry API, and AgentGateway data plane are separate trust boundaries. Each
boundary authenticates and authorizes independently and fails closed.

## Decision

- `mcp.tesserix.app` browser paths serve a Tesserix MCP Gateway UI backed by the
  existing Agentic Registry `/v0` API.
- `/mcp/{registered-server-name}` remains a machine API routed to the MCP data
  plane. Registry names are never rewritten for display purposes.
- Browser paths use Zitadel authorization-code login and an explicit two-person
  allowlist. Machine paths require Zitadel bearer tokens with verified issuer,
  audience, expiry, algorithm, and the `agentgateway.mcp` project role.
- Every external agent receives an isolated OAuth client. Agents exchange its
  credential for a short-lived token at runtime. We do not create gateway PATs,
  expose credential values in the frontend, or store bearer tokens in browser
  storage.
- The UI presents install configuration for supported agent clients. The
  registry remains the only catalog; the UI does not maintain a second MCP
  inventory.
- A browser playground is deferred until a dedicated same-origin backend can
  enforce server allowlisting, inject a short-lived user token, apply CSRF
  protection, bound request size and duration, and avoid returning tokens to
  JavaScript. The frontend will not proxy arbitrary URLs.

## Failure behavior

- If Agentic Registry is unavailable, the UI shell loads and shows a bounded
  catalog error; it does not fall back to stale or invented server data.
- If resolution fails, the server detail page reports the registry error and
  does not generate a guessed upstream endpoint.
- If Zitadel, JWT validation, role authorization, or subject rate limiting
  fails, access is denied. There is no anonymous or static-key fallback.
- Repeated reads are safe. The UI performs no cross-system mutation and
  therefore needs no saga, compensation, or idempotency key.

## Alternatives considered

- Reuse the generic Agentic Registry UI: rejected because it exposes unrelated
  artifact-management concepts and does not provide an MCP-client workflow.
- Route browser roots directly to AgentGateway: rejected because the MCP data
  plane is an API, not an MCP catalog UI.
- Store static gateway access tokens: rejected because one leak has broad blast
  radius and rotation requires coordinated client changes.
- Build a second MCP catalog: rejected because two inventories will drift.

## Consequences

The same frontend image selects its product experience by hostname, so the
existing registry deployment remains compatible. GitOps must route browser and
machine prefixes separately and must keep AgentGateway admin and controller
services private. Rollback is one image and route revision. No data migration
is required. A failed frontend rollout can be reverted independently from the
machine data plane.
