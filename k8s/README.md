# k8s/ — copy-and-go deploy aids

These live in the product repo on purpose: anyone can copy them and stand the
registry up on their own cluster without needing any private infra repo.

| File | What it is | What to do |
|------|------------|------------|
| [`secrets.example.yaml`](secrets.example.yaml) | The **one** Secret the registry needs (DB password + DSN; optional OIDC client secret) | `cp secrets.example.yaml secrets.yaml`, edit, `kubectl apply -f secrets.yaml`. `secrets.yaml` is gitignored. |
| [`argocd-application-local.yaml`](argocd-application-local.yaml) | An ArgoCD Application for a local/kind cluster | Set `repoURL` to a repo your Argo can read, then `kubectl apply -f`. |

## Quickest path (kind, no Argo needed)

```bash
cp k8s/secrets.example.yaml k8s/secrets.yaml      # defaults work as-is for the bundled Postgres
kubectl create namespace agentic-registry --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f k8s/secrets.yaml
make kind-deploy                                  # build + kind load + helm install
kubectl -n agentic-registry port-forward svc/agentic-registry 8080:8080
```

## What the Secret carries

- `POSTGRES_PASSWORD` — password for the bundled local PostgreSQL.
- `DATABASE_URL` — the DSN the server connects with. Host is the in-cluster
  service the chart creates: `agentic-registry-postgresql`. Keep the password in
  this DSN identical to `POSTGRES_PASSWORD`.
- `REGISTRY_CLIENT_SECRET` *(optional)* — only if you run `AUTH_MODE=jwks` and
  want a consumer (e.g. devai) to authenticate via OAuth client-credentials. The
  registry itself never needs a secret — it only verifies tokens.

## Where the Helm chart lives

The chart is maintained in the **tesserix-k8s** infra repo at
`charts/apps/agentic-registry/` (platform convention). For a pure local run
without that repo, use Docker Compose instead:

```bash
docker compose -f deploy/docker-compose.yml up
```

See [`../docs/deploy.md`](../docs/deploy.md) for the full matrix and the
[`../docs/connecting-tools.md`](../docs/connecting-tools.md) secure-connection guide.
