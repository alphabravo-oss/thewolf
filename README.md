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

## Supported Tools

| Category | Tools |
|----------|-------|
| **General Security** | Semgrep, Trivy, Gitleaks, TruffleHog, Grype, OSV-Scanner, Detect-Secrets |
| **Go** | Gosec, Staticcheck, Govulncheck |
| **Python** | Bandit, Ruff, Mypy, Pip-audit, Radon, Vulture |
| **JavaScript** | ESLint, npm-audit |
| **Rust** | Clippy |
| **Containers** | Hadolint, Dockle, Checkov |
| **Infrastructure** | TFLint, Kubescape, Kube-linter |
| **Additional** | CodeQL, PMD, ShellCheck, ScanCode |
| **SBOM** | Syft |
| **Docs** | Spectral, Vale |

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
