# Dashboard evidence

Home is a dashboard: a KPI row, seven charts, the top five high-risk findings ("Act on now"), recent activity (admins) and the collapsed "Are we improving?" comparison. Long lists live on their own pages (Findings, Repositories, Scans, Fixes). Every KPI and chart mark links to a filtered list. Each card loads on its own query, shows a Skeleton while loading and its own error with Retry (it also retries itself every 10 seconds, up to 5 times, for the brief 500s seen after a restart), so one slow endpoint never blanks the page. TanStack Query keys are shared with the list pages (`["fleet","posture",null]`, `["findings","by-repo"]`, `["fix-jobs"]`), and posture refreshes every 30 seconds.

## KPIs and charts

| Card | Data source | Drill-down |
| --- | --- | --- |
| Open critical / high (30-day sparkline, change vs 7 days ago) | `/fleet/posture` counts; sparkline and change are critical + high from `/findings/trends` (same scope as posture) | `/findings?view=list&scope=all&severity=critical,high` |
| New vs resolved this week | `/fleet/posture` `changes` | opens "Are we improving?" |
| Scan coverage % | `/fleet/posture` `coverage` | `/repos` |
| Gates failing | `/fleet/posture` `gates_failing` | `/repos` |
| Fix branches pushed / verified (Enterprise, autofix on) | `/fixes?limit=200` | `/agents?stage=pushed` |
| 1. Observed open findings (daily), 30/90 days | `/findings/trends` | severity → `/findings?view=list&scope=all&severity=…` (the list has no date filter) |
| 2. New vs resolved this week, per comparable branch | `/fleet/posture` `changes.rows` | latest scan `/scans/:id` (table also links the baseline scan) |
| 3. Riskiest repositories (top 10, stacked by severity) | `/findings/by-repo` | `/repos/:id?scope=all&severity=…` |
| 4. Top rules (top 10 security rules/CVEs) | `/findings/aggregate?group_by=rule_id&security_only=1&limit=10` | `/findings?view=rule&q=<rule>` (the By rule view filters on `q`) |
| 5. Age of open critical / high (0–7, 8–30, 31–90, >90 days) | `/fleet/posture` `priorities[].first_seen_at` | `/findings?view=list&scope=all&severity=…&sort=first_seen&order=asc` |
| 6. Scanner health (per-tool success rate) | newest 100 root scans, `/scans?per_page=100&roots=1` | `/coverage` |
| 7. Fix pipeline funnel (Enterprise, autofix on) | `/fixes?limit=200` | `/agents?stage=queued\|started\|verified\|pushed` |

Aggregation is client-side over bounded responses (`components/fleet/dashboard-data.ts`, tested in `dashboard-data.test.ts`). Limits, stated in each card's description:

- Chart 1 is labelled "Observed open findings (daily)": snapshots use each finding's current triage state, so it is not an audit-grade history (owner-accepted label, info tooltip on the card).
- Chart 2 shows the single seven-day comparison window the API computes, per branch. A week-by-week history would need the comparison replayed per window; that is not built.
- Chart 5 uses the posture priorities (all open, unsuppressed critical/high **security** findings) and the earliest scan observing each issue. A missing timestamp counts as oldest.
- Chart 6 counts completed and failed root scans only; success = tool in `tools_completed` and not in `tools_failed`.
- Chart 7 maps fix-job status to the furthest stage reached: queued/claimed → Queued; running (or failed/cancelled after starting) → Started; awaiting review/push, push failed or succeeded → Changes verified; `pushed` → Branch pushed. Pull-request and merge state is not recorded on fix jobs, so the funnel stops at Branch pushed.

Accessibility: every chart has a heading, a one-line meaning, a legend for multi-series charts, and a "Data table" `Section` whose rows carry the drill-down links (the SVG is `aria-hidden`; Recharts' keyboard layer is off so the table is the keyboard path). Fills use `--chart-*` tokens in `globals.css`: severity steps from dark crimson (critical, the most prominent) through red, orange and amber to a neutral slate for info, changing both lightness and hue so critical and high stay distinct (validated for colour-vision deficiency in both themes; every fill is at least 3:1 against the panel, checked in `dashboard-data.test.ts`); new/resolved are an orange/blue pair. Count axes use compact ticks (3K, 60K) so labels fit.

## Metric definitions

- **New / resurfaced high risk:** currently open, unsuppressed critical/high security findings, deduplicated by source location across repository branches. New uses the first recorded observation; resurfaced uses recorded baseline classifications within seven days. This is a triage queue, not a claim that each issue is confirmed exploitable.
- **Fresh coverage:** a repository qualifies only when the latest root scan on every tracked branch completed all selected tools within seven days. Failed, cancelled, incomplete, queued, running, stale and never-scanned states remain visible. Complete describes selected tools and paths; it does not claim exhaustive coverage.
- **Failing gates:** repositories with a recorded failing gate on their latest root attempt on any tracked branch. Old failures no longer survive a later pass. An incomplete or pending latest attempt appears as a coverage gap, never an inferred pass.
- **Work:** all queued/running root scans, with expired worker leases flagged. Fix jobs are scoped to the signed-in user. Paused fixes link to review; succeeded agent jobs still require checking source-branch verification. The dashboard does not infer that an agent's success means its changes were merged. Admins see recent worker heartbeats when queue execution is enabled.
- **Seven-day progress:** compare the latest fresh root scan with the latest complete scan observed at or before the start of the window. The baseline must be no more than 30 days older than the window. Branch, source, remote node, profile, tool set, category/path filters and recorded scanner release must match. Missing baselines, unrecorded releases, changed scope and incomplete coverage are explicitly excluded. With no comparable branches, progress is **unknown**, not zero.
- **New / resurfaced / verified resolved:** distinct security issue signatures using the existing scan-comparison identity. Line movement alone cannot earn resolution credit. A resolved signature must disappear from a complete comparable rescan, have no current observations on another branch, and have no affected branch whose comparison was excluded. An issue still open elsewhere is not new or resurfaced merely because another branch reports it.
- **Suppressed / set aside:** baseline-open signatures still detected but now suppressed or closed by triage. These are separate from verified resolution. A status edit alone never earns resolution credit.

Remediation scans are not backlog evidence: child scans (`origin_scan_id`) and root scans of Wolf's own `wolf-fix/*` branches are left out of current findings, posture, coverage, comparisons and trends, so they never replace or inflate source-branch evidence. Partial scans preserve previous complete observations and can add findings; they cannot erase a known backlog. The same selection applies to current finding APIs, preserving full finding fields and the `include_suppressed` option.

## API compatibility

`GET /fleet/posture` retains the existing summary fields and adds `coverage`, `changes`, `priorities`, `work` and `generated_at`. Each priority carries `first_seen_at` (earliest scan observing the issue). `GET /findings` accepts `sort=first_seen` (first observation, falling back to the record's creation time). `week_over_week_delta` is deprecated; it now represents the deduplicated open-location count change within the comparable cohort. Clients should use `changes` and inspect its comparison coverage instead of rendering a green/red fleet-wide delta.

`GET /findings/trends` and its export now return up to 90 daily UTC snapshots of distinct observed open locations instead of summing repeated scan output. Days without scans carry forward observations; clean complete scans can produce an explicit zero. JSON includes repository counts and incomplete-scan counts. Historical triage is not immutable: snapshots use the records' stored current triage state, so this compatibility series is not an audit-quality historical risk ledger or proof of fixes. Home charts it only under the "Observed open findings (daily)" label; progress claims use the explicit comparisons.

The aggregates use scoped SQL queries shared by SQLite and PostgreSQL. Read failures propagate as errors instead of empty results. The current implementation loads compact scan/finding history rather than issuing a query for every scan; its cost is linear in retained history (the compatibility daily series replays that history). At larger retention volumes, materialize snapshots before adding more historical widgets. No history is silently truncated to manufacture a complete result.

`GET /fleet/report?collection_id=` is the Enterprise "Security posture" report in one JSON document: open counts by severity, the last 30 daily trend snapshots, the seven-day comparison and coverage summaries (without per-branch rows), gates failing, pushed fix branches for repositories in scope, open critical/high older than 30/60/90 days, and the 10 riskiest repositories. It uses the same scoped store aggregates as Home. Enterprise saved snapshots store this document unchanged, and the overlay reads it through `pkg/control.FleetAPI()`.

## Collections and repository work

Collections organize repositories. Open a repository for its current findings, or open a scan for the tools, status and evidence from that individual run. The sidebar's **Across collections** section exposes **All scans** and **All findings** for cross-repository triage; these are aggregate views of the same work.

A collection's repository table includes source, default branch, latest root scan, tools completed/failed, latest scan finding count and scan history. Latest scan counts can include partial results; they are not the current open backlog. Never-scanned repositories show an unknown count rather than zero. Child remediation runs do not replace source scans in this table. Scan history follows current repository membership, including API/CLI scans and history from before a move between collections. Read errors have an explicit retry state.

## Checks

- `go test -race ./internal/db ./internal/api/routes -run 'TestFleet|TestFindingTrends|TestCollection'`
- Set `WOLF_TEST_POSTGRES_DSN` to include the PostgreSQL contract test; it creates an isolated schema.
- `pnpm --dir ui test:e2e dashboard-evidence.spec.ts` covers desktop/mobile, Firefox, accessibility, every chart rendering from `e2e/visual-fixtures.ts`, chart-click and keyboard drill-down URLs, unknown comparisons, Community hiding the funnel, per-card error and retry, and non-admin audit visibility.
- `pnpm --dir ui test src/components/fleet` covers age buckets, the fix funnel, weekly new/resolved, scanner health, top repositories and trend windows.
- `go test ./internal/db -run 'TestFleetEvidenceProgressAndCoverage|TestScanFindingPages'` covers `first_seen_at` and `sort=first_seen`.
- `pnpm --dir ui test:e2e collection-repositories.spec.ts` checks repository/scan context, partial and missing evidence, keyboard sorting, search, scan submission, accessibility and retry.
