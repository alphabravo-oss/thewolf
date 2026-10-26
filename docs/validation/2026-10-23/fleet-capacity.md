# Findings read capacity and shared UI — 2026-10-23

## Supported read-load envelope (P04 / D12)

The runnable check enforces these query-layer SLOs on the [recorded host](pagination-host.txt): 16 visible AMD EPYC-Rome CPUs, approximately 32 GiB RAM, Linux, file-backed SQLite and local PostgreSQL 18.6. The fixture has 55,000 retained finding observations in one repository, then another 55,000 across ten repositories (110,000 total). Each finding carries 1 KiB native data, 10% are suppressed, and responses contain 50 rows. Two readers issue 20 requests per operation and fixture size.

- Scan page: warm p95 ≤500 ms; scan statistics: ≤1 second.
- Global page: warm p95 ≤5 seconds at 55k retained observations; ≤10 seconds at 110k.
- Cold database-cache read: ≤2× the corresponding warm p95 limit.
- Sampled peak Go heap: ≤1.5 GiB for the two-reader workload. Sampling is every 5 ms; this is not total host/database RSS.

The test fails when these limits are exceeded or PostgreSQL is missing. This is a supported query workload, not a universal HTTP/network or whole-installation capacity promise. More history, larger records, more concurrent readers, active scanner/fixer load, remote databases and different hardware require deployment qualification. Admission/fairness and long-running operational load remain P07/O tasks.

Cold reads actually reset the database cache: SQLite closes all idle writer/reader connections, and PostgreSQL uses `pg_buffercache_evict_relation` for tables/indexes in the test-owned schema, requiring zero skipped buffers. OS page caches remain warm; no host-wide eviction is performed. Warm reads follow the cold request. [Cold/warm results and enforced assertions](fleet-cold-warm-capacity.txt); [earlier two-reader trace run](fleet-capacity.txt).

| Store | Observations / repos | Operation | Cold DB ms | Warm p50 ms | Warm p95 ms | Peak Go heap MiB | Response KiB |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| sqlite | 55k / 1 | scan_page | 9.05 | 6.28 | 14.31 | 3.5 | 32.1 |
| sqlite | 55k / 1 | scan_stats | 51.95 | 53.27 | 74.53 | 1.5 | 0.2 |
| sqlite | 55k / 1 | fleet_page | 1101.29 | 1316.90 | 1815.54 | 446.2 | 32.1 |
| sqlite | 110k / 11 | scan_page | 3.06 | 3.60 | 5.36 | 7.0 | 31.9 |
| sqlite | 110k / 11 | scan_stats | 8.20 | 7.00 | 12.67 | 1.7 | 0.2 |
| sqlite | 110k / 11 | fleet_page | 2223.55 | 2805.28 | 3095.32 | 876.8 | 31.9 |
| postgres | 55k / 1 | scan_page | 199.88 | 132.03 | 179.14 | 18.0 | 32.8 |
| postgres | 55k / 1 | scan_stats | 52.33 | 54.49 | 60.71 | 2.3 | 0.2 |
| postgres | 55k / 1 | fleet_page | 2461.66 | 2333.27 | 2518.40 | 455.7 | 32.8 |
| postgres | 110k / 11 | scan_page | 15.41 | 6.20 | 7.85 | 12.6 | 32.5 |
| postgres | 110k / 11 | scan_stats | 6.37 | 4.29 | 6.66 | 2.4 | 0.2 |
| postgres | 110k / 11 | fleet_page | 4936.51 | 5370.89 | 5814.74 | 904.4 | 32.6 |

In the 11-repository fixture, scan-specific operations target its newest 5,500-row scan; global pages cover all 110,000 observations. Response sizes include the test wrapper, exclude HTTP headers, and omit native reports. Query paths use four data queries for scan pages, two for scan statistics and eight for global pages, independent of repository count; transaction statements are excluded.

Run against a **disposable PostgreSQL 18 database** with `CREATE EXTENSION pg_buffercache` installed in `public`:

```sh
WOLF_RUN_FINDING_PAGE_LOAD=1 WOLF_TEST_POSTGRES_DSN='postgres://…'   go test -count=1 -v ./internal/db -run '^TestFindingPageLoad$'
```

## Runtime changes

Global history uses shared slice views and pointer/index maps instead of repeatedly copying full finding structures. Eligibility still follows the existing complete/incomplete branch rules, with deterministic ties. Returned data and triage semantics are preserved.

The SQLite driver ignores `TxOptions.ReadOnly`. Disk-backed stores now use a separate query-only pool, limited to four connections, with deferred read transactions. Writes retain immediate transactions. A reader holds a consistent WAL snapshot while another connection writes; attempted writes through the reader fail, and cancellation releases its connection. Private in-memory/temporary stores retain their original single-store behavior. DSN invariants now precede user-supplied values because the driver reads the first option value.

[Read/write snapshot checks](fleet-read-snapshots.txt) and [focused memory/history/ownership checks](fleet-memory-checks.txt) pass. The broader database/API run found two outdated fixtures ([full output](fleet-final-regression.txt)): the old-schema fixture omitted the newly dependent index, and restart fixtures reopened only the writer pool. They now remove the dependent index / replace the complete reopened store, preserving their original assertions. [All affected cluster, baseline, suppression, completion and reader tests pass](fleet-final-corrections.txt); the full API package passed in the broader run.

## Suppression and phase measurements (P06)

Suppression decisions stay within each request's database snapshot and source binding. Related totals/pages/gates reuse the persisted projection; no cross-user/repository or cross-request cache is introduced. Captured rules remain immutable, while durable edits reconcile flags transactionally. Uncaptured live scans retain the matcher-before-pagination fallback.

Optional Go runtime trace regions identify `finding.database`, `finding.suppression` and `finding.normalization`; the load harness separately measures `finding.serialization`. [Phase totals and percentiles](fleet-phase-timings.json) retain 240 serialization samples from the large workload and a separate captured-policy regression. [Large-workload region events](fleet-phase-events.txt) cover history/page work; the [captured-policy events](fleet-captured-phase-events.txt) also include the policy-read regions. These are region timings, not additive HTTP latency: pool/transaction waits and network delivery remain outside the sampled phases. SQL row decoding is included with database retrieval.

To reproduce, add `-trace /tmp/wolf-read.trace` to the load or captured-policy test command, then run `go tool trace -d=parsed /tmp/wolf-read.trace`. [Captured policy, durable-edit, source-binding and ownership checks](fleet-captured-policy-checks.txt) pass on both stores.

## Shared UI (U06; U09 progress)

Product record tables use `DataTable`; the remaining direct tables are the GitHub import picker, SSH discovery picker and fixer attempts summary with its totals footer. These match the plan's explicit exceptions. Severity, scan, fixer, fixability and HTTP-status badges now use the shared status primitive and color scale; role/digest labels keep their distinct non-status semantics.

The Agents page distinguishes settings, engine-readiness and job-list failures from disabled, unconfigured or empty states, with retries scoped to the failed query. [Nine desktop/mobile Chromium and Firefox checks](shared-status-browser.txt) cover error recovery, warning status styling and audit behavior. [UI typecheck](fleet-ui-typecheck.txt), [lint](fleet-ui-lint.txt) and [browser typecheck](fleet-browser-typecheck.txt) pass. This does not close the wider U09 route/state audit.

[Go lint](fleet-go-lint.txt), [scanner source/lock sync](fleet-scanners-sync.txt) and [owned PostgreSQL fixture cleanup](fleet-database-cleanup.txt) are retained. No deployment or publication was performed.
