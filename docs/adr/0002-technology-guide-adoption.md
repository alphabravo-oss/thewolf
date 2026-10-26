# Technology guide adoption — 2026-10-13

Status: partial adoption with outstanding work, not full compliance.

Baseline: [AlphaBravo technology selection guide](https://technology-selection-guide.aws.ablabs.io/).
Use current stable releases instead of the guide's historical version snapshots.
Do not expand disconnected/air-gap functionality in this work. Preserve Wolf's
scanner factory, fix harnesses and public REST/CLI interfaces.

Implemented in this increment:

- Go 1.27.2, Node 26.11.1 Current, pnpm 12.11.2, React 19.3, Vite 8.3.4,
  TypeScript 7.0.2, Tailwind 4.3.3, refreshed direct dependencies and lockfiles.
- Current TanStack Query, Router, Table, Form, Virtual and Pacer. Query owns
  server state; URL filters and server pagination remain consistent. The
  TypeScript 6 JavaScript API package is retained only for ESLint compatibility,
  following Microsoft's side-by-side migration guidance.
- PostgreSQL 18.6 container pin, pgx v5, restart-safe existing migrations and
  concurrent-start serialization. PostgreSQL and SQLite run in CI.
- Fifteen-minute access tokens, explicit token purposes, persisted refresh
  rotation/replay handling and logout revocation. Browser opaque sessions remain.
- Official MCP Go SDK with Streamable HTTP and a stdio bridge. Execution tools
  use existing REST authorization, validation, auditing and durable scan jobs.
- CI vulnerability/secret checks and build-cache isolation with exact scanner
  versions, checksums, OCI digests and existing promotion controls.

Outstanding guide adoption:

| Area | Current boundary | Acceptance still needed |
| --- | --- | --- |
| Typed contracts | Existing REST/OpenAPI and frontend types remain | Introduce Connect/protobuf at existing operation boundaries without breaking REST/CLI/MCP; generate clients and test contract parity. |
| Data access | pgx through sqlx; custom SQL migrations; SQLite laptop mode | Move deliberately to sqlc/goose with a tested migration baseline and upgrade/rollback fixtures; decide the supported PostgreSQL default deployment path. |
| Construction | Mutable handler/auth globals remain | Inject dependencies at composition roots and prove multiple instances are isolated. |
| Observability | Existing logs/audit/events remain | OTLP traces/metrics, request-to-job propagation, dashboards and operational acceptance. |
| Key lifecycle | Existing HS256 JWT signing and existing encrypted-secret system remain | Rotatable asymmetric signing-key ring and the guide's age/key-rotation lifecycle. |
| Automated remediation | REST/CLI fix engine retained; MCP fix tools read only | Durable idempotent fix creation and explicit branch/push intent before MCP can mutate remediation jobs. |
| Scanner currency | Debian snapshot pins Cppcheck/ShellCheck; upstream images locked | Latest upstream native-tool packaging where required, all parser fixtures, native multi-architecture builds and signed release qualification. |
| Security triage | gosec now runs with a Go 1.27-compatible locked importer | Review 43 remaining high-severity/high-confidence taint-analysis alerts (35 path, 5 command, 3 network); do not treat untriaged alerts as confirmed exploits or blanket false positives. CI retains the JSON report and blocks on them. |
| Release readiness | Local tests/builds only | Deploy a traceable artifact, migrate existing databases deliberately, validate a real remote scan/fix/PR/gate journey and promote qualified stable images. |

These are visible remaining requirements, not silent waivers of the guide.
Passing mocked browser tests or local container smoke does not establish an
"elite" UX or qualify an unattended production rollout.

Validation record: the full Go race suite passed in 135 test packages against
SQLite and PostgreSQL 18. After the final confinement changes, the affected API,
CLI executable, fixer hygiene and release/lock checks were run again. Go lint
passed; UI typecheck, lint, 42 unit tests, production build, browser harness
checks and 21 browser tests passed (two platform-specific skips). Reachable
Go vulnerability checks found none; one required module has an uncalled
vulnerability. Full-history secret scanning passed with four exact, documented
synthetic-fixture exclusions. Gosec is not clean: its 43 remaining taint alerts
are a release blocker, with the complete report retained by CI.

Confinement fixes use Go's os.Root for static UI serving, mechanical remediation
reads/writes and Git HEAD/ref reading. Regression tests reject absolute paths,
parent traversal and escaping symlinks. No broad security-rule exclusions were
added to obtain a passing result.
