# Contributing to Agentic Registry

Thanks for helping build an open, self-hostable, gateway-neutral agentic registry.

## Developer Certificate of Origin (DCO)

All commits **must** be signed off. By signing off you certify the
[DCO](https://developercertificate.org/). Add the trailer with:

```bash
git commit -s -m "feat(api): add label-selector parser"
```

This appends `Signed-off-by: Your Name <you@example.com>`. The DCO check on every
PR enforces it. We use DCO (not a CLA) — the CNCF / Linux-kernel norm.

## Dev setup

```bash
# Backend (Go 1.26+)
make run            # starts the API on :8080 with the in-memory store (no Postgres needed)
make test
make lint           # gofmt + go vet

# Full local stack (Postgres + server + UI)
docker compose up

# Frontend (Node 20+)
cd web && pnpm install && pnpm dev   # Vite dev server on :5173
```

## Conventions

- **Commits:** Conventional Commits (`feat(scope):`, `fix(scope):`, `chore:`…).
- **Branches:** feature branches off `main`; PRs require green CI + a CODEOWNERS review.
- **Architecture invariant:** the registry never becomes an auth or proxy layer.
  Any change that routes traffic, mints tokens, or runs artifacts will be rejected —
  put it in a gateway adapter under `adapters/` instead.
- **Secrets:** never commit credentials; never persist secret *values* in the DB.

## Tests

`make test` must pass. New API behavior needs a table-driven test. The label-selector
engine and the apiVersion-normalization path have the strictest coverage bar.
