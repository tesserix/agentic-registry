# AgentGateway administration and product integration

## What each URL does

| URL | Purpose |
|---|---|
| `https://agentgateway.tesserix.app` | Tesserix desired-state administration for model providers, routes, and policies |
| `https://agentgateway.tesserix.app/ui/traffic/routes` | Solo AgentGateway's read-only XDS runtime and traffic view |
| `https://mcp.tesserix.app` | MCP server catalog, OAuth access guidance, and agent connection details |
| `https://aregistry.tesserix.app` | General Agentic Registry catalog |

Only `samyak.rout@gmail.com` and `mahesh.sangawar@gmail.com` can complete the
human login for the two gateway UIs. Registry also checks the verified Zitadel
email and `agentgateway.models` project role before accepting an administration
write. Browser visibility is not authorization; every request is checked again
on the server.

## How the control planes fit together

1. Products and administrators publish desired state to Agentic Registry.
2. Registry validates and versions `GatewayResource` and `MCPServer` artifacts.
3. The namespace-scoped reconciler pulls one integrity-stamped export and
   applies only `AgentgatewayBackend`, `HTTPRoute`, and `AgentgatewayPolicy` in
   `agentgateway-system`.
4. Solo AgentGateway converts those Kubernetes resources to XDS and keeps the
   last accepted snapshot serving inference and MCP traffic.

Registry is not in the inference path. If the UI, Registry, or reconciler is
temporarily unavailable, existing model and MCP requests continue. New writes
fail closed and reconcile on a later run.

## Connect a product to model routing

Use a Zitadel machine user or service account with the
`agentgateway.models` project role. Request a short-lived token with the
AgentGateway project audience, then send it to the public listener:

```bash
export TESSERIX_AGENTGATEWAY_TOKEN='<short-lived OAuth access token>'

curl --fail-with-body \
  -H "Authorization: Bearer ${TESSERIX_AGENTGATEWAY_TOKEN}" \
  -H 'Content-Type: application/json' \
  https://agentgateway.tesserix.app/openai/v1/chat/completions \
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}'
```

Available public route prefixes are visible in the admin UI. The prefix is
removed before forwarding, so `/openai/v1/chat/completions` reaches the OpenAI
provider as `/v1/chat/completions`. Products must set request deadlines, retry
only timeouts/429/5xx with jitter, and never log bearer tokens or model payloads.

Provider credentials are not stored in Registry. Current providers use caller
credential passthrough or GCP workload identity. Any future platform credential
remains in GCP Secret Manager and is referenced by GitOps; tenant-owned BYO keys
belong in OpenBao.

## Connect an agent to MCP

Select a server in `https://mcp.tesserix.app`, obtain a machine token with the
`agentgateway.mcp` role, and use the displayed endpoint:

```text
https://mcp.tesserix.app/mcp/<server-name>
```

The MCP UI provides ready-to-copy settings for Codex, Claude Code, Cursor, and
VS Code. Products should keep the token in their runtime secret store and send
it as `Authorization: Bearer ...`; it must never be embedded in source or a
frontend bundle.

## Administration API

The UI calls these same-origin endpoints:

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/v0/agentgateway/resources` | List the administrator-visible desired state |
| `PUT` | `/v0/agentgateway/{backends|routes|policies}/{name}` | Idempotently create or replace one resource |
| `DELETE` | `/v0/agentgateway/{backends|routes|policies}/{name}` | Idempotently remove one resource |
| `POST` | `/v0/agentgateway/import` | Tenant-writer batch import of an allowlisted Kubernetes `List` |
| `GET` | `/v0/export/agentgateway` | Reconciler export with count and digest headers |

Every resource is limited to 1 MiB. The API accepts only these GVKs:

- `agentgateway.dev/v1alpha1/AgentgatewayBackend`
- `agentgateway.dev/v1alpha1/AgentgatewayPolicy`
- `gateway.networking.k8s.io/v1/HTTPRoute`

The namespace is forced to `agentgateway-system`. Server-managed metadata,
Secret objects, credential-shaped fields, workloads, RBAC, Gateways, and
AgentgatewayParameters are rejected. Those remain in GitOps.

## Safe change and rollback

Create or edit a resource in the UI, wait for the next reconciliation, and use
the runtime traffic view to confirm it was accepted. Deletion first removes
desired state; the data plane keeps the previous snapshot until the reconciler
prunes that specifically Registry-managed object.

For a control-plane rollback, restore the previous Registry revision. For the
ownership migration, restore the Helm templates from the capture commit in
`tesserix-k8s` and disable Registry pruning before syncing. The resource names
and runtime endpoints remain unchanged during either rollback.
