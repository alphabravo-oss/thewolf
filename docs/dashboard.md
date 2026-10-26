# Dashboard evidence

Home prioritizes work: high-risk security findings, scan coverage gaps, active scans and fix jobs, then seven-day progress. TanStack Query shares the posture response and refreshes it every 30 seconds. Every issue, coverage exception, job and comparison has a link to its evidence or next action. New or resurfaced findings are the default; all open critical/high findings remain available.

## Metric definitions

- **New / resurfaced high risk:** currently open, unsuppressed critical/high security findings, deduplicated by source location across repository branches. New uses the first recorded observation; resurfaced uses recorded baseline classifications within seven days. This is a triage queue, not a claim that each issue is confirmed exploitable.
- **Fresh coverage:** a repository qualifies only when the latest root scan on every tracked branch completed all selected tools within seven days. Failed, cancelled, incomplete, queued, running, stale and never-scanned states remain visible. Complete describes selected tools and paths; it does not claim exhaustive coverage.
- **Failing gates:** repositories with a recorded failing gate on their latest root attempt on any tracked branch. Old failures no longer survive a later pass. An incomplete or pending latest attempt appears as a coverage gap, never an inferred pass.
- **Work:** all queued/running root scans, with expired worker leases flagged. Fix jobs are scoped to the signed-in user. Paused fixes link to review; succeeded agent jobs still require checking source-branch verification. The dashboard does not infer that an agent's success means its changes were merged. Admins see recent worker heartbeats when queue execution is enabled.
- **Seven-day progress:** compare the latest fresh root scan with the latest complete scan observed at or before the start of the window. The baseline must be no more than 30 days older than the window. Branch, source, remote node, profile, tool set, category/path filters and recorded scanner release must match. Missing baselines, unrecorded releases, changed scope and incomplete coverage are explicitly excluded. With no comparable branches, progress is **unknown**, not zero.
- **New / resurfaced / verified resolved:** distinct security issue signatures using the existing scan-comparison identity. Line movement alone cannot earn resolution credit. A resolved signature must disappear from a complete comparable rescan, have no current observations on another branch, and have no affected branch whose comparison was excluded. An issue still open elsewhere is not new or resurfaced merely because another branch reports it.
- **Suppressed / set aside:** baseline-open signatures still detected but now suppressed or closed by triage. These are separate from verified resolution. A status edit alone never earns resolution credit.

Remediation child scans (`origin_scan_id`) do not replace source-branch evidence. Partial scans preserve previous complete observations and can add findings; they cannot erase a known backlog. The same selection applies to current finding APIs, preserving full finding fields and the `include_suppressed` option.

## API compatibility

`GET /fleet/posture` retains the existing summary fields and adds `coverage`, `changes`, `priorities`, `work` and `generated_at`. `week_over_week_delta` is deprecated; it now represents the deduplicated open-location count change within the comparable cohort. Clients should use `changes` and inspect its comparison coverage instead of rendering a green/red fleet-wide delta.

`GET /findings/trends` and its export now return up to 90 daily UTC snapshots of distinct observed open locations instead of summing repeated scan output. Days without scans carry forward observations; clean complete scans can produce an explicit zero. JSON includes repository counts and incomplete-scan counts. Historical triage is not immutable: snapshots use the records' stored current triage state, so this compatibility series is not an audit-quality historical risk ledger or proof of fixes. Home uses the explicit comparisons instead.

The aggregates use scoped SQL queries shared by SQLite and PostgreSQL. Read failures propagate as errors instead of empty results. The current implementation loads compact scan/finding history rather than issuing a query for every scan; its cost is linear in retained history (the compatibility daily series replays that history). At larger retention volumes, materialize snapshots before adding more historical widgets. No history is silently truncated to manufacture a complete result.

## Collections and repository work

Collections organize repositories. Open a repository for its current findings, or open a scan for the tools, status and evidence from that individual run. The sidebar's **Across collections** section exposes **All scans** and **All findings** for cross-repository triage; these are aggregate views of the same work.

A collection's repository table includes source, default branch, latest root scan, tools completed/failed, latest scan finding count and scan history. Latest scan counts can include partial results; they are not the current open backlog. Never-scanned repositories show an unknown count rather than zero. Child remediation runs do not replace source scans in this table. Scan history follows current repository membership, including API/CLI scans and history from before a move between collections. Read errors have an explicit retry state.

## Checks

- `go test -race ./internal/db ./internal/api/routes -run 'TestFleet|TestFindingTrends|TestCollection'`
- Set `WOLF_TEST_POSTGRES_DSN` to include the PostgreSQL contract test; it creates an isolated schema.
- `pnpm --dir ui test:e2e dashboard-evidence.spec.ts` covers desktop/mobile, Firefox, accessibility, drill-down targets, unknown evidence, retry and admin worker visibility.
- `pnpm --dir ui test:e2e collection-repositories.spec.ts` checks repository/scan context, partial and missing evidence, keyboard sorting, search, scan submission, accessibility and retry.
