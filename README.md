<p align="center">
  <img src="docs/wolf-logo.svg" alt="The Wolf" width="120" />
</p>

<h1 align="center">The Wolf</h1>
<p align="center"><strong>Multi Tool Code Analysis Engine</strong></p>
<p align="center">by <a href="https://alphabravo.io">AlphaBravo</a></p>

---

The Wolf is an orchestration engine that runs **35+ static analysis, security, and code-quality tools in parallel** across your repositories. Tools run in isolated Docker containers — wolf doesn't install anything on the host beyond Docker itself. It aggregates findings, deduplicates results, scores severity, and optionally uses AI to enrich and prioritize issues.

## Features

- **Containerized scanners** — every tool runs in its own short-lived Docker container; no `pip install bandit`, no `go install gosec`, no `npm i -g eslint`
- **Three-tier image strategy** — slim wolf-built image for per-language tools, upstream-official images for well-maintained scanners (trivy, semgrep, gitleaks, …), opt-in heavy-toolchain bucket images for JVM / Rust / CodeQL
- **Multi-tool orchestration** — runs 35+ analysis tools in parallel with configurable concurrency
- **Reproducible findings** — every tool is version-pinned in `scanners/versions.env`; identical scans on two hosts produce identical findings
- **Host isolation** — scanned code runs with `--user $(id -u)`, read-only `/scan` bind mount, read-only root filesystem, no inbound network
- **Collection-based organization** — group repositories into collections for batch scanning and cross-repo metrics
- **Branch-aware metrics** — track findings per branch with trend analysis over time
- **Real-time streaming** — live scan progress via SSE with per-tool status, output logs, and finding counts
- **AI enrichment** (optional) — AI-powered severity scoring, tool summaries, and prioritized recommendations via Anthropic or OpenAI
- **Automated fix engine** — AI-generated fix plans with PR creation via Claude Code
- **Scan-fix-rescan loops** — iterative improvement cycles with regression guardrails
- **Web UI** — Next.js dashboard with collection management, scan monitoring, finding exploration, scanner-backend admin
- **CLI** — full-featured command-line interface for scripting and CI/CD integration
- **Plugin architecture** — adding a new tool is one Dockerfile addition + one Go file
- **SARIF & Markdown reports** — export findings in standard formats

## Quick Start

### Prerequisites

- **Docker** (the only runtime requirement for scanners — wolf no longer needs ~40 tools installed on the host)
- Go 1.23+ *(only if building wolf from source)*
- Node.js 18+ *(only if building the UI from source)*

### Build & Run

```bash
# Start wolf — scanners pulled lazily on first use
docker compose up -d

# Pre-pull all scanner images (optional; otherwise pulled on first scan)
docker compose exec wolf wolf pull scanners

# Health-check everything (docker, image presence, repos mount, uid/gid)
docker compose exec wolf wolf doctor

# Run a scan against a repo placed under ./repos/
docker compose exec wolf wolf scan --repo /repos/myproject

# Open the UI
open http://localhost:8778
```

### CLI Usage

```bash
# Scan a local repository
wolf scan --repo /repos/myproject --branch main

# Run only specific tools
wolf scan --repo /repos/myproject --tools semgrep,trivy,gitleaks

# Diagnose the scanner backend
wolf doctor

# Pre-pull every configured scanner image
wolf pull scanners

# Start the API server (also auto-runs in docker-compose)
wolf serve --bind 0.0.0.0:8778

# Print version info
wolf version
```

## Architecture

```
cmd/wolf/                    CLI entrypoint (cobra; serve / scan / doctor / pull / version)
internal/
  api/                       HTTP server, routes (incl. /api/scanners/*), SSE broker, middleware
  ai/                        AI provider abstraction (Anthropic, OpenAI, noop)
  artifacts/                 Durable scan artifact storage
  auth/                      JWT authentication middleware
  db/                        Database layer (SQLite + PostgreSQL)
  fix/                       Automated fix engine (planner, git, PR)
  loop/                      Scan-fix-rescan loop orchestration
  models/                    Domain models
  plugin/                    Plugin registry
    container/               ★ Docker-backed shim that runs every plugin
  scan/                      Scan pipeline (runner, detector, scorer, mapper, reporter)
  setup/scanners/            Container-backend bootstrap (LoadAndInstall, Doctor, Pull)
plugins/                     Tool plugins organized by language/category
scanners/                    Wolf-built scanner images (Dockerfile{,.jvm,.rust,.codeql})
ui/                          Next.js frontend (incl. /scanners admin page)
configs/                     Default configuration
docs/                        MIGRATION_2_0.md, RELEASE_NOTES_2_0.md
```

## Scanner backend — the three-tier hybrid

Wolf doesn't install scanners on the host. Every tool invocation is a `docker run` from one of three image tiers:

### Tier 1 — Upstream-official images (no wolf rebuild)

For tools where the maintainer publishes a multi-arch image, wolf routes invocations directly to their published image. We pin the version in `scanners/versions.env`. **18 tools currently use this tier** — see "Supported tools" below.

### Tier 2 — Wolf-built default image (`wolf-scanners`)

A slim image (~600–900 MB on amd64) carrying the per-language tools that don't have a maintained upstream image — mostly small pip / npm / go / gem / composer installs.

### Tier 3 — Wolf-built bucket images

Heavy toolchains that don't make sense in the default image and don't have a clean upstream:

| Image | Tools | Approx size | Pulled when |
|---|---|---|---|
| `wolf-scanners-jvm` | infer, pmd (+ JDK) | ~2 GB | when a Java/Kotlin/C/C++ tool runs |
| `wolf-scanners-rust` | clippy (+ rust toolchain) | ~1.2 GB | when a Rust scan runs |
| `wolf-scanners-codeql` | codeql + query packs | ~700 MB | only when codeql is explicitly enabled (license-gated) |

A typical Python+JS shop pulls **~700 MB plus a few small upstream images** (semgrep, trivy, gitleaks, etc.). A polyglot shop adds the relevant bucket images on demand.

The shim's `Config.ImageFor(toolName)` walks: `UpstreamTools` → `ImageOverrides` → default `Image`. Operators override either map via `scan.container.upstream_tools` / `scan.container.image_overrides` in wolf.yaml.

Disable the upstream tier entirely (e.g. air-gapped, internal-mirror-only) with `WOLF_SCANNERS_DISABLE_UPSTREAM=1`.

See `PLAN.md` for the full architecture; `scanners/README.md` for how to add or upgrade tools; `docs/MIGRATION_2_0.md` for the upgrade path from wolf 1.x.

## Supported tools

**Total: 44 scanners** across SAST, SCA, secrets, container, infrastructure, IaC, policy-as-code, license, SBOM, docs, DAST, repo-hygiene, dependency-freshness, privacy/PII, deprecated-API detection, and per-language categories.

### By tier

| Tier | Count | How wolf gets the binary |
|---|---|---|
| **Upstream-official images** | 24 | `docker pull aquasec/trivy`, etc. — maintainer-published, multi-arch |
| **Wolf-built default image** | 17 | bundled in `wolf-scanners:<version>` via pip / npm / go install / apt / composer / github-release |
| **Wolf-built bucket images** | 3 | bundled in `wolf-scanners-{jvm,rust,codeql}:<version>` |

### By language / category

Every tool's tier is annotated as 🌐 (upstream), 📦 (wolf-built default), 🔧 (wolf-built bucket).

#### Cross-language

| Category | Tool | Tier | Image | Description |
|---|---|---|---|---|
| **SAST** | Semgrep | 🌐 | `semgrep/semgrep` | Pattern-based + semantic static analysis across all languages |
| **SAST** | CodeQL | 🔧 | `wolf-scanners-codeql` | GitHub's semantic SAST engine (license-restricted to OSS or GHAS) |
| **SCA** | Trivy | 🌐 | `aquasec/trivy` | Filesystem + container + IaC vulnerability scanner |
| **SCA** | Grype | 🌐 | `anchore/grype` | Vulnerability matcher (paired with Syft for SBOM-based scanning) |
| **SCA** | OSV-Scanner | 🌐 | `ghcr.io/google/osv-scanner` | Multi-ecosystem dep scanner using Google's OSV database |
| **SCA / freshness** | Renovate | 🌐 | `ghcr.io/renovatebot/renovate` | Mend Renovate in dry-run / detect-only mode — flags outdated and vulnerable deps across npm, pip, gem, composer, cargo, go.mod, Helm, GitHub Actions, Dockerfile base images, Terraform, pre-commit, GitLab CI, Bitbucket, Gradle, Maven, sbt. Severity by gap: patch → info, minor → low, major → medium, vuln → high. |
| **IaC SAST** | KICS | 🌐 | `checkmarx/kics` | Multi-format IaC scanner — Terraform, K8s, Dockerfile, CloudFormation, Ansible, Helm, ARM, OpenAPI, Pulumi. ~3k rules. MIT, very active. |
| **Policy-as-code** | Conftest | 🌐 | `openpolicyagent/conftest` | OPA-based config testing — operator writes Rego policies in `policy/`, conftest evaluates them against any YAML/JSON/HCL/Dockerfile. |
| **K8s upgrade** | Pluto | 🌐 | `us-docker.pkg.dev/fairwinds-ops/oss/pluto` | Detects deprecated and removed Kubernetes API versions before they break the cluster upgrade. Removed APIs flagged as `high`. |
| **Privacy / PII** | Bearer | 🌐 | `bearer/bearer` | Data-flow scanner for GDPR/HIPAA/PCI categories — tracks where PII is processed and flags risky patterns. |
| **Docs (Markdown)** | markdownlint-cli | 📦 | `wolf-scanners` | Markdown style + structure linter. |
| **Config (YAML)** | yamllint | 📦 | `wolf-scanners` | YAML structural + style linter. |
| **Secrets** | Gitleaks | 🌐 | `zricethezav/gitleaks` | Regex-based secret detection across history + working tree |
| **Secrets** | TruffleHog | 🌐 | `trufflesecurity/trufflehog` | Deep secret scanner with live-credential verification |
| **Secrets** | detect-secrets | 📦 | `wolf-scanners` | Baseline-style secret scanner by Yelp |
| **Container** | Hadolint | 🌐 | `hadolint/hadolint` | Dockerfile linter |
| **Container** | Dockle | 🌐 | `goodwithtech/dockle` | Container-image best-practice checker (CIS-style) |
| **Container** | Checkov | 🌐 | `bridgecrew/checkov` | IaC security scanner (Terraform / Helm / K8s / CloudFormation / …) |
| **Infrastructure** | TFLint | 🌐 | `ghcr.io/terraform-linters/tflint` | Terraform-specific linter and provider-aware checks |
| **Infrastructure** | Kubescape | 🌐 | `quay.io/kubescape/kubescape-cli` | Kubernetes security/compliance scanner |
| **Infrastructure** | Kube-linter | 🌐 | `stackrox/kube-linter` | Static analysis for K8s manifests and Helm charts |
| **SBOM** | Syft | 🌐 | `anchore/syft` | SBOM generator (paired with Grype) |
| **License** | ScanCode | 🌐 | `ghcr.io/nexb/scancode-toolkit` | License + copyright + dependency origin scanner |
| **Docs** | Spectral | 🌐 | `stoplight/spectral` | OpenAPI / AsyncAPI / JSON-Schema linter |
| **Docs** | Vale | 🌐 | `jdkato/vale` | Prose / documentation style linter |
| **DAST** | Nuclei | 🌐 | `projectdiscovery/nuclei` | Template-based HTTP/DNS/TCP vulnerability scanner |
| **Hygiene** | OpenSSF Scorecard | 🌐 | `gcr.io/openssf/scorecard` | Repository security-posture scoring (branch protection, signed releases, …) |
| **Shell** | ShellCheck | 📦 | `wolf-scanners` | Shell-script linter (`*.sh`, `bash`, `dash`, `ksh`) |
| **SQL** | SQLFluff | 📦 | `wolf-scanners` | SQL linter + formatter |

#### Per-language

| Language | Tool | Tier | Image | Description |
|---|---|---|---|---|
| **Python** | Bandit | 📦 | `wolf-scanners` | Security-focused SAST |
| **Python** | Ruff | 📦 | `wolf-scanners` | Fast linter + formatter (replaces flake8/isort/etc.) |
| **Python** | Mypy | 📦 | `wolf-scanners` | Static type checker |
| **Python** | pip-audit | 📦 | `wolf-scanners` | Dependency vulnerability scanner (PyPA) |
| **Python** | Radon | 📦 | `wolf-scanners` | Cyclomatic complexity analyzer |
| **Python** | Vulture | 📦 | `wolf-scanners` | Dead-code finder |
| **Go** | Gosec | 📦 | `wolf-scanners` | Security linter |
| **Go** | Staticcheck | 📦 | `wolf-scanners` | Quality linter (bug detection, simplifications, deprecations) |
| **Go** | Govulncheck | 📦 | `wolf-scanners` | Official Go vulnerability scanner with reachability analysis |
| **Go** | GoKart | 📦 | `wolf-scanners` | Praetorian's source-to-sink taint-analysis SAST — flags actually-reachable risky calls (complements gosec's pattern matching) |
| **Kotlin** | detekt | 🌐 | `detekt/detekt` | Kotlin static analyzer with 200+ rules across style, complexity, potential bugs |
| **JavaScript / TypeScript** | ESLint | 📦 | `wolf-scanners` | Linter |
| **JavaScript / TypeScript** | npm-audit | 📦 | `wolf-scanners` | Dependency vulnerability scanner (bundled with npm) |
| **Ruby** | Brakeman | 📦 | `wolf-scanners` | Rails-focused security scanner |
| **Ruby** | RuboCop | 📦 | `wolf-scanners` | Ruby linter + formatter |
| **PHP** | PHPStan | 📦 | `wolf-scanners` | Static analyzer with progressive strictness |
| **Swift** | SwiftLint | 📦 | `wolf-scanners` | Style + correctness linter (best-effort on Linux) |
| **C / C++** | Cppcheck | 📦 | `wolf-scanners` | Static analyzer for C/C++ |
| **C / C++ / Java / Kotlin** | Infer | 🔧 | `wolf-scanners-jvm` | Facebook/Meta's static analyzer for null derefs, leaks, etc. |
| **Java / Kotlin** | PMD | 🔧 | `wolf-scanners-jvm` | Multi-language source analyzer (Java, Kotlin, Apex, JS, …) |
| **Rust** | Clippy | 🔧 | `wolf-scanners-rust` | Official Rust linter |

### Coverage detection

Wolf maps source files to test files for coverage scoring:

| Language | Test patterns |
|---|---|
| **Go** | `*_test.go` — package-level coverage |
| **Python** | `test_*.py`, `*_test.py`, `tests/` |
| **JavaScript / TypeScript** | `.test.{js,ts,jsx,tsx}`, `.spec.{js,ts,jsx,tsx}`, `__tests__/` |
| **Rust** | `*_test.rs`, `test_*`, `tests/` |
| **Java / Kotlin** | `*Test.java`, `*Tests.java`, `*IT.java`, `*ITCase.java`, `src/test/` → `src/main/` mapping |
| **C / C++** | `*_test.{c,cpp,cc}`, `test_*`, `test/` → `src/` mapping |
| **C#** | `*Test.cs`, `*Tests.cs`, `.Tests/` → project mapping |
| **Ruby** | `*_spec.rb`, `*_test.rb`, `spec/` → `lib/` mapping |
| **Swift** | `*Test.swift`, `*Tests.swift`, `Tests/` → `Sources/` mapping |
| **PHP** | `*Test.php`, `tests/` → `src/` mapping |
| **Scala** | `*Test.scala`, `*Spec.scala`, `*Suite.scala`, `src/test/` → `src/main/` mapping |
| **Dart** | `*_test.dart`, `test/` → `lib/` mapping |
| **Elixir** | `*_test.exs`, `test/` → `lib/` mapping |

## Configuration

Default configuration is in `configs/wolf.yaml`. Key settings:

```yaml
scan:
  concurrency: 8                       # parallel tool execution
  timeout: "30m"                       # per-tool timeout
  default_preset: "standard"
  exclude_patterns: [vendor/, node_modules/, .git/, ...]

  # Container backend (PLAN.md §5)
  container:
    image: "ghcr.io/alphabravocompany/wolf-scanners:dev"
    image_overrides:                   # wolf-built bucket images
      infer:  "ghcr.io/alphabravocompany/wolf-scanners-jvm:dev"
      pmd:    "ghcr.io/alphabravocompany/wolf-scanners-jvm:dev"
      clippy: "ghcr.io/alphabravocompany/wolf-scanners-rust:dev"
      codeql: "ghcr.io/alphabravocompany/wolf-scanners-codeql:dev"
    # upstream_tools is auto-populated from DefaultUpstreamTools();
    # override here to pin specific upstream versions or mirrors.
    pull_policy: "IfNotPresent"        # IfNotPresent | Always | Never
    network:     "bridge"              # bridge | none (paranoid) | host
    memory:      "2g"
    cpus:        "1.5"
    db_volume:   "wolf-scanners-db"

ai:
  provider: "anthropic"                # or "openai", "noop"
  model: "claude-sonnet-4-20250514"

database:
  driver: "sqlite"                     # or "postgres"
  dsn: "~/.wolf/wolf.db"
```

Environment overrides (12-factor) — see `PLAN.md` §5.6 for the full list.

## Operations

### Diagnostics

```bash
wolf doctor
# OK    docker reachable
# OK    scanners image present
# OK    uid/gid mapped
# OK    repos-root pairing
# OK    override images known
```

### Air-gapped install

```bash
# On a connected host
docker pull ghcr.io/alphabravocompany/wolf-scanners:1.0
docker pull aquasec/trivy:0.57.0
docker pull semgrep/semgrep:1.92.0
# ... pull every upstream image you'll use (see "Supported tools" above) ...
docker save -o wolf-scanners.tar \
    ghcr.io/alphabravocompany/wolf-scanners:1.0 \
    aquasec/trivy:0.57.0 \
    semgrep/semgrep:1.92.0
gzip wolf-scanners.tar

# On the air-gapped target
gunzip < wolf-scanners.tar.gz | docker load
export WOLF_SCANNERS_PULL_POLICY=Never
docker compose up -d
```

Or set `WOLF_SCANNERS_DISABLE_UPSTREAM=1` to force everything through wolf-built bucket images (no docker.io / ghcr.io / quay.io / gcr.io reachability needed).

### Network allowlist

Operators behind corporate proxies need outbound HTTPS to:

- **docker.io** — most upstream images (`aquasec/trivy`, `semgrep/semgrep`, `zricethezav/gitleaks`, `anchore/{grype,syft}`, `trufflesecurity/trufflehog`, `hadolint/hadolint`, `goodwithtech/dockle`, `bridgecrew/checkov`, `stackrox/kube-linter`, `projectdiscovery/nuclei`, `jdkato/vale`, `stoplight/spectral`)
- **ghcr.io** — `ghcr.io/alphabravocompany/wolf-scanners*`, `ghcr.io/terraform-linters/tflint`, `ghcr.io/nexb/scancode-toolkit`, `ghcr.io/google/osv-scanner`, `ghcr.io/renovatebot/renovate`
- **us-docker.pkg.dev** — `us-docker.pkg.dev/fairwinds-ops/oss/pluto`
- **quay.io** — `quay.io/kubescape/kubescape-cli`
- **gcr.io** — `gcr.io/openssf/scorecard`

Mirror these to an internal registry for tighter control.

## Adding a new tool

See `scanners/README.md` for the full how-to. Short version:

1. **Decide the tier**: if the maintainer publishes a multi-arch image, prefer Tier 1 (no wolf rebuild).
2. **Pin the version** in `scanners/versions.env`.
3. For **Tier 1**: add the tool → `ToolImageSpec` entry in `internal/plugin/container/buckets.go`'s `DefaultUpstreamTools()`.
4. For **Tier 2** (default image): add an install line in the appropriate `scanners/install/*.sh`, and a smoke-test line in `scanners/smoke-test.sh`.
5. **Write the wolf plugin** at `plugins/<bucket>/<tool>.go` using `container.CommandContext`. See `plugins/python/bandit.go` for the canonical pattern.
6. **Update `scanners/LICENSES.md`** with the tool's license.

## License

Proprietary. Copyright AlphaBravo, Inc. All rights reserved.
