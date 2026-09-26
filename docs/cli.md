# `agentic` — the Agentic Registry CLI

A single, dependency-free Go binary for working with any Agentic Registry over
the public `/v0` API. It holds no secrets beyond the token you explicitly save.

## Install

```sh
# Homebrew (after the first tagged release)
brew install tesserix/tap/agentic

# From source
make cli            # -> bin/agentic
go install github.com/tesserix/agentic-registry/cmd/agentic@latest
```

## Configure

Interactive users should use OAuth device or browser login. Refresh and access
tokens are kept in the operating-system credential store; the JSON config never
contains token material.

```sh
agentic auth login --registry https://aregistry.tesserix.app
agentic auth status
agentic auth logout
```

For unattended CI, create a tenant-scoped credential on **Settings → API
credentials**, copy the secret when it is shown, and set all four variables:

```sh
export AGENTIC_CLIENT_ID="<client-id>"
export AGENTIC_CLIENT_SECRET="<client-secret>"
export AGENTIC_TOKEN_URL="https://auth.tesserix.app/oauth/v2/token"
export AGENTIC_AUDIENCE="386930054896026901"
agentic apply -f agent.yaml
```

Only `AGENTIC_CLIENT_SECRET` is secret. In GitHub Actions, put it in an Actions
secret and keep the client ID, token URL, and audience in repository or
environment variables:

```yaml
env:
  AGENTIC_CLIENT_ID: ${{ vars.AGENTIC_CLIENT_ID }}
  AGENTIC_CLIENT_SECRET: ${{ secrets.AGENTIC_CLIENT_SECRET }}
  AGENTIC_TOKEN_URL: ${{ vars.AGENTIC_TOKEN_URL }}
  AGENTIC_AUDIENCE: ${{ vars.AGENTIC_AUDIENCE }}
steps:
  - run: agentic apply -f agent.yaml
```

The CLI obtains a short-lived token with the OAuth client-credentials grant,
caches it only in process, and never persists the client secret. Rotate a
credential by updating the CI secret during the identity provider's overlap
window; revoke it after all publishers use the replacement. Restrict each
credential to the minimum scopes, namespaces, artifact kinds, and lifetime.

Self-hosted Registry operators can discover the issuer and audience from
`GET /v0/auth/config`; obtain `token_endpoint` from the issuer's
`/.well-known/openid-configuration` document rather than assuming the Tesserix
values above.

The legacy explicit-token override remains available for ephemeral use:

```sh
export AGENTIC_REGISTRY=https://agentic-registry.example.com
export AGENTIC_TOKEN=...            # optional bearer token; never pass it as an argument
export AGENTIC_INSECURE=1           # local self-signed gateways only
```

Non-secret config is written to `~/.agentic/config.json` (mode 0600).

## Commands

| Command | Description |
|---|---|
| `agentic status` | Endpoint, health (version/store), and signing key |
| `agentic init <Kind> <name>` | Scaffold a manifest to stdout |
| `agentic apply -f <file.yaml>` | Publish a multi-doc YAML bundle |
| `agentic push <file.yaml>` | Publish a single resource |
| `agentic list <plural> [--selector S] [-o json]` | List a collection (table) |
| `agentic search <query> [-o json]` | Cross-kind ranked (pgvector) search |
| `agentic pull <plural> <name> [--tag T]` | Fetch one artifact (omit `--tag` for latest) |
| `agentic versions <plural> <name>` | All published versions; newest marked `(latest)` |
| `agentic history <plural> <name>` | Append-only revision timeline with digests |
| `agentic verify <plural> <name> [--tag T]` | Verify the registry's Ed25519 signature over the digest |
| `agentic render <name> [-f vars.json]` | Render a Prompt with variables |
| `agentic delete <plural> <name> <tag>` | Delete one version |
| `agentic version` | CLI version |

`plural` is one of: `skills tools mcpservers prompts workflows blueprints agents`.

## Versioning

Publishing without a version auto-increments semver (`v0.0.1`, `v0.0.2`, …);
pass an explicit version in the manifest (`metadata.tag: v1.2.0`) to pin. The
newest version is always reachable as `@latest` (and shown as `latest`).

## Examples

```sh
agentic init Skill code-reviewer > skill.yaml
agentic apply -f skill.yaml
agentic search "kubernetes troubleshooting"
agentic versions skills code-reviewer
agentic verify skills code-reviewer            # ✓ verified, signed by registry key …
agentic render summarize-incident -f vars.json
```

## Release (maintainers)

Tag `v*` → the `cli release` workflow runs GoReleaser, publishing multi-arch
binaries to a GitHub Release and a Homebrew formula to `tesserix/homebrew-tap`
(needs the `HOMEBREW_TAP_GITHUB_TOKEN` secret). Local dry run: `make release-snapshot`.
