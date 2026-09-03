# ADR 0001: Edition boundary

Status: accepted

This restates Wolf PRD §1A.5. It is the commercial architecture.

## Decision

Capabilities that already exist in the public tree stay public. They are not
relocated into the private overlay because they look enterprise-grade.

### Stay in Community (`alphabravo-oss/thewolf`)

- 53-scanner orchestration, plugin registry, `scanners/tools.yaml`, container execution
- First-party scanner supply-chain factory (discovery through offline bundles)
- All current fix harnesses (Claude, Codex, OpenCode, API, ToolDefinition) plus verification
- Findings, fingerprints, corroboration, baselines, suppressions, gates, SARIF
- Local auth, TOTP, API tokens, admin/user roles
- REST/OpenAPI, CLI, SSE, SQLite, PostgreSQL, Compose, baseline Helm
- MCP core, Fix Engine Protocol, and public evidence contracts as they are added

### Go to Enterprise (`AlphaBravoCompany/wolf-enterprise`)

- OIDC/SAML/LDAP/SCIM, orgs/workspaces/ABAC
- Attack paths, reachability, private catalogs (not the first-party factory)
- Org-wide autofix policy, certified fixer images, SIEM/ticketing, extra SCMs
- Enterprise UI routes composed at private build time

Licensing is capability-based (`enterprise.identity`, …), never a single
`isEnterprise` boolean. The Community binary never imports Enterprise packages.
The overlay pins a Community commit and registers modules through `pkg/edition`.
`pkg/edition.ContractVersion` is the public contract semver. Breaking changes to
those packages are a major bump and an overlay pin bump in the same change.

## Consequences

- Do not copy `internal/` or `plugins/` into the overlay.
- Do not invent BSL license text here; counsel owns `LICENSE`.
- Factory and fix harnesses remain Community.
