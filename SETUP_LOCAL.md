# Local setup — Agentic Registry on `sandboxctl`

Run the whole stack (API + marketplace UI) on a local Kubernetes sandbox using
[`sandboxctl`](https://github.com/tesserix/sandboxctl) — kind + Argo CD + Istio +
an in-cluster registry/Gitea, all behind `https://*.sandbox.app:8443`.

Postgres is **not** bundled with this app anymore — it uses the **shared
`local-infra` datastores** (CNPG Postgres, Redis, NATS, Mongo) in the
`local-infra` namespace. Deploy `local-infra` FIRST (see Prerequisites); this
product auto-connects via the reflected `local-infra-creds` Secret + stable DNS
(`local-pg-rw.local-infra.svc.cluster.local:5432`, db/role `registry`).

> `sandboxctl deploy` automatically uses the chart's `values-local.yaml` and
> applies `k8s/secrets.yaml` — both already provided in this repo.

## 0. Prerequisites (one time)

```sh
# Install sandboxctl if you don't have it yet:
command -v sandboxctl >/dev/null || brew install tesserix/tap/sandboxctl
# Podman is the VM backend on macOS; sandboxctl manages the podman machine.
```

## 1. Fill in local secrets

```sh
cp k8s/secrets.example.yaml k8s/secrets.yaml   # k8s/secrets.yaml is gitignored
# defaults work as-is for the bundled local Postgres; edit if you want real auth
```

## 2. Bring the platform up (first run ≈ 10 min) — with CNPG

```sh
sandboxctl up --podman-disk 80 --podman-memory 12g --with-cnpg
```

The **first** `up` pulls images for kind, Argo CD, Istio, Gitea, the registry,
cert-manager, etc. — give it **at least 10 minutes**. Subsequent `up`s are fast
(and `deploy` skips `up` if the cluster is already running). `--with-cnpg`
installs the CloudNativePG operator that the shared `local-infra` Postgres needs.

## 2b. Deploy the shared local-infra datastores (FIRST, once per sandbox)

All products share one set of datastores in the `local-infra` namespace. Deploy
it before any product:

```sh
sandboxctl deploy --chart ../tesserix-k8s/charts/apps/local-infra --name local-infra --no-build
```

This brings up the shared CNPG Postgres, Redis, NATS and Mongo, and creates the
`local-infra-creds` Secret that reflector copies into each product namespace
(including `agentic-registry`). Products then auto-connect via that reflected
Secret + stable DNS — no per-app database to deploy.

## 3. Get access credentials & status

```sh
sandboxctl creds      # URLs + admin passwords (Argo CD, Gitea, registry, …)
sandboxctl status     # cluster + component health
```

## 4. Deploy Agentic Registry

The chart lives in the infra repo (`tesserix-k8s`, a sibling checkout), so point
`--chart` at it. Run from **this** repo root (it holds the `Dockerfile`):

```sh
cd /path/to/agentic-registry
sandboxctl deploy --chart ../tesserix-k8s/charts/apps/agentic-registry --purge-old-tags
```

This builds the image from `deploy/Dockerfile`, pushes it to the in-cluster
registry, applies `k8s/secrets.yaml`, renders the chart with `values-local.yaml`,
and lets Argo CD sync it. The app connects to the shared `local-infra` Postgres
(`local-pg-rw.local-infra.svc.cluster.local`, db `registry`) — make sure you
deployed `local-infra` (step 2b) first.

Open: **https://agentic-registry.sandbox.app:8443**
(or `curl -k https://agentic-registry.sandbox.app:8443/v0/health`)

## 5. Keep images & disk clean

The build cache and old image tags fill the Podman VM fast. Prune regularly:

```sh
podman system prune -a -f --volumes && podman builder prune -af
```

`--purge-old-tags` on `deploy` also drops superseded image tags from the
in-cluster registry each build.

## 6. Redeploy / tear down

```sh
sandboxctl deploy --chart ../tesserix-k8s/charts/apps/agentic-registry --purge-old-tags   # after code changes
sandboxctl undeploy --name agentic-registry                                               # remove just this app
sandboxctl down                                                                           # wipe the whole sandbox
```

## Wipe / reset registry data

Data lives in the shared `local-infra` Postgres. To reset just this product's
slice (db `registry`, Redis logical DB 4) without touching other products:

```sh
../tesserix-k8s/charts/apps/local-infra/clean-product.sh agentic-registry
```

## Troubleshooting

- **Pod CrashLoopBackOff on first boot** — it waits for the database; confirm
  the shared `local-infra` stack is up (`kubectl -n local-infra get pods`) and
  that the `local-infra-creds` Secret was reflected into the
  `agentic-registry` namespace.
- **No StorageClass** — the shared CNPG / datastores live in `local-infra`; set
  `persistence: emptyDir` in
  `../tesserix-k8s/charts/apps/local-infra/values.yaml` if your kind cluster has
  no default StorageClass.
