# Security Policy

## Reporting a Vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately via GitHub's **Report a vulnerability** button
(Security → Advisories), or email **security@agentic.dev**.

We aim to acknowledge reports within **3 business days** and to ship a fix or
mitigation for confirmed high-severity issues within **14 days**.

## Supported Versions

| Version | Supported |
|---------|-----------|
| `main`  | ✅ |
| latest `v0.x` release | ✅ |
| older | ❌ |

## Scope & Design Guarantees

Agentic Registry is a **catalog / control plane**. By design it:

- does **not** mint tokens, authenticate end-user traffic, or proxy requests;
- stores **no secrets** in its database — provider credentials are stored only as
  references to an external secret manager;
- validates identity from a trusted gateway (`X-Forwarded-*`) or a JWKS-verified JWT,
  and applies a visibility/RBAC pre-filter **before** any label-selector query.

Reports about secret leakage, RBAC bypass, namespace-ownership spoofing, or
selector-based information disclosure are highest priority.
