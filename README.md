<p align="center">
  <img src="docs/wolf-logo.svg" alt="The Wolf" width="120" />
</p>

<h1 align="center">The Wolf</h1>
<p align="center"><strong>Multi Tool Code Analysis Engine</strong></p>
<p align="center">by <a href="https://alphabravo.io">AlphaBravo</a></p>

---

The Wolf is an orchestration engine that runs multiple static analysis, security scanning, and code quality tools in parallel across your repositories. It aggregates findings, deduplicates results, scores severity, and optionally uses AI to enrich and prioritize issues.

## Features

- **Multi-tool orchestration** — runs 30+ analysis tools (Semgrep, Trivy, Gitleaks, Bandit, ESLint, Gosec, and more) in parallel with configurable concurrency
- **Collection-based organization** — group repositories into collections for batch scanning and cross-repo metrics
- **Branch-aware metrics** — track findings per branch with trend analysis over time
- **Real-time streaming** — live scan progress via SSE with per-tool status, output logs, and finding counts
- **AI enrichment** (optional) — AI-powered severity scoring, tool summaries, and prioritized recommendations via Anthropic or OpenAI
- **Automated fix engine** — AI-generated fix plans with PR creation via Claude Code
- **Scan-fix-rescan loops** — iterative improvement cycles with regression guardrails
- **Web UI** — Next.js dashboard with collection management, scan monitoring, finding exploration, and settings
- **CLI** — full-featured command-line interface for scripting and CI/CD integration
- **Plugin architecture** — extensible plugin system supporting Go, Python, JavaScript, Rust, containers, infrastructure, and more
- **SARIF & Markdown reports** — export findings in standard formats
- **Durable artifact storage** — scan outputs persisted to `~/.wolf/artifacts/` with future S3 support

## Quick Start

### Prerequisites

- Go 1.23+
- Node.js 18+ (for the UI)
- One or more analysis tools installed (see [Supported Tools](#supported-tools))

### Build & Run

```bash
# Build the backend
go build -o wolf ./cmd/wolf/

# Set up the database and create an admin user
./wolf setup

# Start the server (API + embedded UI)
./wolf serve --port 8778

# Open the UI
open http://localhost:3000
```

### CLI Usage

```bash
# Scan a local repository
./wolf scan --repo /path/to/repo --branch main

# Scan with specific tools
./wolf scan --repo /path/to/repo --tools semgrep,trivy,gitleaks

# Manage collections
./wolf collection create --name my-services
./wolf collection add-repo --collection my-services --repo /path/to/repo
./wolf collection scan --name my-services
```

## Architecture

```
cmd/wolf/          CLI entrypoints (serve, scan, collection, setup, etc.)
internal/
  api/             HTTP server, routes, SSE broker, middleware
  ai/              AI provider abstraction (Anthropic, OpenAI, noop)
  artifacts/       Durable scan artifact storage
  auth/            JWT authentication middleware
  db/              Database layer (SQLite + PostgreSQL)
  fix/             Automated fix engine (planner, git, PR)
  loop/            Scan-fix-rescan loop orchestration
  models/          Domain models
  plugin/          Plugin registry
  scan/            Scan pipeline (runner, detector, scorer, mapper, reporter)
plugins/           Tool plugins organized by language/category
ui/                Next.js frontend application
configs/           Default configuration
```

## Supported Languages & Tools

### Language-Specific Analysis Tools

| Language | Analysis Tools | Test Coverage Detection |
|----------|---------------|----------------------|
| **Go** | Gosec, Staticcheck, Govulncheck | `*_test.go` — package-level (any test covers all source files in the directory) |
| **Python** | Bandit, Ruff, Mypy, Pip-audit, Radon, Vulture | `test_*.py`, `*_test.py`, `tests/` directory |
| **JavaScript / TypeScript** | ESLint, npm-audit | `.test.{js,ts,jsx,tsx}`, `.spec.{js,ts,jsx,tsx}`, `__tests__/` directory |
| **Rust** | Clippy | `*_test.rs`, `test_*`, `tests/` directory |
| **Java / Kotlin** | Infer, PMD | `*Test.java`, `*Tests.java`, `*IT.java`, `*ITCase.java`, `src/test/` → `src/main/` mapping |
| **C / C++** | Cppcheck, Infer | `*_test.{c,cpp,cc}`, `test_*`, `test/` → `src/` mapping |
| **C#** | cross-language tools only | `*Test.cs`, `*Tests.cs`, `.Tests/` → project mapping |
| **Ruby** | Brakeman, RuboCop | `*_spec.rb`, `*_test.rb`, `spec/` → `lib/` mapping |
| **Swift** | SwiftLint | `*Test.swift`, `*Tests.swift`, `Tests/` → `Sources/` mapping |
| **PHP** | PHPStan | `*Test.php`, `tests/` → `src/` mapping |
| **Scala** | cross-language tools only | `*Test.scala`, `*Spec.scala`, `*Suite.scala`, `src/test/` → `src/main/` mapping |
| **Dart** | cross-language tools only | `*_test.dart`, `test/` → `lib/` mapping |
| **Elixir** | cross-language tools only | `*_test.exs`, `test/` → `lib/` mapping |
| **SQL** | SQLFluff | — |
| **Shell** | ShellCheck | — |

### Cross-Language Tools

| Category | Tools | Description |
|----------|-------|-------------|
| **SAST** | Semgrep, CodeQL | Pattern-based and semantic static analysis across all languages |
| **SCA** | Trivy, Grype, OSV-Scanner | Dependency vulnerability scanning |
| **Secrets** | Gitleaks, TruffleHog, Detect-Secrets | Secret and credential detection in source code |
| **Containers** | Hadolint, Dockle, Checkov | Dockerfile linting, image checks, IaC security |
| **Infrastructure** | TFLint, Kubescape, Kube-linter | Terraform and Kubernetes configuration scanning |
| **SBOM** | Syft | Software bill of materials generation |
| **License** | ScanCode | License compliance scanning |
| **Docs** | Spectral, Vale | API spec linting and documentation style checking |
| **DAST** | Nuclei | Template-based dynamic vulnerability scanning |

## Configuration

Default configuration is in `configs/wolf.yaml`. Key settings:

```yaml
scan:
  concurrency: 8        # parallel tool execution
  timeout: "30m"        # per-tool timeout

ai:
  provider: "anthropic" # or "openai", "noop"
  model: "claude-sonnet-4-20250514"

database:
  driver: "sqlite"      # or "postgres"
  dsn: "~/.wolf/wolf.db"
```

## Docker

```bash
docker-compose up -d
```

## License

Proprietary. Copyright AlphaBravo, Inc. All rights reserved.
