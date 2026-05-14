# Wolf 2.0 — Containerized Scanners

**Breaking release.** Wolf no longer requires ~40 tools installed on the
host. Scanners now run in short-lived Docker containers from the
`wolf-scanners` image family.

## Highlights

- **One install step.** `docker compose up -d` brings up wolf and the
  default scanner image (`wolf-scanners`). No `pip install bandit`, no
  `go install gosec`, no `npm i -g eslint`.
- **Reproducible findings.** Every tool in `wolf-scanners:2.0` is pinned at
  a specific upstream version. Two hosts on the same wolf version produce
  byte-identical findings (modulo network-dependent vuln DBs).
- **Host isolation.** Scanned code never executes against the host user or
  host filesystem. Each scanner runs `--user $(id -u)` with `/scan` mounted
  read-only and the root filesystem read-only.
- **Smaller per-language footprint.** The 4-image split (`wolf-scanners`,
  `-jvm`, `-rust`, `-codeql`) means a Python/JS team pulls ~2.5 GB instead
  of the previous 6–8 GB monolith. JVM/Rust/CodeQL pulled lazily.
- **OpenSSF Scorecard** added as a new bundled scanner (repo-hygiene
  scoring: branch protection, signed releases, pinned dependencies, etc.).
- **`wolf doctor`** and **`wolf pull scanners`** CLI commands for
  diagnostics and image management.

## Tools shipped

The default `wolf-scanners` image contains 35+ tools:

- **SAST**: semgrep
- **SCA**: trivy, grype, osv-scanner
- **Secrets**: gitleaks, trufflehog, detect-secrets
- **Containers/IaC**: hadolint, dockle, checkov, tflint, kubescape, kube-linter
- **DAST**: nuclei
- **Docs/specs**: vale, spectral
- **License/SBOM**: scancode, syft
- **Repo hygiene**: scorecard (new)
- **Per-language**: bandit, ruff, mypy, pip-audit, radon, vulture (Python);
  gosec, staticcheck, govulncheck (Go); eslint, npm-audit (JS/TS);
  brakeman, rubocop (Ruby); phpstan (PHP); swiftlint (Swift);
  cppcheck (C/C++); shellcheck (shell); sqlfluff (SQL)

Optional images:

- `wolf-scanners-jvm` — infer, pmd (for Java/Kotlin/C++ scanning that needs a JDK)
- `wolf-scanners-rust` — clippy + the rust toolchain
- `wolf-scanners-codeql` — CodeQL (license-restricted to open-source unless you
  have GitHub Enterprise Advanced Security)

## Breaking changes

See [MIGRATION_2_0.md](./MIGRATION_2_0.md) for the full upgrade guide.

- Docker is a hard runtime requirement.
- Finding fingerprints invalidate (paths are now repo-relative). One-time
  migration recommended.
- Repos to scan must be under `WOLF_REPOS_ROOT` (default `./repos/`).
- `wolf scan --repo /absolute/path` now expects a container path (e.g.
  `/repos/myproject`) when run inside the compose stack.

## Non-changes

- Same orchestration model: language detection → tool selection → parallel
  execution with concurrency cap → dedup → fingerprint.
- Same finding schema, same SARIF export, same UI.
- Same fix engine (claude/codex/git/gh/glab still shell out from wolf-slim
  to the host — to be containerized in v2.1).
- Same plugin contract (`models.Plugin`); plugin authors just call
  `container.CommandContext` instead of `plugin.CommandContext`.

## Adding new tools

The bundled tools are now defined in `scanners/versions.env` (one line per
tool with a pinned version). Add a new tool by:

1. Pinning the version in `versions.env`.
2. Adding an install line to the appropriate `scanners/install/*.sh`.
3. Adding the tool to `scanners/smoke-test.sh`.
4. Writing the wolf plugin at `plugins/<bucket>/<tool>.go` using
   `container.CommandContext`. See `plugins/python/bandit.go` for the canonical
   pattern.
5. (If the tool needs a non-default image) adding it to
   `internal/plugin/container/buckets.go`'s `DefaultBucketImages` map.

## Acknowledgements

- The 4-image split (instead of the originally-planned single fat image)
  was suggested mid-design after sizing analysis showed the monolithic
  image was 6-8 GB. Splitting cut the typical user's pull to ~2.5 GB
  while keeping the 4-image build matrix small enough to maintain.
- OpenSSF Scorecard was added at the suggestion of expanding the security
  hygiene coverage (alongside the existing OSV-Scanner integration).
