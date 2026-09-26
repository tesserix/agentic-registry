# ADR 0005: Self-service publisher identity and CLI credentials

## Status

Accepted for implementation by issue #95.

## Context

Agentic Registry is already a tenant-aware OAuth resource server. Browser
traffic arrives through the deployed Zitadel gateway and machine clients can
present JWKS-verified access tokens or controlled deploy keys. External users
still need operator help to obtain publishing authority, and the CLI currently
expects a manually copied bearer token stored in a JSON file.

Initial sizing assumes 10,000 tenants, 100,000 artifacts, 10 credential
mutations and 20 artifact writes per second at peak, and threefold growth over
36 months. Authentication and catalog APIs target 99.9% monthly availability.
Normal reads target p99 below 300 ms and one-artifact publication below 500 ms,
excluding identity-provider latency.

Publishing authority, private manifests, ownership metadata, and audit evidence
are valuable assets. Attackers include unauthenticated internet traffic, a user
from another tenant, a compromised CLI or CI job, credential-stuffing bots, and
an insider. Trust crosses browser or CLI, Zitadel, the gateway, Registry, and
Postgres. Tenant, role, namespace, and artifact permissions must come from a
verified identity rather than a request body.

## Decision

Registry remains a resource server and never owns passwords, signs access
tokens, stores plaintext client secrets, or exposes a Zitadel administrative
credential to the browser.

### Human authentication

The marketplace keeps the existing OIDC browser session. `agentic auth login`
uses OAuth device authorization when the configured public client supports it,
with authorization code plus PKCE as the browser fallback. The CLI stores
refresh material in the operating-system credential store and keeps access
tokens only until their short expiry. A JSON-file fallback must be explicitly
enabled and warned about.

### Automation credentials

The Registry UI exposes **Settings / API credentials**, backed by a server-side
identity-control-plane adapter. The adapter creates, lists, rotates, and revokes
tenant-scoped OAuth clients in Zitadel. A generated client secret is displayed
once. Registry artifact endpoints receive only short-lived access tokens minted
by Zitadel; Registry Postgres stores no credential verifier.

Credential policy includes tenant, allowed namespaces and artifact kinds,
expiry, and least-privilege scopes. New scopes are `registry:read`,
`registry:publish`, and `registry:delete`; `registry:write` remains a temporary
alias for publish plus delete. `registry:admin` remains platform-only.

The initial credential lifetime defaults to 30 days and is capped at 90 days.
Access tokens target five to fifteen minutes. Rotation may overlap old and new
credentials until a declared cutover. Revocation stops new tokens immediately;
already issued access lasts only until its bounded expiry.

### Tenant onboarding

First login idempotently creates or joins a Zitadel organization and Registry
tenant mapping, a generated default namespace, and an owner membership. The
workflow is durable because identity, membership, and namespace provisioning
cross system boundaries. Workflow identity derives from verified subject and
requested tenant slug. Self-signup never grants global `registry:admin`.

Branded or reverse-DNS namespaces require proof of domain ownership. New
artifacts default to private. Public visibility is an explicit audited state
transition.

### CLI publication

The CLI gains `auth login`, `auth status`, `auth logout`, `validate`, and
`apply --dry-run`. CI exchanges injected client credentials for a short-lived
token and never accepts secrets as process-list-visible command arguments.
Mutations carry an idempotency key. A multi-document apply validates completely
and commits atomically in Registry Postgres or writes nothing.

The public error contract is `{code, message, request_id}`. Lists use cursor
pagination and server-enforced limits. Secret-shaped fields are rejected from
searchable manifests, and logs record identifiers and outcomes rather than
tokens or artifact payloads.

## Dependency failure behavior

- Zitadel down: public reads and valid unexpired bearer reads continue with
  cached JWKS inside policy; new login, token refresh, tenant onboarding, and
  credential mutation fail explicitly.
- Onboarding activity fails: idempotent activities retry with capped jitter;
  terminal state becomes `needs_support`, never a partially ready tenant.
- Registry times out after publish commit: a retry with the same idempotency key
  returns the original version.
- Semantic indexing down: the strongly consistent publish succeeds with
  `indexing_pending`; the outbox retries and alerts on lag.
- Audit/outbox commit fails: the state mutation fails because intent and state
  must be atomic.

## Consistency, cost, and rollout

Artifact state, idempotency result, and audit/outbox intent commit in one
Postgres transaction. Zitadel remains authoritative for credentials and
memberships. Valkey may rate-limit or cache but is not authoritative. At the
stated scale the credential metadata and idempotency rows remain a small,
indexed Postgres workload; no additional datastore is justified.

Rollout independently gates self-signup, API credential creation, device login,
and new scopes by tenant: internal dogfood, allowlisted external beta, public
preview, then general availability. Existing deploy keys and manual tokens stay
available during a dated migration. Rollback disables new onboarding or
credential creation without deleting tenants or artifacts. Destructive schema
cleanup and production rollout require separate approval and GitOps delivery.

## Alternatives rejected

- Mint Registry-specific API keys in Registry: makes the catalog an identity
  provider and creates another secret, rotation, breach, and audit boundary.
- Keep copying long-lived bearer tokens into `~/.agentic/config.json`: exposes
  them to filesystem backups and gives no safe browser login or revocation UX.
- Let self-signup users become Registry administrators: collapses tenant and
  platform administration.
- Put client secrets into artifact manifests or Registry metadata: makes them
  searchable and impossible to rotate independently.
- Provision every user manually: does not meet the external developer journey
  and cannot scale to the stated tenant target.

## Consequences

The marketplace can present one coherent onboarding experience without moving
token authority into Registry. The implementation depends on a narrowly scoped
Zitadel management adapter and secure gateway forwarding. CLI login becomes
more complex, but tokens become short-lived, revocable, and safely stored.
