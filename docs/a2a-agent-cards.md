# A2A Agent Cards

The registry renders every `Agent` artifact into a standards-compliant
[A2A (Agent2Agent)](https://a2a-protocol.org) **Agent Card** — the JSON
capability descriptor a runtime consumer fetches to learn *what* an agent can
do and *where/how* to call it.

The registry is a **catalog/control-plane, never a proxy**. It serves the card
(discovery + trust); the consumer then talks A2A **directly** to the agent's own
service `url`. The registry never sits on the agent-to-agent request path — same
principle as `/v0/prompts/{name}/render`.

## Authoring an Agent

Add an optional `a2a` block and a `skills` list to an `Agent` artifact. Skill
entries that are **strings** resolve to real registry `Skill` objects in the
same namespace (single source of truth, reused, digest-tracked); **maps** are
inline one-offs. Any unknown field under `a2a` passes straight through to the
card, so newer A2A fields work without a registry change.

```yaml
apiVersion: registry.agentic.dev/v1alpha1
kind: Agent
metadata:
  name: oncall-responder
  namespace: sre            # namespace = org or org-team (uniqueness scope)
  tag: "1.2.0"
spec:
  title: On-Call Responder
  description: Watches alerts, investigates, proposes remediations.
  a2a:
    url: https://oncall-responder.sre.svc.cluster.local/a2a/v1   # the agent's OWN endpoint
    preferredTransport: JSONRPC          # JSONRPC | GRPC | HTTP+JSON
    provider: { organization: Tesserix, url: https://tesserix.app }
    capabilities: { streaming: true, pushNotifications: true }
    defaultInputModes:  [application/json, text/plain]
    defaultOutputModes: [application/json, text/plain]
  skills:
    - kubernetes-troubleshooter          # → resolves a registry Skill
    - id: propose-remediation            # → inline skill
      name: Propose Remediation
      description: Drafts a rollback/scale/patch plan and opens a PR.
      tags: [sre, remediation]
```

## Fetching a card

| Endpoint | Returns |
|---|---|
| `GET /v0/agents/{name}/card?namespace=<ns>` | A2A card for the latest version |
| `GET /v0/agents/{name}/{tag}/card?namespace=<ns>` | A2A card for a pinned version |
| `GET /v0/agents/{name}/.well-known/agent-card.json` | Same card, via the A2A well-known convention |
| MCP tool `get_agent_card` | Same card, over the built-in MCP discovery server |

The rendered card adds a **provenance** entry under `capabilities.extensions`
(`uri: …/ext/provenance`) carrying the agent's `arn`, `digest`, `ref`, and —
when registry signing is enabled — a `signature`, so a consumer can verify the
card came from this registry and pin the exact artifact.

`version` always comes from the artifact tag and `skills[]` from real registry
data — a publisher cannot spoof either through the `a2a` block.

## Runtime fetch-and-use (the consumer side)

A consuming pod does **discover → fetch card → call directly**:

```
1. search/list agents           GET /v0/search?q=kubernetes   (or list_agents)
2. fetch the agent's card        GET /v0/agents/oncall-responder/card?namespace=sre
3. read card.url + card.skills
4. talk A2A to card.url          POST card.url  {jsonrpc, method: "message/send", …}
```

DevAI ships this consumer at `devai.a2a` (`src/devai/a2a/`):

```python
from devai.a2a import create_a2a_client

a2a = create_a2a_client(settings, registry_client)     # None-safe, additive
card = a2a.fetch_card("oncall-responder", namespace="sre")
# or route by capability:
card = a2a.find_for_capability("kubernetes")
result = a2a.send_message(card, "Pod is CrashLoopBackOff — propose a fix.")
```

The registry is only touched for steps 1–2; step 4 goes straight to the agent.
