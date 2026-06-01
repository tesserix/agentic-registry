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

```sh
agentic login --registry https://agentic-registry.example.com --token "$TOKEN"
# or per-invocation:
export AGENTIC_REGISTRY=https://agentic-registry.example.com
export AGENTIC_TOKEN=...            # optional bearer token
export AGENTIC_INSECURE=1           # local self-signed gateways only
```

Config is written to `~/.agentic/config.json` (mode 0600).

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
