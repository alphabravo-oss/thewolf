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
- **AI enrichment** (optional) — AI-powered severity scoring, tool summaries, prioritized recommendations, and ready-to-hand-off remediation prompts via `wolf enrich`
- **Automated fix engine** — AI-generated fix plans with PR creation via Claude Code
- **Scan-fix-rescan loops** — iterative improvement cycles with regression guardrails (`wolf loop`)
- **Baselines & diff** — pin a stable scan as a baseline and surface only `new`/`resurfaced`/`fixed` findings on the next run
- **Quality gates** — declarative policies that block CI on severity counts, new findings, or category thresholds (`wolf scan gate` exits 5 on failure)
- **Finding suppressions** — durable, audit-logged hide rules with `.wolfignore` and server-side suppression policies
- **SARIF interop** — consume external scanner output via `wolf sarif import`, export any scan as SARIF
- **Web UI** — Vite + React 19 + TanStack Router + Tailwind 4 + shadcn/ui dashboard with collection management, scan monitoring, finding exploration, fleet posture, and scanner-backend admin
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

# Write AI-ready remediation prompts into the findings.json
wolf enrich --scan <scan-id>

# Run the AI auto-remediation loop (scan → fix → rescan)
wolf loop --repo /repos/myproject --max-iterations 5

# Diagnose the scanner backend
wolf doctor

# Pre-pull every configured scanner image
wolf pull scanners

# Start the API server (also auto-runs in docker-compose)
wolf serve --bind 0.0.0.0:8778

# Print version info
wolf version
```

## API & CLI

The HTTP API and the `wolf` CLI expose the same capabilities, so humans,
CI pipelines, and AI agents can automate every part of the product.

### API

- All endpoints live under `/api/v1`. The legacy `/api/*` paths still work
  via a deprecating redirect for one release.
- Interactive documentation: **`/api/v1/docs`** (Swagger UI) and
  `/api/v1/docs/redoc`. The raw spec is at `/api/v1/openapi.json`. These
  are public — no credential needed to read the docs.
- Authenticate with either a JWT (from `POST /api/v1/auth/login`, used by
  the web UI) or a **`wolf_` API token** for non-interactive use, both
  sent as `Authorization: Bearer <credential>`.

### API tokens & scopes

API tokens are scoped, revocable credentials for automation. Scopes are
`verb:resource` strings (`read:scans`, `write:repos`, …) plus `admin`;
`write:X` implies `read:X`. Tokens default to a 90-day expiry.

```bash
# Mint a least-privilege token (the secret is shown once)
wolf auth token create --name ci --scope read:scans --scope write:scans
wolf auth token list
wolf auth token revoke <id>
```

### CLI as an API client

Beyond the local `wolf scan`, every API endpoint is a `wolf <resource>
<verb>` command. Point the CLI at a server with flags, a saved context,
or `WOLF_SERVER` / `WOLF_TOKEN`:

```bash
# Save a reusable context (~/.wolf/cli.yaml)
wolf config set-context prod --server https://wolf.internal --token wolf_…
wolf config use-context prod

# Drive the API — add -o json for machine-readable output
wolf repo create --name acme --path /repos/acme
SCAN=$(wolf scan create --repo <repo-id> -o json | jq -r .data.id)
wolf scan watch "$SCAN"
wolf scan findings "$SCAN" --severity high -o json
wolf finding set-status <finding-id> --status false_positive

# Quality gates and baselines for CI
wolf baseline create --repo <repo-id> --scan "$SCAN"
wolf scan diff "$SCAN"                       # new vs the baseline
wolf scan gate "$SCAN" --fail-exit-code      # exits 5 on policy violation
wolf sarif export "$SCAN" > findings.sarif   # consume in your CI dashboard
wolf suppress create --file-glob "vendor/**" --reason "third-party code"
```

CLI output is a table on a terminal and JSON when piped. Exit codes:
`0` success, `1` runtime error, `2` usage error, `3` not found,
`4` auth/permission, `5` quality gate failed.

### Web UI

The Settings → "AI features" toggle is the master switch (defaults off).
The Repositories page can create a repo of any source type — local path,
GitHub (use a `github_token` secret for private repos), remote git URL,
or SSH node. Loops, Fixes, Scanners, and the admin Audit log all have
dedicated nav entries.

### Fleet mode

When you're managing 20+ repos across multiple hosts:
- Flip the `fleet_mode` setting in Settings → General. All Repos / Scans / Findings / Collections become visible org-wide.
- Use **Import from GitHub** on the Repos page to pull every repo from an org via your `github_token` secret.
- Use **Discover repos** on a node detail page to multi-select git directories on that SSH host.
- The **/** Fleet dashboard shows posture, vulnerable components, attention list, and inventory across the whole fleet.

## Architecture

```text
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
ui/                          Vite + React 19 frontend (incl. /scanners admin page)
configs/                     Default configuration
docs/                        MIGRATION_2_0.md, RELEASE_NOTES_2_0.md
```

## Scanner backend — the three-tier hybrid

Wolf doesn't install scanners on the host. Every tool invocation is a `docker run` from one of three image tiers:

### Tier 1 — Upstream-official images (no wolf rebuild)

For tools where the maintainer publishes a usable image, wolf routes invocations directly to their published image. We pin the version in `scanners/tools.yaml` and `scanners/versions.env`. **22 tools currently use this tier** — see "Supported tools" below.

### Tier 2 — Wolf-built default image (`wolf-scanners`)

A slim image (~600–900 MB on amd64) carrying the per-language tools that don't have a maintained upstream image — mostly small pip / npm / go / gem / composer installs.

### Tier 3 — Wolf-built bucket images

Heavy toolchains that don't make sense in the default image and don't have a clean upstream:

| Image | Tools | Approx size | Pulled when |
|---|---|---|---|
| `wolf-scanners-jvm` | detekt, infer, pmd (+ JDK) | ~2 GB | when a Java/Kotlin/C/C++ tool runs |
| `wolf-scanners-rust` | clippy (+ rust toolchain) | ~1.2 GB | when a Rust scan runs |
| `wolf-scanners-codeql` | codeql + query packs | ~700 MB | only when codeql is explicitly enabled (license-gated) |

A typical Python+JS shop pulls **~700 MB plus a few small upstream images** (semgrep, trivy, gitleaks, etc.). A polyglot shop adds the relevant bucket images on demand.

The shim's `Config.ImageFor(toolName)` walks: `UpstreamTools` → `ImageOverrides` → default `Image`. Operators override either map via `scan.container.upstream_tools` / `scan.container.image_overrides` in wolf.yaml.

Disable the upstream tier entirely (e.g. air-gapped, internal-mirror-only) with `WOLF_SCANNERS_DISABLE_UPSTREAM=1`.

See `PLAN.md` for the full architecture; `scanners/tools.yaml` for the authoritative scanner manifest; `scanners/TOOLS.md` for the generated scanner table; `scanners/README.md` for how to add or upgrade tools; `docs/MIGRATION_2_0.md` for the upgrade path from wolf 1.x.

## Rebuilding the wolf-built images (server-side)

The four wolf-built images (`wolf-scanners`, `-jvm`, `-rust`, `-codeql`) can be rebuilt directly from the wolf server — no repo checkout, no host toolchains. The full `scanners/` build context is `go:embed`-ed into the binary, so the server runs `docker buildx build` itself (Docker is already a hard dependency for scanning). Rebuild from **Settings → Scanner Images** in the UI, or via the API:

```bash
# Rebuild one variant locally (no credentials needed) — loads into the local daemon
curl -X POST .../api/v1/scanners/images/default/build -d '{"push": false}'

# Rebuild all four
curl -X POST .../api/v1/scanners/images/build-all -d '{"push": false}'
```

Both endpoints stream the live `docker buildx` output over SSE and are admin-scoped (`write:config`, audit-logged). Server-local builds are **single-arch** (the host's platform); CI (`scanners-image.yml`) remains the multi-arch (amd64 + arm64) publisher.

**Local builds need no credentials.** With `push: false` (the default) the server builds with `docker buildx build --load`, loading the image straight into the local Docker daemon. The scanner backend's pull policy is `IfNotPresent`, so the next scan finds the freshly-built image present and runs it with no registry round-trip. A fresh wolf install with zero credentials can rebuild every image and scan with it.

### Optional — publishing to DockerHub

Credentials gate **publishing only**. To push a rebuilt image to DockerHub, store a PAT as an *optional* `dockerhub_token` secret (username in `key_name`, the PAT as the encrypted value):

```bash
curl -X POST .../api/v1/config/secrets \
  -d '{"key_type": "dockerhub_token", "key_name": "<dockerhub-username>", "value": "<PAT>"}'
```

With the secret present, a `push: true` build logs in via `--password-stdin` (the PAT never reaches argv, logs, or the SSE stream), tags the image `:<wolf-version>` **and** `:latest`, and pushes to `docker.io/<namespace>/<image>` (namespace from the `scanner_registry_namespace` setting, default `alphabravodevops`). In the UI, a **"push to DockerHub"** toggle appears beside the Rebuild button only when the secret exists; absent credentials hide the toggle but never block local builds. A `push: true` request with no `dockerhub_token` secret returns `404` with a hint.

### Selecting which tag the runtime pulls — `WOLF_SCANNERS_TAG`

The runtime resolves each image ref as `<namespace>/<image>:<tag>`, where `<tag>` comes from `WOLF_SCANNERS_TAG` (default `2.0.0`). The active resolved tag is logged at startup. CI publishes `latest` and `dev`, so until a versioned release is cut, set `WOLF_SCANNERS_TAG=latest` to track the published images:

```bash
WOLF_SCANNERS_TAG=latest wolf serve
```

A server-side build/push tags the running wolf version automatically, so once you publish from the UI the default tag resolves cleanly.

### Slimmer Go layer

The default image's Go-toolchain layer (gosec/govulncheck need `go list`/`go env` at runtime) is slimmed by `scanners/install/go-tools.sh`: it drops `$GOPATH/pkg` (module/build cache) and `$GOPATH/bin`, and strips the toolchain's `test/`, `doc/`, `api/`, and `misc/` dirs. The Go tools still resolve modules (validated by `scanners/smoke-test.sh`), and the default image's unpacked size drops by ≥600 MB with no tool regressions.

## Supported tools

**Total: 49 scanners** across SAST, SCA, secrets, container, infrastructure, IaC, policy-as-code, license, SBOM, docs, DAST, repo-hygiene, dependency-freshness, privacy/PII, deprecated-API detection, and per-language categories.

### By tier

| Tier | Count | How wolf gets the binary |
|---|---|---|
| **Upstream-official images** | 22 | `docker pull aquasec/trivy`, etc. — maintainer-published |
| **Wolf-built default image** | 22 | bundled in `wolf-scanners:<version>` via pip / npm / go install / apt / composer / github-release |
| **Wolf-built bucket images** | 5 | bundled in `wolf-scanners-{jvm,rust,codeql}:<version>` |

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

### Activating optional bucket scanners (PMD, CodeQL)

The default `wolf-scanners` image is kept lean — heavy JVM and CodeQL
toolchains are not included so the base image stays under ~3 GB. To
enable `pmd` (Java) or `codeql`, build the bucket image and point wolf
at it:

```bash
# PMD lives in the JVM bucket (with infer)
make scanners-build-jvm
export WOLF_SCANNERS_IMAGE_JVM=wolf-scanners-jvm:dev

# CodeQL has its own ~800MB bucket
make scanners-build-codeql
export WOLF_SCANNERS_IMAGE_CODEQL=wolf-scanners-codeql:dev
```

Without these env vars wolf will list `pmd` and `codeql` among the
selected scanners but they exit with `tool not present in this image`.
This is intentional — operators opt in to the heavyweight buckets.

### Scanner DB cache (auto-configured)

Vulnerability-database scanners (`grype`, `trivy`) cache their DBs on
the host at `~/.wolf/scanner-cache/` and reuse them across runs. The
first scan downloads (~30s); subsequent scans are instant.

Override with `WOLF_SCANNERS_DB_VOLUME=<host-path>` (or `=""` to disable
caching entirely and re-download each run).

### Scanners that are skipped on arm64 hosts

A few upstream images are published amd64-only and crash under qemu
emulation on Apple Silicon / arm64 Linux. Wolf detects this and skips
them cleanly with a `[SKIP]` message rather than surfacing the
qemu-induced crash:

- `bearer` — Go runtime panics under emulation
- `scorecard` — image fails to find its entrypoint under emulation

To use these scanners, run wolf from an amd64 host (CI, x86_64 server,
etc.) or omit them from `--tools`.

### Scanners that need a target argument

Two scanners don't apply to source code and require an explicit target:

- `nuclei` — DAST tool; pass `--target https://example.com` (an HTTP
  endpoint to probe). Skips cleanly when no target is provided.
- `dockle` — scans built container images; pass `--target <image:tag>`
  for an image the operator has already built or pulled. Skips cleanly
  when no target is provided.

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
- **ghcr.io** — `ghcr.io/alphabravocompany/wolf-scanners*`, `ghcr.io/terraform-linters/tflint`, `ghcr.io/google/osv-scanner`, `ghcr.io/renovatebot/renovate`
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
