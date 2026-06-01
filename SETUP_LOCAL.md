# Local setup — Agentic Registry on `sandboxctl`

Run the whole stack (Go API + React/Vite marketplace UI + pgvector search) on a
local Kubernetes sandbox using [`sandboxctl`](https://github.com/tesserix/sandboxctl)
— kind + Argo CD + Istio + an in-cluster registry/Gitea, all behind
`https://*.sandbox.app:8443`.

Postgres is **not** bundled with this app for local runs — it uses the shared
**`local-infra`** CNPG Postgres in the `local-infra` namespace
(`local-pg-rw.local-infra.svc.cluster.local:5432`, db/role `registry`). Deploy
`local-infra` first (step 2b).

> `sandboxctl deploy` automatically renders the chart's `values-local.yaml` and
> applies `k8s/secrets.yaml` — both already provided. `values-local.yaml` turns
> on two niceties for local: `seedExamples: "true"` (auto-load a starter
> catalog) and `vectorSearch: "true"` (pgvector cosine ranking + the ⌘K palette).

---

## 0. Prerequisites (one time)

```sh
command -v sandboxctl >/dev/null || brew install tesserix/tap/sandboxctl
# Podman is the VM backend on macOS; sandboxctl manages the podman machine.
```

To run `kubectl` against the sandbox, point it at sandboxctl's kubeconfig:

```sh
export KUBECONFIG=~/.sandboxctl/kubeconfig   # context: kind-sandboxctl
```

## 1. Fill in local secrets

```sh
cp k8s/secrets.example.yaml k8s/secrets.yaml   # gitignored; defaults work as-is
```

## 2. Bring the platform up (first run ≈ 10 min, with CNPG)

```sh
sandboxctl up --podman-disk 80 --podman-memory 12g --with-cnpg
```

`--with-cnpg` installs the CloudNativePG operator the shared Postgres needs.
Subsequent `up`s are fast; `deploy` skips `up` if the cluster is already running.

## 2b. Deploy the shared local-infra datastores (once per sandbox)

```sh
sandboxctl deploy --chart ../tesserix-k8s/charts/apps/local-infra --name local-infra --no-build
```

## 2c. Enable pgvector (once per database)

The app's `registry` role isn't a superuser, so it can't `CREATE EXTENSION`
itself — enable `vector` once as the CNPG superuser (persists across restarts):

```sh
kubectl exec -n local-infra local-pg-1 -c postgres -- \
  psql -U postgres -d registry -c 'CREATE EXTENSION IF NOT EXISTS vector;'
```

The registry **auto-detects** the extension on its next start, adds the
`embedding vector(256)` column + HNSW index, and backfills embeddings. If you
skip this step the registry still runs — it just falls back to substring search
(you'll see `pgvector unavailable, using substring search` in the logs).

## 3. Access & status

```sh
sandboxctl creds      # URLs + admin passwords
sandboxctl status     # cluster + component health
```

## 4. Deploy Agentic Registry

The Helm chart lives in the infra repo (`tesserix-k8s`, a sibling checkout), so
point `--chart` at it. Run from **this** repo root (it holds `deploy/Dockerfile`
and `sandboxctl.yaml`):

```sh
cd /path/to/agentic-registry
sandboxctl deploy --chart ../tesserix-k8s/charts/apps/agentic-registry --values values-local.yaml
```

This builds the image (web UI + Go binary, multi-arch native), pushes it to the
in-cluster registry, applies `k8s/secrets.yaml`, renders the chart, and lets
Argo CD sync it. On first boot the store is empty, so the embedded **starter
catalog (14 artifacts across every kind)** is auto-applied — the catalog and the
⌘K search are populated immediately.

Open **https://agentic-registry.sandbox.app:8443** and press **⌘K** (Ctrl-K) to
search across every kind. Health check:

```sh
curl -k https://agentic-registry.sandbox.app:8443/v0/health
curl -k "https://agentic-registry.sandbox.app:8443/v0/search?q=kubernetes"
```

## 5. Iterate on code

`values-local.yaml` sets `pullPolicy: Always`, so every redeploy re-pulls the
freshly built image — no stale-layer surprises:

```sh
sandboxctl deploy --chart ../tesserix-k8s/charts/apps/agentic-registry --values values-local.yaml
# chart-only change (no rebuild): add --redeploy
```

Publish your own artifacts any time (kubectl-apply-style multi-doc YAML):

```sh
curl -k -X POST --data-binary @examples/skill.yaml \
  https://agentic-registry.sandbox.app:8443/v0/apply
```

## 6. Keep images & disk clean

```sh
podman system prune -a -f --volumes && podman builder prune -af
```

## 7. Reset / tear down

```sh
# Reset just this product's data (re-seeds on next restart):
kubectl exec -n local-infra local-pg-1 -c postgres -- \
  psql -U postgres -d registry -c 'TRUNCATE registry.artifacts;'
kubectl rollout restart deploy/agentic-registry -n agentic-registry

sandboxctl undeploy --name agentic-registry   # remove just this app
sandboxctl down                               # wipe the whole sandbox
```

---

## Troubleshooting

| Symptom | Cause & fix |
|---------|-------------|
| `CreateContainerConfigError: … runAsNonRoot and image has non-numeric user` | The image runs as the numeric uid **1000** (`aregistry`) and the chart sets `runAsUser: 1000`. If you see this, you're on an old image/chart — redeploy. |
| Catalog empty after deploy | `seedExamples` must be `"true"` in `values-local.yaml`, and seeding only runs when the store is empty. Truncate + restart (step 7) to force a re-seed, or `POST /v0/apply`. |
| Search returns substring matches, not ranked | pgvector isn't enabled — run step **2c**, then `kubectl rollout restart deploy/agentic-registry -n agentic-registry`. Confirm with `… psql -U postgres -d registry -tAc "SELECT count(embedding) FROM registry.artifacts;"`. |
| Redeploy shows the old UI/binary | Ensure `pullPolicy: Always` in `values-local.yaml` (default here). The node caches the moving `:latest` tag otherwise. |
| Pod stuck waiting for DB | Confirm `local-infra` is up (`kubectl -n local-infra get pods`) and the `registry` db/role exist. |
| `kubectl` hits the wrong cluster | `export KUBECONFIG=~/.sandboxctl/kubeconfig` (context `kind-sandboxctl`). |
