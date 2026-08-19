# Connecting Tools Securely & Building Adapters

This is the canonical guide for any tool — devai, an agentic gateway, a CI job,
another platform — to connect to Agentic Registry **securely** and build an
adapter against it.

> **Remember the invariant:** the registry is a catalog, not an auth server and
> not a proxy. It **issues no tokens** and **proxies no traffic**. Your tool
> authenticates *to* it with a credential minted by *your* identity provider;
> the registry only **verifies** that credential. This guide is about how your
> tool obtains and presents that credential.

---

## 1. The trust model at a glance

```
 ┌─────────────┐  1. client_credentials      ┌──────────────┐
 │  your tool  │ ───────────────────────────▶│   IdP        │
 │ (devai, …)  │     (client_id + secret)     │ (Keycloak)   │
 │             │ ◀─────────────────────────── │  /token      │
 │             │  2. short-lived JWT           └──────────────┘
 │             │     scope=registry:read registry:write
 │             │                               ┌──────────────┐
 │             │  3. GET /v0/...  Bearer <JWT> │  agentic-    │
 │             │ ───────────────────────────▶ │  registry    │
 │             │                               │  verifies via│
 │             │ ◀─────────────────────────── │  JWKS (4)    │
 └─────────────┘  5. JSON                      └──────────────┘
```

1. Your tool is a registered **OIDC client** in your IdP (Zitadel, Auth0,
   Okta, Entra, …). Its `client_secret` lives in a **secret manager**, never in
   code and never in the registry.
2. It performs the **client-credentials grant** to get a **short-lived JWT**
   carrying scopes.
3. It calls the registry with `Authorization: Bearer <JWT>`.
4. The registry verifies the JWT against the IdP's **JWKS** (signature, `iss`,
   `aud`, `exp`) and reads its **scopes** and **groups**.
5. The registry returns data filtered by the caller's visibility/RBAC.

No secret is ever shared with the registry. Tokens are short-lived, so a leak is
self-limiting.

## 2. Registry configuration (the operator does this once)

Run the registry in `jwks` auth mode pointed at your IdP:

```bash
AUTH_MODE=jwks
AUTH_JWKS_URL=https://idp.example.com/realms/main/protocol/openid-connect/certs
AUTH_ISSUER=https://idp.example.com/realms/main
AUTH_AUDIENCE=agentic-registry        # optional but recommended
AUTH_GROUPS_CLAIM=groups              # claim carrying tenant roles
```

For Tesserix Zitadel human administration, use the project-role object claim
and require both the project role and the explicit email allowlist:

```bash
AUTH_GROUPS_CLAIM=urn:zitadel:iam:org:project:roles
AUTH_ADMIN_ROLE=agentregistry.admin
AUTH_ADMIN_EMAILS=samyak.rout@gmail.com,mahesh.sangawar@gmail.com
```

Zitadel encodes project roles as object keys; Agentic Registry reads those keys
without trusting their nested display values. A human with the role but an
email outside the allowlist receives no write grant. Machine identities remain
subject to scopes and tenant roles and should not carry human email claims.

In-cluster, also put the registry behind the mesh so only known workloads reach
it (defense in depth): Istio `RequestAuthentication` + `AuthorizationPolicy`, or
the `trusted-header` mode where a gateway forwards a *pre-validated* identity via
`X-Forwarded-User/Email/Groups` — trusted **only** over mTLS from that gateway.

## 3. Scopes & roles

| Scope | Grants |
|-------|--------|
| `registry:read`  | read non-public artifacts the caller's tenant/role allows |
| `registry:write` | publish / delete within the caller's tenant |
| `registry:admin` | implies both, everywhere |

Public artifacts need **no** token. Scopes are enforced **only** when the token
carries them, so group-based and local-dev callers keep working. Tenant
membership comes from a `tenant`/`tid` claim; roles from group claims
(`<tenant>:reader|writer|admin`, or `registry:admin`).

> The registry always applies the **visibility/RBAC pre-filter before** any
> label selector. A selector can never widen what a caller may see.

## 4. Get a token (any language)

```bash
curl -s -X POST "$AUTH_TOKEN_URL" \
  -d grant_type=client_credentials \
  -d client_id="$CLIENT_ID" \
  -d client_secret="$CLIENT_SECRET" \
  -d scope="registry:read registry:write" | jq -r .access_token
```

Then:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "$REGISTRY/v0/skills?labelSelector=language=go,domain in (code-review)"
```

## 5. Build an adapter (the devai reference)

devai wraps the registry behind an **adapter family** so it can swap backends
(`tesserix`, `solo_aregistry`, `mcp_registry`, `portkey`) with one env var, and
never imports the registry's HTTP details into business logic. Copy this shape:

```python
# 1. obtain a short-lived token via client-credentials (cached + auto-refresh)
from devai.adapters.registry.oidc import ClientCredentialsTokenProvider
tokens = ClientCredentialsTokenProvider(
    token_url=os.environ["REGISTRY_OIDC_TOKEN_URL"],
    client_id=os.environ["REGISTRY_CLIENT_ID"],
    client_secret=os.environ["REGISTRY_CLIENT_SECRET"],  # from secret manager
    scopes="registry:read registry:write",
)

# 2. present it on every call
headers = {"Authorization": f"Bearer {tokens.token()}", "Accept": "application/json"}
requests.get(f"{REGISTRY}/v0/skills", headers=headers)
```

The minimum surface an adapter implements (see
[`devai/src/devai/adapters/registry/base.py`](../../devai/src/devai/adapters/registry/base.py)):

- `list_<kind>()` / `get_<kind>(name)` for each kind
- `discover(plural, label_selector)` — the gateway-neutral runtime contract
- `publish(envelope)`
- `health_check()` — must never raise; degrade to `ok=False`

**Rules for any consumer adapter** (proven by devai's contract tests):

1. **Never** persist the client secret in the registry or in code — secret
   manager only.
2. Prefer **client-credentials short-lived tokens** over a static token.
3. The adapter **never raises** out of the family — a registry blip degrades to
   local fallback, it does not crash your tool.
4. Lazy-import the HTTP SDK; the registry must be optional.

## 6. Secret placement (Tesserix deployments)

| Secret | Where it lives | How it reaches the tool |
|--------|----------------|--------------------------|
| Registry OIDC `client_secret` | **GCP Secret Manager** | External Secret → K8s Secret → env `REGISTRY_CLIENT_SECRET` |
| Registry DB password | GCP Secret Manager | only the registry pod, via External Secret |
| JWKS URL / issuer / audience | non-secret config | env / ConfigMap |

The registry's own database holds **no** consumer secrets — it only verifies
tokens it never minted.

## 7. GitHub Actions publisher

GitHub publishes manifests into one tenant; Registry does not call GitHub and
does not store a GitHub token. Put the raw tenant deploy key in a GitHub Actions
environment secret and put only its SHA-256 digest in Registry configuration:

```yaml
- name: Publish agent manifests
  env:
    REGISTRY_URL: https://aregistry.tesserix.app
    REGISTRY_DEPLOY_KEY: ${{ secrets.AGENTIC_REGISTRY_DEPLOY_KEY }}
  run: |
    curl --fail-with-body --silent --show-error \
      --request POST \
      --header "Authorization: Bearer ${REGISTRY_DEPLOY_KEY}" \
      --header "Content-Type: application/yaml" \
      --data-binary @agents.yaml \
      "${REGISTRY_URL}/v0/apply"
```

The workflow must be environment-protected and must not expose the secret to
fork pull requests. Reapplying the same content is safe; explicit immutable
version tags reject conflicting content.

## 8. Agent Gateway and other consumers

Agent Gateway pulls rendered control-plane configuration instead of receiving
an outbound push from Registry:

```bash
curl --fail-with-body --silent --show-error \
  --header "Authorization: Bearer ${REGISTRY_TOKEN}" \
  "${REGISTRY_URL}/v0/export/agentgateway?namespace=tesserix&targetNamespace=agentgateway-system"
```

Use a dedicated machine identity or a read-scoped tenant credential. Apply the
result through the owning GitOps/controller process. kagent uses
`/v0/export/kagent`; generic consumers use `/v0/{collection}` and A2A Agent
Cards. Provider and model credentials remain in the Agent Gateway secret
boundary and never enter Registry.
