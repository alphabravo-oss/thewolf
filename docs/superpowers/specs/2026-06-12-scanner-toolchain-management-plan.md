# Design & Plan: Scanner Toolchain Management, Version Detection, and Image Hygiene

- **Date:** 2026-06-12
- **Branch:** `main`
- **Status:** Implemented baseline; release hardening follow-ups remain
- **Author:** Codex planning session

---

## 1. Goal

Make Wolf's scanner toolchain easy to understand, update, validate, and operate.
The current architecture is generally sound: scanner integrations are modular,
container execution is centralized, and many heavy tools already use upstream
maintainer images. The gaps are around maintainability:

1. Version metadata is split across `scanners/versions.env`, Dockerfiles, scripts,
   docs, and `internal/plugin/container/buckets.go`.
2. Runtime defaults can float on `:latest`, while scanner contents are mostly
   pinned.
3. There is no automated check that every registered plugin has a deliberate image
   strategy.
4. Documentation counts have drifted from the actual plugin registry.
5. Operators cannot see which scanner tools are stale without manually checking
   registries and release feeds.

The desired end state is a scanner platform where Wolf can answer:

- Which tools are installed or routed?
- Which image does each tool use?
- Which version is pinned?
- Is a newer version available?
- Is the local image present and fresh?
- Are docs and tests consistent with the registry?
- What has to change to safely bump a tool?

---

## 2. Current State

### 2.1 Image Split

Wolf currently has four Wolf-built scanner images:

| Image | Purpose |
|---|---|
| `wolf-scanners` | Default image for bundled tools. |
| `wolf-scanners-jvm` | JVM bucket image for tools such as `infer` and `pmd`. |
| `wolf-scanners-rust` | Rust bucket image for `clippy`. |
| `wolf-scanners-codeql` | CodeQL bucket image for `codeql`. |

There are also upstream/native tool images for tools where the maintainer publishes
a usable container image, such as `trivy`, `semgrep`, `gitleaks`, `checkov`,
`renovate`, and others.

Current implemented split:

| Tier | Count | Description |
|---|---:|---|
| Default Wolf image | 22 | Tools installed into `wolf-scanners`. |
| Wolf bucket images | 5 | Tools routed to JVM, Rust, or CodeQL bucket images. |
| Upstream/native images | 22 | Tools run directly from maintainer images. |
| Total registered plugins | 49 | Scanner plugins registered under `plugins/*`. |

### 2.2 Good Existing Design

- Each scanner integration is implemented as a separate plugin module under
  `plugins/*`.
- Scanner execution goes through the centralized container runner rather than each
  plugin shelling out independently.
- The runner applies useful isolation controls: non-root user, read-only repo mount,
  read-only container filesystem, tmpfs, resource limits, and controlled networking.
- Upstream images reduce the size and maintenance burden of Wolf-built scanner images.
- `scanners/versions.env` is intended to be the scanner version source of truth.

### 2.3 Problems To Fix

| Problem | Impact |
|---|---|
| Runtime defaults use `:latest` scanner image tags | Operators may get different scanner contents across deployments without an explicit upgrade decision. |
| `versions.env` and upstream image tags can drift | The stated source of truth is not fully enforced. |
| Some version values remain outside `versions.env` | Toolchain updates are harder to audit and automate. |
| Plugin/image tier parity is not tested | A new plugin can be added without an explicit image strategy. |
| Docs count scanners manually | README and docs drift from code. |
| No newer-version detection | Operators and maintainers cannot quickly see stale scanner tools. |
| Startup only ensures the default image | Upstream or bucket image issues may surface at scan execution time instead of setup time. |

---

## 3. Design Principles

1. **Pinned by default, freshness visible.** Wolf should not silently use the latest
   tool version for scans. It should pin versions and report when newer versions are
   available.
2. **One metadata source.** Tool name, version, image tier, image reference, update
   source, docs label, license notes, and smoke-test command should live in one
   structured manifest.
3. **Generated consumers.** Go maps, docs tables, version env files, smoke tests, and
   scanner status APIs should be generated from or validated against the manifest.
4. **Operator control.** Operators can override images and disable upstream images,
   but Wolf should show clearly when an override differs from the canonical manifest.
5. **No unbounded registry calls during scans.** Version checks should be explicit,
   cached, rate-limited, and never block normal scan execution.
6. **Fail early for setup, degrade gracefully for scans.** Setup and admin health
   checks should validate all configured images. Individual scans should produce a
   clear per-tool error if an image is unavailable.

---

## 4. Proposed Architecture

### 4.1 New Scanner Tool Manifest

Add a structured manifest:

```text
scanners/tools.yaml
```

Example entry:

```yaml
tools:
  semgrep:
    display_name: Semgrep
    category: sast
    plugin_package: plugins/sast
    integration_tier: upstream
    pinned_version: "1.92.0"
    version_variable: SEMGREP_VERSION
    image:
      registry: docker.io
      repository: semgrep/semgrep
      tag_template: "{{ version }}"
      pinned_reference: semgrep/semgrep:1.92.0
      entrypoint: semgrep
    update_source:
      type: docker_registry
      repository: semgrep/semgrep
      tag_pattern: '^\d+\.\d+\.\d+$'
    smoke:
      command: ["semgrep", "--version"]
      expected_pattern: "1.92.0"
    docs:
      description: Generic SAST scanner with broad language coverage.
      homepage: https://semgrep.dev
      license: LGPL-2.1
```

Example bundled entry:

```yaml
tools:
  bandit:
    display_name: Bandit
    category: sast
    plugin_package: plugins/python
    integration_tier: default
    pinned_version: "1.7.10"
    version_variable: BANDIT_VERSION
    install:
      manager: pip
      package: bandit
    update_source:
      type: pypi
      package: bandit
    smoke:
      command: ["bandit", "--version"]
      expected_pattern: "1.7.10"
```

Example bucket entry:

```yaml
tools:
  pmd:
    display_name: PMD
    category: sast
    plugin_package: plugins/additional
    integration_tier: bucket
    bucket: jvm
    pinned_version: "7.7.0"
    version_variable: PMD_VERSION
    update_source:
      type: github_releases
      owner: pmd
      repo: pmd
      tag_pattern: '^pmd_releases/\d+\.\d+\.\d+$'
```

### 4.2 Manifest Ownership Rules

The manifest becomes authoritative for:

- Scanner tool names.
- Scanner display names.
- Integration tier: `default`, `bucket`, or `upstream`.
- Pinned version.
- Image repository, tag template, and entrypoint for upstream images.
- Bucket image assignment.
- Update source and version parsing rules.
- Smoke-test command metadata.
- Documentation counts and generated tables.

The plugin registry remains authoritative for executable plugin behavior.

The parity contract:

- Every registered plugin must have exactly one manifest entry.
- Every manifest entry must map to an existing registered plugin.
- Every manifest entry must declare exactly one integration tier.
- Every upstream entry must declare image metadata.
- Every default or bucket entry must declare an install or bucket strategy.

### 4.3 Generated Or Validated Outputs

Prefer validation first, generation second. This limits churn while establishing the
contract.

Initial validated files:

- `internal/plugin/container/buckets.go`
- `scanners/versions.env`
- `scanners/Dockerfile*`
- `scanners/install/*.sh`
- `README.md` scanner tables

Later generated files:

- `internal/plugin/container/generated_tools.go`
- `scanners/versions.env`
- README scanner count table
- scanner smoke-test matrix

Implemented baseline:

- `scanners/tools.yaml` is validated against the plugin registry, version pins,
  routing maps, generated docs, and smoke-test coverage.
- `scanners/toolchains.yaml` declares scanner image base/runtime toolchains and is
  validated against Dockerfiles and install scripts.
- `scanners/TOOLS.md` is generated from `scanners/tools.yaml` and checked in CI.

### 4.4 New Internal Packages

| Package | Purpose |
|---|---|
| `internal/scannertools/manifest` | Load, validate, and query `scanners/tools.yaml`. |
| `internal/scannertools/versions` | Compare semantic/calendar versions and normalize tag prefixes. |
| `internal/scannertools/latest` | Check remote registries/package indexes for newer versions. |
| `internal/scannertools/status` | Combine manifest, local Docker state, configured overrides, and latest-version data. |
| `internal/api/routes/scannertools` | Serve scanner tool status, version checks, and image health. |
| `internal/cli/scannertools` | CLI commands for maintainers/operators. |

---

## 5. Version Freshness Detection

### 5.1 Product Behavior

Wolf should show scanner freshness without automatically upgrading scanners.

UI examples:

| Tool | Pinned | Latest | Image | Status |
|---|---|---|---|---|
| Semgrep | `1.92.0` | `1.94.1` | `semgrep/semgrep:1.92.0` | Update available |
| Bandit | `1.7.10` | `1.7.10` | `wolf-scanners:2.0.0` | Current |
| Checkov | `3.2.297` | `3.2.527` | `bridgecrew/checkov:3.2.527` | Manifest drift |
| Pluto | `5.20.4` | unknown | `us-docker.pkg.dev/.../pluto:v5.9.0` | Check failed |

Freshness status values:

| Status | Meaning |
|---|---|
| `current` | Latest known version equals pinned version. |
| `update_available` | Latest known version is newer than pinned version. |
| `unknown` | Wolf cannot determine latest version. |
| `check_failed` | Registry/package lookup failed. |
| `manifest_drift` | Manifest, version env, and runtime image map disagree. |
| `overridden` | Operator configured a non-canonical image. |

### 5.2 Supported Update Sources

Add update checkers in this order:

| Source | Examples | Implementation |
|---|---|---|
| Docker Registry tags | `semgrep`, `trivy`, `renovate` | Registry HTTP API with pagination and tag filtering. |
| GitHub Releases | `pmd`, `codeql`, release tarball tools | GitHub releases/tags API, unauthenticated with optional token. |
| PyPI JSON | `bandit`, `ruff`, `mypy`, `pip-audit` | `https://pypi.org/pypi/{package}/json`. |
| npm registry | `eslint`, `markdownlint`, `spectral` | `https://registry.npmjs.org/{package}`. |
| RubyGems API | `brakeman`, `rubocop` | RubyGems versions API. |
| Go module proxy | `gosec`, `staticcheck`, `govulncheck` | `https://proxy.golang.org/{module}/@v/list`. |
| Composer Packagist | `phpstan` | Packagist package metadata API. |
| Rust channels/crates | `clippy`, Rust toolchain | Rust release channel metadata or configured manual mode. |
| Manual | hard cases | Manifest marks as manually checked with reason. |

### 5.3 Caching And Rate Limits

Add a database table:

```sql
CREATE TABLE scanner_version_checks (
  tool_name TEXT PRIMARY KEY,
  pinned_version TEXT NOT NULL,
  latest_version TEXT,
  latest_reference TEXT,
  status TEXT NOT NULL,
  checked_at DATETIME NOT NULL,
  error TEXT,
  source_type TEXT NOT NULL,
  source_url TEXT
);
```

Rules:

- Default cache TTL: 24 hours.
- Manual refresh endpoint requires admin permission.
- Background refresh is optional and disabled by default initially.
- A scan never performs network version checks.
- Failed checks cache for a shorter TTL, e.g. 1 hour, to avoid repeated registry
  failures.
- Admin UI displays last checked time and last error.

### 5.4 APIs

Add endpoints:

```text
GET  /api/v1/scanners/tools
GET  /api/v1/scanners/tools/{name}
POST /api/v1/scanners/tools/check-updates
POST /api/v1/scanners/tools/{name}/check-update
GET  /api/v1/scanners/images
POST /api/v1/scanners/images/pull
```

Response example:

```json
{
  "data": {
    "name": "semgrep",
    "display_name": "Semgrep",
    "integration_tier": "upstream",
    "pinned_version": "1.92.0",
    "latest_version": "1.94.1",
    "freshness_status": "update_available",
    "configured_image": "semgrep/semgrep:1.92.0",
    "canonical_image": "semgrep/semgrep:1.92.0",
    "image_present": true,
    "image_digest": "sha256:...",
    "last_checked_at": "2026-06-12T14:00:00Z"
  }
}
```

### 5.5 CLI

Add commands:

```bash
wolf scanners tools
wolf scanners tools --output json
wolf scanners tools semgrep
wolf scanners check-updates
wolf scanners check-updates --tool semgrep
wolf scanners validate-manifest
wolf scanners images
wolf scanners pull --all
```

Example output:

```text
TOOL       TIER      PINNED    LATEST    STATUS
semgrep    upstream  1.92.0    1.94.1    update_available
bandit     default   1.7.10    1.7.10    current
checkov    upstream  3.2.297   3.2.527   manifest_drift
```

---

## 6. Image Tag And Pinning Policy

### 6.1 Build-Time Policy

Scanner builds should remain pinned. `docker build` should not install arbitrary
latest scanner versions.

Rules:

- Tool versions are pinned in the manifest.
- Dockerfiles and install scripts consume pinned values only.
- Smoke tests assert the installed version for each bundled or bucket tool.
- Base images should move from tag-only references to digest-pinned references where
  practical.
- The Go, Node, Python, Ruby, PHP, and Rust toolchain versions should be declared in
  the manifest or a sibling `scanners/toolchains.yaml`.

### 6.2 Runtime Policy

Runtime defaults should avoid `:latest`.

Preferred default:

```yaml
WOLF_SCANNERS_IMAGE=alphabravodevops/wolf-scanners:${WOLF_VERSION}
WOLF_SCANNERS_BUCKET_VERSION=${WOLF_VERSION}
```

For development:

```yaml
WOLF_SCANNERS_IMAGE=wolf-scanners:dev
WOLF_SCANNERS_BUCKET_BASE=wolf-scanners
WOLF_SCANNERS_BUCKET_VERSION=dev
```

For production:

```yaml
WOLF_SCANNERS_IMAGE=alphabravodevops/wolf-scanners:2.0.0
WOLF_SCANNERS_BUCKET_BASE=alphabravodevops/wolf-scanners
WOLF_SCANNERS_BUCKET_VERSION=2.0.0
```

Longer term, release manifests should publish image digests:

```yaml
wolf_scanners:
  image: alphabravodevops/wolf-scanners:2.0.0
  digest: sha256:...
```

### 6.3 Operator Overrides

Operator overrides remain supported:

```yaml
scan:
  container:
    image_overrides:
      semgrep: registry.internal/security/semgrep:1.92.0
```

Wolf should show:

- Canonical image.
- Configured image.
- Whether the configured image is present locally.
- Whether the configured image digest changed since the last pull.
- Whether the override disables version detection.

---

## 7. Implementation Plan

### Phase 1: Establish The Manifest Contract

Tasks:

1. Add `scanners/tools.yaml` with entries for all currently registered scanner
   plugins.
2. Add `internal/scannertools/manifest` to load and validate the manifest.
3. Add a unit test that enumerates `plugin.All()` and verifies one manifest entry per
   registered plugin.
4. Add a unit test that verifies every manifest entry maps to one known integration
   tier: `default`, `bucket`, or `upstream`.
5. Add a unit test that compares manifest upstream image references to
   `DefaultUpstreamTools()`.
6. Add a unit test that compares manifest version variables to `scanners/versions.env`.
7. Fix current drift, including `checkov`, `pluto`, and any stale `scancode` metadata.

Reasoning:

This phase creates a safety rail before refactoring. It should fail loudly when
scanner metadata drifts.

Definition of done:

- `go test ./...` fails if a plugin is added without a manifest entry.
- `go test ./...` fails if `versions.env` and upstream image tags disagree.
- Current scanner count is computed from code and manifest, not hand-counted.
- Existing scanner behavior is unchanged.

### Phase 2: Remove Floating Runtime Defaults

Tasks:

1. Replace production-facing `:latest` defaults in compose/setup examples with an
   explicit release tag or `${WOLF_VERSION}`.
2. Keep `:dev` tags for local development flows.
3. Document supported image tag modes: `dev`, release tag, digest-pinned production.
4. Add startup warning when configured image uses `:latest`.
5. Add scanner image metadata to `/api/v1/scanners/images`.

Reasoning:

Pinned tool versions are undermined if the scanner image itself floats. Operators
should make an explicit upgrade decision.

Definition of done:

- Default production configuration no longer points at `:latest`.
- Admin health/status clearly flags `:latest` as non-reproducible.
- Development workflows still build and run with `:dev`.

### Phase 3: Add Full Image And Tool Status

Tasks:

1. Add a scanner tool status service that merges:
   - manifest entry
   - runtime container config
   - local Docker image presence
   - remote image digest, where available
   - version check cache, when available
2. Extend scanner API responses to return per-tool image status.
3. Add CLI commands for scanner tool status.
4. Add UI page under Settings -> Scanners or Admin -> Scanners.
5. Add clear scan-time errors for missing upstream/bucket images.

Reasoning:

This gives operators one place to understand whether Wolf is ready to scan with each
tool.

Definition of done:

- Admin can see all 49 scanner tools and their tier.
- Admin can identify missing images before running a scan.
- A missing upstream image error names the tool, image, and suggested pull command.
- CLI can output the same data as JSON for automation.

### Phase 4: Add Newer-Version Detection

Tasks:

1. Implement `internal/scannertools/latest` with pluggable source checkers.
2. Support Docker registry tags, GitHub releases/tags, PyPI, npm, RubyGems, Go module
   proxy, Packagist, and manual mode.
3. Add `scanner_version_checks` table.
4. Add manual refresh API endpoints.
5. Add CLI `wolf scanners check-updates`.
6. Add UI indicators for `current`, `update_available`, `unknown`, `check_failed`,
   `manifest_drift`, and `overridden`.
7. Ensure version checks are cached, rate-limited, timeout-bounded, and never run
   during normal scans.

Reasoning:

Wolf should help maintainers keep the scanner fleet current without silently changing
scan behavior.

Definition of done:

- Admin can refresh scanner version checks on demand.
- Freshness data is cached and visible in API, CLI, and UI.
- Scan execution remains fully pinned and does not perform update network calls.
- Failed update checks do not break scans.

### Phase 5: Improve Build And Smoke Tests

Tasks:

1. Generate or validate scanner smoke-test matrix from the manifest.
2. Assert bundled and bucket tools report the pinned version after build.
3. Validate upstream image tags exist before release.
4. Validate image architecture support for `linux/amd64` and `linux/arm64`.
5. Move hardcoded toolchain versions into manifest-managed metadata.
6. Add CI job for scanner metadata validation that does not require building all
   images.
7. Add optional release CI job that builds images and runs full smoke tests.

Reasoning:

Metadata validation should be fast and always run. Full scanner image builds are
heavier and can run on release or scheduled workflows.

Definition of done:

- Fast CI catches metadata drift.
- Release CI catches missing image tags and broken installs.
- Build logs show exact versions installed.
- Smoke-test failures identify the specific tool and expected version.

Implemented baseline:

- `make scanners-validate` fails if a default or bucket tool lacks manifest smoke
  metadata or if a pinned smoke expectation no longer appears in `smoke-test.sh`.
- `make scanners-validate` fails if `scanners/toolchains.yaml` drifts from scanner
  Dockerfiles or install scripts.
- `make scanners-upstream-check` verifies upstream image tags and expected
  platforms without building the Wolf scanner images.

### Phase 6: Generate Docs And Reduce Manual Drift

Tasks:

1. Generate scanner count tables from the manifest.
2. Update README scanner sections to avoid hand-maintained counts where possible.
3. Add `wolf scanners tools --markdown` or a small docs generation command.
4. Add a docs drift test that fails when generated scanner docs are stale.

Reasoning:

Docs should describe the scanner registry, not duplicate it manually.

Definition of done:

- README scanner counts match the code.
- A stale generated scanner table fails CI.
- Adding a scanner has a predictable docs update path.

### Phase 7: Optional Assisted Upgrade Workflow

Tasks:

1. Add `wolf scanner plan-upgrades` to list available updates with risk metadata.
2. Add `make scanners-bump TOOL=semgrep VERSION=1.94.1` for maintainers.
3. Update manifest, `versions.env`, generated files, and smoke-test expectations in
   one command.
4. Generate a changelog fragment.
5. Optionally open a PR from a scheduled workflow.

Reasoning:

Detection is useful; a guided bump flow makes it operationally cheap.

Definition of done:

- A maintainer can bump one manifest-managed tool with one command.
- The command updates manifest metadata, `versions.env`, generated docs, and upstream
  image references that follow the standard tag templates.
- The command writes a scanner-bump changelog fragment.
- `wolf scanner plan-upgrades` refreshes version metadata and renders the current
  tool upgrade status.
- Tests catch any plugin fixture changes needed after the bump.

---

## 8. Testing Strategy

### 8.1 Unit Tests

- Manifest schema validation.
- Plugin registry parity.
- Version variable parsing from `versions.env`.
- Image tag parsing and normalization.
- Version comparison across semver, `v`-prefixed semver, and calendar-like tags.
- Update checker behavior with mocked HTTP responses.
- Cache TTL behavior.

### 8.2 Integration Tests

- `/api/v1/scanners/tools` returns all registered tools.
- `/api/v1/scanners/tools/check-updates` stores cached freshness results.
- `/api/v1/scanners/images` includes canonical and configured image references.
- Missing upstream image produces a clear per-tool error.
- Operator image override is reflected in status output.

### 8.3 CLI Tests

- `wolf scanners tools --output json` emits stable JSON.
- `wolf scanners validate-manifest` fails on missing manifest entry.
- `wolf scanners check-updates --tool semgrep` updates one cache entry.
- Table output remains readable with long image names.

### 8.4 Build And Release Tests

- `make scanners-build` consumes pinned manifest versions.
- `make scanners-smoke` verifies installed versions.
- Release job verifies upstream image tags exist.
- Release job verifies expected platforms are available.
- CI fails on `:latest` defaults in production examples.

### 8.5 Security Tests

- Update checkers enforce timeouts.
- Update checker URLs are constructed from manifest fields, not user-submitted raw
  URLs.
- Registry errors are sanitized before returning to UI/API.
- Version check endpoints require admin or write-config permission.
- No registry credentials are logged.

---

## 9. Validation Checklist

Before merging:

- `go test ./...`
- `npm run typecheck` in `ui-next`, if UI changes are included.
- `npm run build` in `ui-next`, if UI changes are included.
- `make scanners-validate`
- `make scanners-smoke`, for release-affecting changes.
- Manual check: Settings -> Scanners shows all tools, counts, tiers, pinned versions,
  configured images, and freshness status.
- Manual check: scanner image pull action handles default, bucket, and upstream images.
- Manual check: a forced missing upstream image produces a useful scan error.

---

## 10. Risks And Mitigations

| Risk | Mitigation |
|---|---|
| Registry APIs rate-limit update checks | Cache results, require manual refresh initially, support optional tokens. |
| Version comparison is inconsistent across ecosystems | Store source-specific normalization rules in the manifest. |
| Some tools do not publish machine-readable release data | Mark as `manual` and show that status honestly. |
| Operator overrides make freshness ambiguous | Show canonical and configured image separately. |
| Digest pinning makes local development awkward | Use digest pinning for release manifests, not local `:dev` workflows. |
| Manifest becomes too large | Keep executable behavior in plugins; manifest only owns metadata. |
| Generated files create noisy diffs | Start with validation tests, generate only after the contract stabilizes. |

---

## 11. Suggested Milestone Order

1. **Metadata safety rail:** add manifest and parity tests.
2. **Drift cleanup:** fix current `checkov`, `pluto`, stale docs, and stale scanner
   metadata.
3. **Reproducible defaults:** remove production `:latest` defaults and add warnings.
4. **Status visibility:** add scanner tool/image status API, CLI, and UI.
5. **Freshness detection:** add cached update checkers.
6. **Release hardening:** add smoke-test and upstream-image validation jobs.
7. **Assisted upgrades:** add optional bump workflow.

This order keeps risk low: first make drift visible, then clean it up, then add
operator-facing features.

---

## 12. Definition Of Done For The Whole Project

The project is complete when:

- Wolf has one scanner manifest covering every registered scanner plugin.
- `go test ./...` catches plugin/image/version metadata drift.
- Production defaults are reproducible and do not rely on `:latest`.
- Admin UI and CLI show every scanner's tier, pinned version, configured image,
  image presence, and freshness status.
- Wolf can detect and cache newer available tool versions without changing scan
  behavior.
- Scans never perform update checks.
- Missing or stale images are reported before or during scan execution with clear
  remediation.
- Scanner docs are generated or validated so counts cannot drift silently.
- Scanner build and release workflows verify pinned versions and upstream image
  availability.
