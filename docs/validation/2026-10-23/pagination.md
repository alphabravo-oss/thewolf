# Findings pagination — 2026-10-23

Follow-up qualification: [cold/warm capacity, SQLite read concurrency and phase measurements](fleet-capacity.md) closes P04/P06 and D12. This record retains the initial measurements and their original limits.

Scan and global findings filter/count/sort in SQL before returning at most 200 full records. Native SARIF is excluded from ordinary pages; explicit `include_sarif=true` remains compatible. Captured scans use persisted scan-time decisions; uncaptured live scans still apply the shared matcher before pagination. Durable suppression changes reconcile those stored decisions.

Global eligibility retains the existing branch/history rules using compact columns. Complete scans replace their branch observation; incomplete scans add evidence. Equal severity/time candidates now have a deterministic ID tie-break. Selected scans are fetched together and validated once per request. No cross-request policy cache was introduced.

Migration 070 adds `idx_findings_scan_visible_severity`; both stores use it for the default scan order. The regression checks index creation and idempotent replay. [EXPLAIN output and final measurements](pagination-load.txt), [pre-index/allocation baseline](pagination-load-before.txt).

Scan, finding and vulnerability count/page queries use repeatable-read transactions. Vulnerabilities support literal search and whitelisted sorting; empty searches no longer trigger unnecessary backfill. Page arithmetic cannot overflow. Scan statistics use full-scope SQL groups. Rule/repository aggregates reuse compact history without fetching native reports.

## Verification

- `WOLF_TEST_POSTGRES_DSN=... go test -race -count=1 ./internal/db ./internal/api/routes` — [both packages pass](pagination-database-api.txt).
- Focused final checks after the history-allocation/index changes: [passing SQLite/PostgreSQL and API results](pagination-final-checks.txt). Covers scan/global pages, history parity, suppression, gates, ownership, filters and tie-breaks.
- `pnpm typecheck`, `pnpm lint`, `pnpm test:e2e:typecheck` — [typecheck](pagination-ui-typecheck.txt), [lint](pagination-ui-lint.txt), [browser typecheck](pagination-browser-typecheck.txt).
- `pnpm exec playwright test e2e/vulnerability-pagination.spec.ts` — [three browser projects pass](pagination-browser.txt). Search finds a record beyond page one; paging, sort and reload preserve the server query.
- OpenAPI query parameters cover scan/global findings and vulnerability search/sort; [spec tests pass](pagination-openapi.txt).
- Audited all eight `serverSide` DataTable call sites: global findings, scan findings, scans, vulnerabilities, audit, repository findings/history and coverage. Supported controls send server parameters; unsupported controls are disabled. None combines server paging with virtualization.

## Load measurements and remaining limits

Runnable fixture: `WOLF_RUN_FINDING_PAGE_LOAD=1 WOLF_TEST_POSTGRES_DSN=... go test -count=1 -v ./internal/db -run '^TestFindingPageLoad$'`.

The fixture measures 55,000 findings in one repository, then adds another 55,000 across ten repositories (110,000 total for global queries), with 10% suppressed and 1 KiB native payload per finding. It records first read, 20 warm samples, p50/p95 latency, allocated bytes and serialized page size for both stores. [Host description](pagination-host.txt).

Normal captured scan pages execute four data queries (scan policy, hidden count, filtered total, page); statistics execute two. Global pages execute eight, including four existing compact-history reads and one batch of scan policies; transaction statements are excluded. These counts follow the call paths, rather than a production tracing sample.

These are local, single-request measurements, not production capacity qualification. First read is not a flushed disk-cache measurement. Compact global history still grows with retained evidence. Production concurrency, peak memory, phase-by-phase accounting and the supported-load SLO remain P04/P06; exports and uncaptured live scans retain their existing full-scope fallback. No deployment or publication was performed.

## Recorded results

| Store | Fleet fixture | Operation | p50 ms | p95 ms | Allocated MiB/request | Response KiB |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| sqlite | 55k / 1 repo | scan_page | 8.56 | 10.09 | 0.2 | 32.1 |
| sqlite | 55k / 1 repo | scan_stats | 46.21 | 63.51 | 0.0 | 0.2 |
| sqlite | 55k / 1 repo | fleet_page | 1060.33 | 1117.68 | 581.6 | 32.1 |
| sqlite | 110k / 11 repos | scan_page | 1.55 | 2.29 | 0.2 | 31.9 |
| sqlite | 110k / 11 repos | scan_stats | 4.47 | 8.37 | 0.0 | 0.2 |
| sqlite | 110k / 11 repos | fleet_page | 2272.50 | 2451.70 | 1164.5 | 31.9 |
| postgres | 55k / 1 repo | scan_page | 35.01 | 40.14 | 0.2 | 32.8 |
| postgres | 55k / 1 repo | scan_stats | 40.57 | 52.06 | 0.0 | 0.2 |
| postgres | 55k / 1 repo | fleet_page | 2122.26 | 2261.58 | 588.1 | 32.8 |
| postgres | 110k / 11 repos | scan_page | 7.92 | 9.45 | 0.2 | 32.6 |
| postgres | 110k / 11 repos | scan_stats | 4.99 | 5.89 | 0.0 | 0.2 |
| postgres | 110k / 11 repos | fleet_page | 4701.09 | 4819.51 | 1176.9 | 32.6 |

For the 11-repository fixture, scan-specific operations target its newest 5,500-finding scan; fleet pages cover all 110,000 findings. Allocated bytes are total allocation during the operation, not peak live heap. Serialization size is the test page wrapper, excluding HTTP headers. The shared host's baseline partially overlapped other Go checks; these are directional regression measurements, not isolated production throughput figures.

The 55k scan-page p95 improved from 59.80 to 10.09 ms on SQLite and from 156.85 to 40.14 ms on PostgreSQL. Global history allocation fell by roughly 40%, but its remaining 55k/110k memory and latency cost is material; P04 stays open.

[Go lint](pagination-go-lint.txt) reports zero issues. [Scanner source/lock sync](pagination-scanners-sync.txt) and [disposable PostgreSQL cleanup](pagination-database-cleanup.txt) are retained.
