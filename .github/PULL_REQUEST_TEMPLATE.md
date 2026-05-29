## What & why

<!-- What does this change and why. Link issues with "Closes #123". -->

## Checklist

- [ ] Commits are signed off (`git commit -s`, DCO)
- [ ] `make test` and `make lint` pass
- [ ] Tests added/updated for new behavior
- [ ] No secrets committed; no secret values persisted to the DB
- [ ] Does **not** turn the registry into an auth/proxy layer (gateway logic belongs in `adapters/`)
- [ ] Docs / `IMPLEMENTATION_PLAN.md` updated if the contract changed
