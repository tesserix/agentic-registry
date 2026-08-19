# Deploy

## Local (zero dependencies)

```bash
make run                       # in-memory store, API on :8080
# or the full stack with Postgres + UI:
docker compose -f deploy/docker-compose.yml up
open http://localhost:8080
```

## Container image

CI builds and pushes `ghcr.io/tesserix/agentic-registry` on every push to `main`
and on `v*` tags (`.github/workflows/release.yml`, with SBOM + provenance). To
build locally:

```bash
docker build -f deploy/Dockerfile -t agentic-registry:dev .
```

The image is a single distroless static binary that serves **both** the API and
the marketplace UI (`WEB_DIR=/app/web`).

## Where the Helm chart lives

Per the platform convention, the Helm chart is maintained in the **`tesserix-k8s`**
infra repo at `charts/apps/agentic-registry/` (with `values.yaml`,
`values-prod.yaml`, `values-local.yaml`) — **not** in this app repo. This repo
ships only the `Dockerfile` and `docker-compose.yml`. The commands below assume
`tesserix-k8s` is a sibling checkout (`../tesserix-k8s`); override with
`CHART=/path/to/tesserix-k8s/charts/apps/agentic-registry`.

## Local kind cluster (host it today)

Everything local-specific lives in the chart's `values-local.yaml`: it bundles a
PostgreSQL inside the chart, reads the DB credential from a plain K8s Secret (no
GCP Secret Manager / External Secrets), and turns off Workload Identity, KEDA,
and Istio — none of which kind has.

```bash
# 1. Provide the secret (one Secret holds POSTGRES_PASSWORD + DATABASE_URL).
make local-secret                 # cp k8s/secrets.example.yaml -> k8s/secrets.yaml
$EDITOR k8s/secrets.yaml          # defaults already work for the bundled Postgres

# 2. Build, load into kind, apply secret, and deploy — one command.
make kind-deploy                  # helm path (no Argo needed)
#   …or, to deploy via your existing Argo:
make kind-argo                    # applies k8s/argocd-application-local.yaml

# 3. Reach it.
kubectl -n agentic-registry port-forward svc/agentic-registry 8080:8080
open http://localhost:8080        # marketplace UI
curl localhost:8080/v0/health
```

What the local overlay sets vs prod:

| Concern | Local (`values-local.yaml`) | Prod (`values.yaml`) |
|---------|------------------------------|----------------------|
| DB credential | `database.mode=existingSecret` → your `k8s/secrets.yaml` | `externalSecret` ← GCP Secret Manager |
| PostgreSQL | bundled in chart (`postgresql.enabled=true`) | external CNPG cluster |
| Schema | app `AUTO_MIGRATE=true` (embedded) | `db-schema-bootstrap` CronJob; `AUTO_MIGRATE=false` |
| Image | `agentic-registry:local`, `IfNotPresent` (kind load) | `ghcr.io/tesserix/...` |
| Auth | `anonymous` (full access) | `jwks` (OIDC + scopes) |
| Workload Identity / KEDA / Istio | off | on |

For Argo: `k8s/argocd-application-local.yaml` points at the tesserix-k8s chart
path `charts/apps/agentic-registry` with `values-local.yaml`. Set its `repoURL`
to a repo your local Argo can read (the helm path via `make kind-deploy` needs no
repo at all).

> Validate the chart anytime: `make helm-lint`.

## Kubernetes (Helm)

```bash
CHART=../tesserix-k8s/charts/apps/agentic-registry
helm upgrade --install agentic-registry "$CHART" \
  --namespace agentic-registry --create-namespace \
  -f "$CHART/values.yaml" -f "$CHART/values-prod.yaml"
```

Memory-only resources, KEDA memory-trigger autoscaling, an ExternalSecret for
the DB DSN, and Workload Identity for object storage are all wired in
`charts/apps/agentic-registry/values.yaml` (in tesserix-k8s).

For CI publishers, set `AUTH_ANONYMOUS_ROLE=read` and configure
`AUTH_DEPLOY_KEYS` as comma-separated `tenant=sha256-digest` entries. The raw
Bearer keys remain only in the publishers' secret stores. Add a second entry
for the same tenant during rotation, update publishers, then remove the old
digest after the overlap window.

For Zitadel-backed human administration, configure `AUTH_MODE=jwks` (or
`trusted-header` only behind a network-contained OAuth proxy), the expected
issuer/JWKS/audience, and all three policy values below:

```bash
AUTH_GROUPS_CLAIM=urn:zitadel:iam:org:project:roles
AUTH_ADMIN_ROLE=agentregistry.admin
AUTH_ADMIN_EMAILS=samyak.rout@gmail.com,mahesh.sangawar@gmail.com
```

`AUTH_ADMIN_EMAILS` and `AUTH_ADMIN_ROLE` must be configured together or the
process refuses to start. OIDC client secrets stay in GCP Secret Manager and
reach only the OAuth proxy through External Secrets; they are not Registry
configuration.

## Tesserix GKE (ArgoCD)

This deployment follows the platform guardrails — **no manual `kubectl apply`**.

1. **Schema** lives in `tesserix-k8s/charts/apps/db-schema-bootstrap/schemas/agentic-registry/`
   (single source of truth). The `db-schema-bootstrap` CronJob applies it; the
   app runs with `AUTO_MIGRATE=false`.
2. **DB DSN** secret `prod-agentic-registry-database-url` in GCP Secret Manager,
   synced by the chart's ExternalSecret.
3. **ArgoCD app** `tesserix-k8s/argocd/prod/apps/ai-apps/agentic-registry.yaml`
   points at the in-repo chart `charts/apps/agentic-registry` (values.yaml +
   values-prod.yaml), deploying to the new namespace `agentic-registry`.
4. Image build uses the **public → CI → private** cycle for the `tesserix` org.

After the image is pushed and the `tesserix-k8s` changes are committed, ArgoCD
syncs automatically.

## Point devai at it

Set on the devai deployment (see `devai/src/devai/adapters/registry/`):

```bash
DEVAI_REGISTRY_PROVIDER=tesserix
DEVAI_REGISTRY_URL=https://registry.tesserix.app
# secure machine-to-machine auth (preferred over a static token):
DEVAI_REGISTRY_OIDC_TOKEN_URL=https://idp.example.com/realms/devai/protocol/openid-connect/token
DEVAI_REGISTRY_CLIENT_ID=devai
DEVAI_REGISTRY_CLIENT_SECRET=...        # from GCP Secret Manager
```

See [`connecting-tools.md`](connecting-tools.md) for the full secure-connection
and adapter-building contract.
