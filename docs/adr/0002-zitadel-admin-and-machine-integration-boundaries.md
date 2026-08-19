# ADR 0002: Zitadel human administration and machine integration boundaries

## Context

Agentic Registry serves a public artifact catalog, two human administrators,
GitHub publishers, Agent Gateway, kagent, and future runtime consumers. The
registry is expected to remain below 10,000 artifacts for 36 months, with 100
peak read requests per second, fewer than 2 write requests per second, an
average object below 256 KB, and read:write traffic above 100:1. The service
target is 99.9% monthly availability, p99 below 500 ms for catalog reads, and
p99 below 1 second for exports. Identity checks add no datastore hop after the
JWKS cache is warm.

Assets worth protecting are artifact integrity, non-public catalog entries,
OIDC and deploy credentials, and runtime configuration exported to Agent
Gateway. Threats include unauthenticated internet users, a Zitadel user outside
the administrator allowlist, a compromised CI job, a compromised runtime, and
a forged identity header. The internet edge, Zitadel, Registry API, GitHub
Actions, secret manager, and runtime controllers are separate trust boundaries.
Every crossing authenticates and authorizes independently.

## Decision

### Human administration

- The Tesserix Zitadel organization owns a project named `AgentRegistry`.
- The project exposes the role `agentregistry.admin` and an OIDC web app for the
  Registry administration UI.
- Only `samyak.rout@gmail.com` and `mahesh.sangawar@gmail.com` receive the
  project role and appear in `AUTH_ADMIN_EMAILS`.
- A human receives the internal `registry:admin` grant only when both their
  verified email is allowlisted and the configured external role is present.
  Email comparison is exact and case-insensitive. Possessing only one factor
  fails closed.
- Zitadel's project-role claim is a JSON object keyed by role name. The Registry
  reads those keys through `AUTH_GROUPS_CLAIM` and maps only
  `AUTH_ADMIN_ROLE=agentregistry.admin` to the internal administrator role.
- The browser obtains a short-lived Zitadel session through an OAuth proxy or
  BFF. Cookies are `Secure`, `HttpOnly`, and `SameSite=Lax`; mutation requests
  receive CSRF protection at that edge. The Registry never stores browser
  tokens or OIDC client secrets.
- `GET /v0/session` returns only `authenticated`, `email`, and the server-derived
  `admin` capability. The UI uses it to hide mutation controls. Every write is
  still authorized again by the API; the UI is not a security boundary.

### Machine integrations

- GitHub Actions publishes manifests with `POST /v0/apply` and a tenant-scoped
  deploy key. GitHub stores the raw key; Registry stores only its SHA-256 digest.
- Agent Gateway pulls catalog-derived configuration from
  `GET /v0/export/agentgateway`. kagent uses `GET /v0/export/kagent`; other
  consumers use the versioned catalog and Agent Card endpoints.
- Machine callers use a tenant deploy key or a dedicated Zitadel service user
  with short-lived client-credentials tokens and least-privilege scopes. They
  never impersonate a human email.
- Registry remains a catalog and control-plane renderer. It does not store
  GitHub tokens, Gateway credentials, provider API keys, or model credentials;
  it does not make inference calls and is not in the runtime data path.

## Failure behavior

- If Zitadel or JWKS validation is unavailable, new human administration fails
  closed. Public artifact reads remain available at the separately routed read
  boundary.
- If the OAuth proxy is unavailable, the administration UI is unavailable;
  machine publication and export continue on their independently authenticated
  routes.
- A duplicate GitHub delivery safely reapplies the same manifest. Immutable
  versions remain immutable and a tenant key cannot publish to another tenant.
- If a Gateway export call times out, the controller retains its last applied
  configuration and retries with bounded exponential backoff and jitter. The
  Registry never pushes partial configuration into the runtime.

## Rollout and rollback

Rollout is additive: deploy the Registry support, create the Zitadel project and
OIDC app, store the one-time client secret in GCP Secret Manager, configure the
OAuth proxy through GitOps, grant the two users, verify public reads and denied
third-user writes, then enable the administration route. No database migration
is required. Rollback is one Git revision for the edge configuration and one
Registry image revision; tenant deploy keys and public reads remain valid.

The incremental runtime cost is one small OAuth proxy deployment plus Zitadel
traffic already covered by the platform identity service. No new database,
queue, or credential store is introduced.

## Alternatives considered

- Put Registry in the inference path: rejected because a catalog outage would
  become an inference outage and the Registry would need provider secrets.
- Store GitHub and Gateway credentials in Registry: rejected because it expands
  the breach blast radius and duplicates Secret Manager.
- Authorize administrators by email alone: rejected because a project role is
  an independently revocable grant and prevents accidental organization-wide
  access.
- Require login for public catalog reads: rejected because public agent cards
  and discovery must remain consumable without a human session.

## Consequences

Human, CI, and runtime principals have separate credentials, permissions, and
revocation paths. Adding a future consumer requires a machine identity and an
existing API contract, not a new outbound connector inside Registry. Identity
project/app creation remains an operator action because Zitadel returns an OIDC
client secret only once and it must be written directly to Secret Manager.
