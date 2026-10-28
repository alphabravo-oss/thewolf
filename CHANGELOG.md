# Changelog

## [Unreleased]

- **Breaking: SQLite is no longer supported.** PostgreSQL is the only runtime
  database. Migrate existing installs **before** upgrading with
  `wolf db migrate-from-sqlite` (docs/deployment.md, "Move from SQLite to
  PostgreSQL"; the command is kept for one more release). `WOLF_DB_DRIVER=sqlite`
  now fails with that pointer, `WOLF_DB_DRIVER` defaults to `postgres`, and Wolf
  still refuses to start on an empty PostgreSQL while an unmigrated
  `~/.wolf/wolf.db` exists. `wolf backup` / `wolf restore` only handled SQLite and
  now print the replacement (`pg_dump`/`pg_restore` or CNPG Barman); the one-worker
  SQLite limit on `scan-worker` is gone. Tests run against PostgreSQL:
  set `WOLF_TEST_POSTGRES_DSN` (see `db.NewTestPostgres`); CI sets it.
- Perf: the bundled PostgreSQL service now starts with `shared_buffers=512MB`, `effective_cache_size=1536MB`, `work_mem=16MB` and a 2 CPU / 2 GB limit (was 1 CPU / 512 MB, which re-read the findings table on every dashboard load). Override with `POSTGRES_SHARED_BUFFERS`, `POSTGRES_EFFECTIVE_CACHE_SIZE`, `POSTGRES_WORK_MEM`, `POSTGRES_CPUS`, `POSTGRES_MEMORY`.
- Performance: the Home and Reports aggregates (`/fleet/posture`, `/findings/trends`,
  `/findings/by-repo`, `/fleet/report`, needs-attention, top rules, the findings
  list) no longer load every historical finding per request. Current state reads
  only the latest complete scan of each branch plus later attempts; first-seen and
  new-vs-resurfaced are answered with a query for just the issues involved; trends
  load each distinct scan once. On a 250k-finding database `/fleet/posture` went
  from 3.2 s to 0.11 s, `/findings/trends` from 4.6 s to 0.45 s and `/fleet/report`
  from 10.8 s to 0.48 s, with identical results.
- PostgreSQL is now the Compose default (`postgres` service always on, services
  wait for it healthy, `WOLF_DB_DRIVER=postgres`, DSN built from
  `POSTGRES_PASSWORD`). SQLite support ends after this release. New
  `wolf db migrate-from-sqlite --sqlite <wolf.db> [--postgres <dsn>]` copies an
  existing install in one verified transaction (read-only source, row counts
  checked, rollback on mismatch); a 635 MB install moved in ~18 s. Wolf refuses
  to start on an empty PostgreSQL while an unmigrated `~/.wolf/wolf.db` exists.
  See docs/deployment.md "Move from SQLite to PostgreSQL".
- Security: Enterprise integration `authorization` headers (SIEM, ticketing,
  integrations) are now encrypted at rest with the Community master key
  (AES-256-GCM, same as stored credentials). Community gains `pkg/control.Sealer`
  (`BindSealer`/`SealerAPI`, values prefixed `wolf-sealed:v1:`) for overlays.
  Existing plaintext values keep delivering and are sealed on the next edit.
- Fix: "Scan coverage" on Home and Reports no longer shows full coverage in red;
  the shared KPI gauge now treats higher as better (red below 50%, amber below
  90%). Fix: `/api/v1/version`, the sidebar footer and `wolf version` report
  0.2.2 instead of 0.2.0 when the build sets no ldflags version; a Go test keeps
  `routes.AppVersion` equal to `ui/package.json`.
- detect-secrets: entropy-only hits (`Hex High Entropy String`,
  `Base64 High Entropy String`) are reported as low instead of high. They mostly
  match SHA-256 digests in generated JSON; one astronomer scan raised 33.7k high
  findings this way. Pattern-based detectors stay high. Existing findings are not
  migrated; new scans get the new severity.

- Reports (Enterprise, `enterprise.compliance`): a **Reports** sidebar item with a
  print-ready "Security posture" report for every repository or one collection
  (open critical/high with a 30-day trend, new vs resolved this week, coverage,
  gates failing, fix branches pushed, age over 30/60/90 days, top 10 riskiest
  repositories, all labelled observed, not audit-grade). "Download PDF" uses the
  browser print dialog with a print stylesheet. Saved reports and weekly/monthly
  schedules are Enterprise. Community gains `GET /fleet/report` and
  `pkg/control.Fleet` (`BindFleet`/`FleetAPI`) for overlays; Community shows the
  upgrade card.

- Home is a dashboard: KPI row with a 30-day sparkline, seven drill-down charts
  (observed open findings daily, new vs resolved, riskiest repositories, top rules,
  age of open critical/high, scanner health, fix pipeline), "Act on now" and recent
  activity; each card loads, fails and retries on its own. `/fleet/posture`
  priorities gain `first_seen_at`; `/findings` accepts `sort=first_seen`; the
  findings list accepts `severity=critical,high`, By rule filters on `q`, and
  `/agents` accepts `?stage=`. See `docs/dashboard.md`.
- Fold Enterprise into Settings and remove the sidebar Enterprise section: Single
  sign-on and Directory sync under Users & access, SIEM/ticketing destinations
  (typed name/URL/authorization, send test, delete) under Integrations, Fixer engines
  and Git access under AI fixing, and License & support (what the edition includes in
  plain words, license install, diagnostics bundle download, contact link). Old
  `/enterprise/*` links redirect to the matching section; the stub module pages, the
  hub and the vulnerability attack-path/verification tabs are gone for now.

- UI polish pass: Home recent activity reads in plain language (`lib/audit.ts`), the
  dashboard uses equal-height cards with collapsible "Keep work moving" and "Are we
  improving?" sections, agent/fix detail uses the page template (header with
  breadcrumbs and status, KPI strip, Overview/Decisions tabs, log in a `Section`),
  Vulnerabilities gains a KPI strip, the Enterprise hub edition tile no longer labels
  Enterprise as Community, the sidebar Enterprise group links to the hub, and
  Settings/Account section pages use a wider shared measure (`.section-page`).

- Remove the Community evaluation caps (5 repositories, 3 users, 1 concurrent scan
  worker). Community is uncapped; Enterprise is differentiated by capabilities, not
  limits. `WOLF_COMMUNITY_LIMITS`, the `community_limit` error and the `limits` field
  of `GET /api/edition` are gone.

- License Community under Apache-2.0: add `LICENSE`, replace the source-available/BSL
  wording in `NOTICE`, `LICENSE_POLICY.md`, README, CONTRIBUTING and image metadata.

- Let AI remediation (Enterprise) fix GitLab projects on gitlab.com or a self-hosted
  instance: Git/GitLab repos accept a host-bound `gitlab_token` credential, the
  writability preflight checks the token's role through the GitLab API, and fix
  branches clone and push with it. `WOLF_GIT_CA_BUNDLE` adds a private CA for
  self-hosted servers. Repo creation now validates the selected Git credential and
  its host binding.

- Move Kubernetes Helm packaging, its security render checks and the kind remote-scan
  qualification to Wolf Enterprise. Community deploys with Docker Compose; the
  native and Compose remote-scan qualification lanes remain.

- Keep scan totals, quality gates and finding detail on one suppression projection
  per operation. Refresh live manifest counts after rule/default edits while retaining
  recorded producer metadata and immutable historical downloads.


- Evaluate Git ignores with literal NUL-delimited paths, controlled Git settings,
  validated policy files and bounded cancellation. Failures leave suppression
  projections unchanged and reach live views, report publication and rule transactions.


- Reject malformed or unreadable `.wolfignore` rules without partial suppression;
  propagate failures through live views, gates, report/CLI publication and durable
  rule transactions. Suppression previews share one parsed ruleset, and scan
  statistics now match path-suppressed finding lists.


- Keep partially suppressed persisted clusters visible by selecting an open,
  unsuppressed representative using the runner's existing preference. Retain native
  finding IDs, fix references, evidence membership and canonical issue identity.
  Recompute promoted scores/history from their own inputs and roll back all changes
  together. Preview now distinguishes matched issues/evidence from newly hidden
  issues; the UI confirmation and saved-message reflect that distinction.

- Make durable suppression creation, revocation and expiry transactional with
  finding flags, counts, revisions, existing canonical visibility and audit records.
  Preserve concurrent triage and reapply overlapping active rules. Native/import
  inserts read current rules inside their transaction. Run expiry at server startup
  and once per minute; retry failures. Preview only the newest scan in the requested
  branch or repository and return its identity, including empty scans. Reject past
  expiry dates and stop advertising unsupported advisory scope/ignore negation.

- Limit built-in lockfile suppression to SAST and secrets using explicit analysis
  category predicates. SCA, license, container and unknown-category findings remain
  visible unless another rule applies. Custom reason labels no longer change rule
  behavior. Finding detail, evidence and triage responses now use the same path
  suppression matcher as lists, gates and reports.

- Retry finding-revision conflicts during native/import finalization from fresh
  persisted snapshots, rerendering reports and the gate without rerunning scanners.
  Bound retries to three attempts and preserve cancellation/lease-loss guards.
  Publish original SARIF bytes through the existing immutable artifact writer.
  Executor-process recovery immediately before/after commit is qualified on
  SQLite/PostgreSQL; broader deployment and failure-matrix work remains open.

- Commit scan completion, canonical evidence, final gate/artifact reference,
  first-success baseline and import claim together. Keep execution ownership until
  publication succeeds; reject stale/cancelled completion. Migration 067 binds gate
  caches to scan status as well as finding revision, preventing a late running gate
  from replacing the final result. Failed imports leave no checksum reservation;
  identical uploads can retry, and clean SARIF runs retain tool metadata.

- Reconcile baseline history transactionally during manual merges and splits.
  Migration 066 binds derived dates to their source identity so a joined member
  cannot retain or propagate another issue's history. Regrouped legacy observations
  without a pinned baseline become unknown; unrelated history is preserved.
  Baseline-sensitive gates report incomplete when relevant state is unknown.

- Persist baseline classification and observation dates transactionally on both
  databases. Keep the selected branch baseline across replay, retain original
  introduction on resurfacing, include history beyond 20 scans, and preserve
  triage and native evidence. Migration 065 leaves legacy dates unknown. Validate
  saved baseline execution/ownership, propagate failures, derive imported baseline
  state locally, and include persisted history in JSON/SARIF reports. Fixed/resolved
  transitions and canonical timeline/backfill qualification remain open.

- Publish recorded scan artifacts at unique paths with matching checksums; repeated
  gate/diff requests and retries cannot overwrite earlier downloads. Reject corrupt
  content with HTTP 409 and expose known finding revisions. Migration 064 retains
  unversioned legacy rows. Propagate bundle/log/metadata write failures, preserve
  current finding state on late scan failure, and regenerate stale live manifests
  while keeping historical downloads. Whole-scan/recovery qualification remains open.

- Reconcile scan headers after manual grouping and evaluate compact scan results
  from a consistent finding snapshot. Migration 063 versions persisted findings
  and gates, invalidates unversioned gate caches and rejects stale gate writes.
  Propagate policy/snapshot errors; generated Markdown/SARIF now applies shared
  path suppressions and identifies its finding revision. Automatic finalization retry and
  process recovery remain D02 work; upgrade API/workers together.

- Make vulnerability merge/split and canonical persistence transactional on
  SQLite and PostgreSQL. Keep native finding IDs/evidence, reconcile finding
  membership, reject invalid subsets and active scans, and preserve manual
  decisions during replay. Clear inherited confidence after manual association
  and propagate evidence-read failures. Whole-scan recovery and
  future-scan membership policy still require the remaining D02 work.

- Persist finding clusters without deleting native evidence or changing member
  IDs. Reconcile list, fleet and report counts; retain contributor filters and
  per-tool artifacts. Serialize triage and recovery with cluster finalization,
  and keep open evidence visible when another member was suppressed or triaged.
  Surface canonical-write failures and fix PostgreSQL suppression encoding.
  Existing scans are not backfilled; full D02 qualification remains open.

- Build final scan aggregates from persisted findings so reports retain database
  IDs, including resumed scans. Finish enrichment before scoring and reload final
  database values for artifacts; finding insert/read/update errors fail the scan.
  Per-tool evidence rows remain intact while full cluster reconciliation is open.

- Preserve vulnerable dependency identity across scanner output, storage, API and
  JSON/SARIF reports. New SCA fingerprints include package, advisory and location;
  separate packages and CVEs no longer collapse in one lockfile. Keep all Grype
  locations and the selected npm/pip manifest path. Migration 061 adds dependency
  metadata without rewriting historical v1 findings; legacy baseline continuity
  and final persisted-cluster reconciliation remain separate qualification work.

- Validate retained scan source identity before reusing completed tools or resetting
  incomplete evidence. Preserve SSH archive provenance, keep retryable workspaces,
  and guard recovery deletions with the scan lease on SQLite and PostgreSQL.
  Git/GitHub/local/SSH preparation now uses versioned, length-framed content digests
  that distinguish binary entry boundaries, symlinks and permissions. Legacy
  in-flight digests require a new scan if recovery cannot verify the source.

- Apply shared native Git defaults to source preparation and fix operations:
  ignore inherited Git redirection/global configuration, disable repository
  fsmonitor/hooks/signing/credential helpers, and retain explicitly supplied
  transport credentials. Source inspection now propagates errors and cancellation;
  failed or missing retained workspaces fail without silently replacing the source.
  Repository filters and filesystem/network isolation remain under review.

- Confine local branch and origin metadata reads, including registered repository
  lookups and linked worktrees, to approved roots without running Git. Existing
  local sources outside the browse allowlist require configuration before upgrade;
  reftable support and broader Git isolation remain open.
- Retain managed scan workspaces while preparation, resumed scans or enrichment
  still use them. Confine cleanup to the opened workspace root; reject nested
  path aliases, invalid retention requests and duration overflow. Report cleanup
  failures and show removed/retained counts in Administration.

- Publish first-run JWT and encryption keys atomically without replacing an
  existing or concurrently created key. Fail startup on key-loading errors,
  preserve existing formats, and make concurrent encryption-key reloads safe.

- Respect the configured scanner runtime during API startup and worker default
  selection. Kubernetes startup no longer probes Docker; explicit worker flags
  still override the environment and Docker diagnostics remain available.
- Confine scan artifact downloads, raw tool output and manifest reads to the
  initialized artifact root, including symlink resolution.

- Restrict automatic GitHub credentials during branch discovery to the exact
  HTTPS GitHub origin; reject token-bearing lookalike or insecure remotes before
  transport creation.
- Install fixer CLIs through a confined, synced temporary file and atomic rename.
  Preserve existing binaries on failed copies and avoid following destination
  symlinks or hardlinks; reject invalid binary sizes and oversized downloads.

- Upgrade Alpine deployment packages before installation and verify the rebuilt
  runtime against the locked vulnerability databases. PostgreSQL readiness probes
  now require TCP so initialization cannot report ready prematurely.
- Update active Go toolchains to 1.27.3 and refresh pinned vulnerability databases.
  Confine workspace digest reads and fail local scan preparation on hashing errors.
- Return the original scan on idempotent retries at Community capacity while
  preserving limits for new scans and conflicts for changed requests.
- Fix PostgreSQL schedule enable/disable writes and enabled-only selection.
  Qualify native and Compose scan/restart flows with both supported databases;
  correct PostgreSQL 18 test storage and disposable fixture ownership.
- Qualify genuine Kubernetes scanner Jobs and restart recovery in a fresh Kind
  cluster; import local OCI images with their correct names and platform.
- Retain current deployment identities, runner build inventory, security review
  ledger and reproducible candidate validation evidence under `docs/validation`.

- Fail remediation on incomplete scanner verification, engine errors, or patch
  application failures while retaining the workspace. Apply API patches once
  per turn before verification. Same-file rollbacks now invalidate every affected
  repair, including earlier uncommitted keeps, and reports reflect final retries.
- Preserve remediation workspaces when diff storage or reopening fails; stop
  publication of unreviewable work and prevent silent replacement on resume.
- Confine Go formatter writes to opened workspace files and require exact or
  unambiguous finding IDs before applying agent decisions or suppressions.
- Prevent failed remediation checkouts from deleting files. Preserve literal
  filenames, confine removal of new files, and clear intent-to-add index entries.
  Rollback and commit failures now stop the job and retain its workspace for
  recovery instead of reporting success or continuing to push.
- Validate GitHub repository coordinates at source parsing, cloning, and cache
  lookup; reuse the same parser for fixer clone URLs and reject extra segments.
- Restrict OCI registry redirects to the configured origin and request method,
  including authenticated retries. Cross-origin CDN redirects fail explicitly;
  registry qualification requirements are documented in the operations runbook.
  Referrer and token responses now reject oversized bodies and trailing JSON.
- Keep authenticated/private/negotiated metadata out of the shared discovery
  cache, honor no-store on revalidation, preserve caller-owned request headers,
  and propagate cancellation. Old cache entries are invalidated safely.
- Scan preparation now stops on unavailable or malformed readiness, deduplicates
  downloads, retries failed images, rechecks readiness, and preserves the request
  and idempotency key when retrying an uncertain scan submission. Non-admins see
  actionable download guidance; reduced-coverage scans require an explicit choice.
- Remove duplicate bulk-download controls from Home and scanner settings. Scope
  Wolf image updates to the displayed outdated Wolf images; recognize local
  builds and private registries with ports, and fix narrow-screen image wrapping.
- Use configured Docker endpoints for local image probes; distinguish absent
  images from daemon failures and verify presence after a pull. Preserve HTTP
  status when the UI receives HTML proxy errors or null error bodies.
- Confine local browse filesystem operations with `os.Root` and check browse and
  GitInfo paths before lookup. Disable external diff/textconv/fsmonitor helpers
  during fix diff collection; include staged edits without duplicating committed
  changes. Broader Git subprocess confinement remains under security review.
- Local scans now exit nonzero for failed/missing tools, zero-tool execution,
  incomplete parsing, cancellation, or canonical report-write errors while
  retaining partial evidence. GoKart raw output is retained, and its finding
  filter no longer drops distinct source/sink expressions on the same line.
- Consolidate execution into `docs/PLAN.md` with stable checkbox task IDs,
  acceptance evidence, and an archive of 18 superseded plans/designs.
- Fix GoKart's pinned-toolchain analysis crashes and request SARIF on stdout.
  Preserve partial findings on fatal exits while reporting failed execution;
  reject missing/malformed reports. Native scanner smoke now exercises unsafe,
  safe, and invalid-source fixtures. Existing scanner images require rebuild
  and qualification before deployment.

## [2.0.0] — TBD

### Breaking

- **Docker is now a hard runtime requirement.** Wolf-slim mounts the
  docker socket to spawn short-lived scanner containers per tool
  invocation. Host-installed scanner binaries are no longer used.
- **Finding fingerprints invalidate.** `FilePath` is now repo-relative
  (e.g. `pkg/foo.go`) instead of absolute (`/home/me/repo/pkg/foo.go`).
  Existing findings keep their old fingerprints and will look "new"
  after the first 2.0 scan. See `docs/MIGRATION_2_0.md` §"Migrating
  fingerprints".
- **Repos must live under `WOLF_REPOS_ROOT`.** Wolf-slim mounts this
  host directory read-only at `/repos` inside its container. The wolf
  CLI accepts paths like `/repos/myproject`.
- **Removed `exec.LookPath`-based scanner availability.** All plugins
  now check `container.IsScannersReady()` against the configured
  scanner image set.

### Added

- **Containerized scanners** — three-tier hybrid:
  - **Upstream-official images** routed per-tool (trivy, grype, syft,
    osv-scanner, gitleaks, trufflehog, hadolint, dockle, checkov, tflint,
    kubescape, kube-linter, semgrep, nuclei, vale, spectral, scancode,
    scorecard). No wolf rebuild burden for these; upstream maintainers
    handle multi-arch builds and CVE responses.
  - **Slim wolf-built default image** `wolf-scanners` (~600–900 MB on
    amd64) carrying the per-language tools that don't have a usable
    upstream image (bandit/ruff/mypy/pip-audit/radon/vulture, gosec/
    staticcheck/govulncheck, eslint, brakeman/rubocop, phpstan, swiftlint,
    cppcheck, shellcheck, detect-secrets, sqlfluff).
  - **Wolf-built bucket images** for heavy toolchains: `wolf-scanners-jvm`
    (~2 GB: infer + pmd + JDK), `wolf-scanners-rust` (~1.2 GB: clippy +
    rust toolchain), `wolf-scanners-codeql` (~700 MB: CodeQL, opt-in).
- **`internal/plugin/container/`** — new shim that translates the
  legacy `plugin.CommandContext` into `docker run --rm` invocations
  with read-only bind mounts, user mapping, network policy, resource
  caps, and per-tool image lookup via `Config.ImageOverrides`.
- **`internal/setup/scanners/`** — `LoadAndInstall`, `Doctor`, `Pull`
  helpers wired into `cmd/wolf/main.go` startup and CLI subcommands.
- **`wolf doctor`** — diagnostics: docker reachable, image present,
  uid/gid mapping, repos-root pairing.
- **`wolf pull scanners`** — pre-pull every configured scanner image.
- **`/api/scanners/config|doctor|pull`** REST endpoints.
- **`/scanners` UI page** — read-only config viewer + Doctor / Pull
  All Images buttons.
- **OpenSSF Scorecard** scanner — repo-hygiene scoring (branch
  protection, signed releases, pinned deps).
- **Renovate** scanner (dry-run / detect-only) — flags outdated and
  vulnerable deps across Helm, GitHub Actions, Dockerfile base images,
  Terraform modules, pre-commit hooks, GitLab CI, Bitbucket pipelines,
  Gradle / Maven / sbt — ecosystems trivy/grype/osv-scanner don't
  cover well. Severity by gap: patch→info, minor→low, major→medium,
  vulnerability→high.
- **8 additional scanners** (36 → 44 total):
  - **KICS** (Checkmarx, MIT) — multi-format IaC SAST.
  - **Conftest** (OPA, Apache-2.0) — policy-as-code.
  - **Pluto** (Fairwinds, Apache-2.0) — deprecated K8s API detection.
  - **detekt** (Apache-2.0) — Kotlin static analyzer.
  - **Bearer** (ELv2) — data-flow / PII / privacy scanner.
  - **markdownlint-cli** (MIT) — Markdown style + structure.
  - **yamllint** (GPL-3.0) — YAML structural + style.
  - **GoKart** (Praetorian, Apache-2.0) — taint-analysis SAST for Go.
- **`Options.EntrypointOverride`** in the shim — lets plugins wrap an
  upstream image's entrypoint with `sh -c` for tools that write
  results to disk (KICS, detekt, markdownlint).
- **CI workflows**: `.github/workflows/scanners-image.yml` (matrix
  build of 4 images with smoke test) and `go.yml` (vet/build/test/
  lint).
- **`docs/MIGRATION_2_0.md`** and **`docs/RELEASE_NOTES_2_0.md`**.

### Changed

- `Dockerfile` (wolf-slim) now installs `docker-cli` and adds the wolf
  user to the docker group.
- `docker-compose.yml` mounts `/var/run/docker.sock` and
  `${WOLF_REPOS_ROOT}:/repos:ro`, threads all `WOLF_SCANNERS_*` env
  vars, declares a `wolf-scanners-db` named volume.
- `configs/wolf.yaml` gained a `scan.container` section with image,
  overrides, pull policy, network mode, memory/CPU caps, db volume,
  path translation.
- All 40 existing plugins migrated to `container.CommandContext`; path
  normalization (`/scan/foo` → `foo`) applied to finding paths.
- README rewritten for the container-only quick-start.

### Restored

- `internal/artifacts/` package (was referenced but missing from a
  prior commit; the build was broken before this release).
- `cmd/wolf/main.go` (was missing from the prior commit; CLI entry
  built from scratch with cobra subcommands).

### Migration

See `docs/MIGRATION_2_0.md` for the full upgrade guide. Short version:

```bash
docker compose down
docker compose pull wolf
mkdir -p ./repos && ln -s /your/project ./repos/your-project
docker compose up -d
docker compose exec wolf wolf pull scanners
docker compose exec wolf wolf doctor
docker compose exec wolf wolf scan --repo /repos/your-project
```
