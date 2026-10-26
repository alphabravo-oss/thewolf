# Community interface parity

Community actions must be available through UI, REST, CLI, webhook, pipeline,
and MCP (when enabled). MCP is default **off**.

| Action | UI | REST | CLI | Webhook | Pipeline | MCP |
|---|---|---|---|---|---|---|
| Start scan | yes | `POST /scans` | `wolf scan create` | GitHub inbound | Actions snippet | `wolf.scans.create` |
| List scans | yes | `GET /scans` | `wolf scan list` | — | — | `wolf.scans.list` |
| Gate | yes | `GET /scans/{id}/gate` | `wolf scan gate --fail-exit-code` | outbound `scan.completed` | exit 5 | `wolf.scans.gate` |
| Measured test coverage | Repository → Tests | `GET /scans/{id}/test-coverage` | `wolf scan test-coverage` | — | report evidence | `wolf.scans.test-coverage` |
| Import test coverage | Repository → Tests | `PUT /scans/{id}/test-coverage` | `wolf scan import-coverage` | — | CI report upload | `wolf.scans.import-coverage` |
| Coverage by file | Repository → Tests | `GET /scans/{id}/test-coverage/files` | `wolf scan coverage-files` | — | paginated JSON | `wolf.scans.coverage-files` |
| Coverage policies | Repository → Tests | `/policies` | `wolf policy list/create/update` | — | coverage gate | `wolf.policies.list/create/update` |
| List findings | yes | `GET /findings` | `wolf finding list` | — | — | `wolf.findings.list` |
| List vulnerabilities | yes | `GET /vulnerabilities` | `wolf vulnerability list` | — | — | `wolf.vulnerabilities.list` |
| List repos | yes | `GET /repos` | `wolf repo list` | — | — | `wolf.repos.list` |
| Retry scan | yes | `POST /scans/{id}/retry` | `wolf scan retry` | — | — | `wolf.scans.retry` |
| Suppress | yes | `POST /suppressions` | `wolf suppress` | — | — | no |
| Edition status | Settings | `GET /edition` | `wolf system edition` | — | — | `wolf.edition.get` |
| Scanner coverage | `/coverage` | `GET /coverage` | `wolf system coverage` | — | — | `wolf.coverage.get` |
| License | Settings | `GET /license` | `wolf system license` | — | — | `wolf.license.get` |
| Evidence | Vulnerability page | `GET /evidence` | `wolf system evidence` / `wolf vulnerability evidence` | — | — | `wolf.evidence.list` |
| MCP | — | `POST /mcp` | `wolf mcp` (stdio) | — | — | session |
| Webhook events | Settings | `GET /webhooks/events` | `wolf system webhook-events` | outbound | — | no |
| Scan profiles | Start dialog | `GET /scan-profiles` | `wolf system scan-profiles` | — | `--profile` | `wolf.scan-profiles.get` |

MCP uses the official Go SDK's stateless Streamable HTTP transport at
`POST /api/v1/mcp`, with the same bearer/session authentication as
REST. Set `WOLF_MCP_ENABLED=1`; it remains off by default. `wolf mcp` bridges
stdio clients to that endpoint and negotiates the protocol version.

Tools and resources dispatch through the real REST router, retaining endpoint
scopes, ownership, validation, audit and durable job handling. Readable resources
return MCP `contents`; tools return protocol content blocks. Initialization
notifications do not produce unsolicited JSON-RPC replies.

Scan operations include preflight, create, get, result, cancel, retry and gate.
`wolf.scans.create` requires `body` and `idempotency_key`; retries with the same
key and payload replay the durable operation. Tools accept `id` for resources; retry also requires `idempotency_key`.
Use a string-valued `query` object for REST filters. Fix tools expose create,
list, get and diff. `wolf.fixes.create` requires a `body` and nonempty
`idempotency_key`, fixing enabled, `write:fixes`, and ownership of the selected
repository/scan/findings. Its default `dry_run` retains a review branch; explicit
`mode: push` also authorizes publishing the branch and opening a GitHub PR. MCP OAuth
client registration and a general-purpose event subscription transport are not
implemented by this adapter.

## Dependency finding identity

New SCA findings use identity version 2: package name, installed version,
ecosystem and scanner-supplied PURL, plus advisory and repository-relative
location. Fine category, source module enrichment and display wording do not
identify a known dependency. Explicit advisory aliases prefer CVE, then GHSA;
the original scanner rule remains available as evidence. Unknown package or
advisory data retains tool/display distinctions instead of asserting agreement.

REST findings and JSON reports expose `package_name`, `package_version`,
`package_ecosystem`, `package_url` and `advisory_id`. SARIF stores the corresponding
camelCase properties and restores them on import. The UI and remote CLI consume
these REST rows; local CLI reports use the same JSON/SARIF generators. Grype
locations remain separate occurrences; npm and pip audit report the manifest
actually selected for execution.

Migration 061 adds these fields on SQLite and PostgreSQL without rewriting
historical IDs or v1 fingerprints. Historical rows have empty package metadata;
a fresh scan can therefore produce different v2 identities. Do not treat that
boundary as proof of a newly introduced vulnerability. Legacy backfill and
baseline continuity qualification remain open in D19 and D04. Native scanner
execution qualification is separate from the parser fixtures in D01.

PURLs are preserved as supplied, not fully canonicalized. Missing or differently
encoded PURLs, incomplete installed-version data (including npm audit) and
unlinked advisory IDs can conservatively remain separate. This change does not
claim complete cross-tool alias resolution or final persisted-cluster
reconciliation (D02). [D01 evidence](validation/2026-10-23/sca-identity/README.md).

## Persisted finding clusters

Newly finalized scans persist one primary finding per cluster. Migration 062 adds
`cluster_id`: empty identifies a legacy/provisional row; a primary points to its
own ID, and each member points to that primary. Finalization commits membership
and primary metadata together under the execution lease. It validates scan and
repository scope and rejects a stale primary that would hide open evidence.
Existing finding IDs and fix-item references remain valid; no member is deleted
by clustering.

Scan/repository/fleet lists, scan statistics, baselines, fix selection and combined
reports read primaries. `GET /findings/{id}` still resolves an original member ID
and includes its `cluster_id` and authorized `evidence` members. Tool filters and
totals include all persisted contributors; tool facets overlap and do not sum to
the number of unique issues. Each tool's parsed JSON artifact retains its native
rows, while combined JSON/SARIF reports contain primaries and cluster provenance.
SARIF import does not trust exported cluster IDs as membership in a new scan.

Enrichment finishes before scoring and report generation. Reports reload final
database values, including PostgreSQL score precision. Triage through a member
ID updates the entire cluster and serializes against finalization. Recovery
reads all evidence and clears provisional membership before resetting an
incomplete tool, preventing retained members from pointing at a removed primary.
Finding and canonical-write failures fail the scan instead of reporting success.

Priority scores retain the severity weight, path weight and actual AI context
alongside the computed result. Without an AI assessment, `ai_context_score` stays
zero; scoring uses a neutral multiplier of one. Nonfinite or out-of-range AI scores
(outside 0–10) are discarded as unavailable before persistence. The formula is severity weight
× path weight × context multiplier / 3, capped at 100, with dependency boosts of
1.1/1.3/1.5 for at least 2/5/10 dependents. Severity weights are 10/8/5/2/1 for
critical/high/medium/low/info. Path weights are 3 for security-sensitive segments,
2 for application layers, 0.5 for vendor/generated/test fixtures, otherwise 1;
the first matching group wins. Matching uses complete path segments and file
stems, so `author.go` and `myapi/routes_backup` do not gain an auth/API boost.
Existing historical scores are retained until a supported rescore/regroup.

`FIX-HIGH.md` includes every unsuppressed high/critical issue. Missing knowledge
entries appear under Uncategorized with scanner guidance rather than disappearing.
Dependency, CodeQL, Checkov and secret-scanner mappings reuse the existing curated
strategies; these documents are deterministic guidance, not verified patches.

Upgrade API and workers together. Existing completed scans are not backfilled;
their rows remain provisional until an explicit backfill policy is qualified
under D19, or a fresh scan produces clusters. This adds database work and includes
mapping latency in completion; large-repository performance remains a separate
gate. Membership/primary writes and canonical reconciliation are individually
transactional. Whole-scan completion and baseline/artifact coordination remain
open under D02. [Membership evidence](validation/2026-10-23/finding-clusters/README.md).

Manual vulnerability merge/split now commits canonical evidence, finding cluster
links, contributor lists and canonical metadata together. Split requests must
select a nonempty proper subset of the source's finding IDs; unknown or duplicate
IDs and malformed/trailing JSON are rejected. Any affected nonterminal scan
returns 409. Cross-repository changes remain forbidden, and cross-scan merges
keep separate finding primaries in each scan. Original finding IDs, native
scanner fields and triage are preserved. A suppressed critical member cannot
replace visible open evidence as the primary.

Manual evidence records carry an explicit reason. Manual association clears
inherited confidence instead of claiming scanner agreement, retains contributor
names, and recalculates each new primary's deterministic score from its own
stored inputs (without inventing missing enrichment). Replaying canonical persistence honors
these decisions and cannot recreate a deleted merge source or overwrite a split.
Canonical reconciliation and manual changes serialize per repository, then lock
affected scans. Evidence read failures return errors rather than empty success.
[Manual change evidence](validation/2026-10-23/manual-clusters/README.md).

Manual decisions apply to existing observations, not a persistent rule for future
scans. Live finding lists/statistics see the changed primaries. Manual edits also
refresh stored scan counts using the shared suppression matcher available at edit
time. Saved artifact files and baseline state still require reconciliation;
whole-scan completion and recovery windows remain D02 work. No new corroboration
UI is qualified until D01–D08 pass.

### Consistent finding snapshots

Migration 063 adds `findings_revision` to scans and quality gate results. Finding
writes, regrouping, triage, finalization and recovery update the revision in their
transactions. Existing gate rows receive revision -1 and must be reevaluated.
API/workers must be upgraded together; mixed-version writes are not qualified.

Compact scan results calculate counts and gates from one database snapshot.
Gate writes compare that snapshot's revision under a scan lock; a concurrent
change returns HTTP 409 (`findings_changed`) and the caller can retry. Cached gate
reads exclude obsolete finding revisions. Policy lookup/parsing and snapshot/run
read failures return errors, rather than a fallback policy or empty success.

On-demand Markdown/SARIF uses the same snapshot and suppression matcher as the
finding API, and returns `X-Wolf-Findings-Revision`. Gate response counts exclude
suppressed findings; SARIF retains suppressed evidence with suppression metadata.
[Verification](validation/2026-10-23/cluster-views/README.md).

The revision does not version external ignore files, policies or measured coverage.
Current requests reevaluate them; their complete cache/version contracts remain
D05/D22. Saved artifact files retain their historical input revisions after manual
edits. Baseline/first-seen and whole-scan completion remain D02 work.

### Recorded artifact snapshots

Migration 064 adds nullable `findings_revision` to artifact records. Each new
record owns a unique confined path and the checksum/size of its published bytes.
Gates, diffs, report bundles, parsed tool output, logs and imported SARIF use the
shared publisher. Publication errors fail the request or scan rather than silently
advertising missing output. Late failure updates preserve newer finding state.

Downloads prefer portable storage keys and verify recorded checksums before
sending bytes. Corruption returns 409 `artifact_integrity`.
`X-Wolf-Artifact-Integrity` distinguishes verified content from legacy rows without
a checksum; `X-Wolf-Findings-Revision` identifies the input revision when known.
Raw outputs and legacy rows may have an unknown revision. Old artifact URLs still
return historical bytes. A saved manifest with an obsolete known revision causes
the manifest endpoint to generate a current one; its original artifact download
remains historical. Legacy unversioned manifests make no current-revision claim.

The common CLI/API report writer returns all output errors and counts persisted
and path-based suppressions consistently. Filesystem publication and DB recording
are separate operations: failed attempts may leave partial bundles or unreferenced
files. Complete retry/retention/lease coordination, large-file streaming, raw
producer failure handling and platform durability remain D02/D24 gates.
[Verification and limits](validation/2026-10-23/artifact-snapshots/README.md).


### Persisted baseline observations

Migration 065 adds scan `baseline_scan_id` / `baseline_resolved_at` and finding
`first_seen_at` / `last_seen_at`. Legacy dates remain nullable. Finalization pins
its chosen baseline and observation cutoff and writes primary/member state in one
transaction. Retry uses that pinned selection. Enrichment/progress updates cannot
reset the baseline metadata; triage remains separate.

A saved `auto` branch baseline defines the backlog, with latest eligible prior
scan fallback only when none is saved. History is branch/source scoped without
the old 20-scan cutoff. First introduction survives resurfacing; evidence absent
from the previous eligible observation is resurfaced. Other findings absent from
the saved backlog remain new. Named baselines are explicit comparison inputs;
creating one does not implicitly select it.

Baseline creation requires matching owner/repository/branch, completion time,
completed requested tools and no recorded failure/cancellation. Unknown or malformed
execution metadata is not proof of a successful baseline. API validation returns
400, duplicate names 409, and persistence errors 500. Native baseline failures fail
the scan; SARIF import derives state from local history rather than uploaded state.
JSON/SARIF export persisted state, introduction/resolution IDs and observation dates.
Report/manifest JSON includes the chosen baseline ID when known.

Fixed/resolved transitions, canonical timelines and retention/backfill, strategy
selection and scope/coverage qualification remain open. Manual regrouping is
qualified below for current observations.
Terminal database publication is qualified below; filesystem bundle publication
remains separate. [Verification and limits](validation/2026-10-23/baseline-state/README.md).


### Manual baseline reconciliation

Manual merge/split rederives affected observation history inside the membership
transaction before refreshing canonical metadata, counts and finding revisions.
Migration 066 adds an internal identity binding for derived history. Dates are
reused only for the identity that supplied them, preventing a split or a later
independent finding from inheriting an unrelated merged issue's dates.

For scans with a pinned baseline selection, merge/split keeps that selection and
cutoff. For legacy/provisional scans without one, regrouped issues have unsupported
derived history cleared to unknown; unrelated findings retain their state. The
migration itself preserves existing dates and leaves their binding unknown.

Baseline-filtered gate rules do not match empty state. When an otherwise relevant
rule needs that state, the gate reports `incomplete` unless a known failure already
establishes `fail`. Suppressed or otherwise irrelevant findings do not cause this
incompleteness. Generated SARIF carries the same supported state/dates as persisted
findings and uses the current finding revision; original downloads stay historical.

Canonical timelines, fixed/resolved absence proof, deleted-history reconstruction,
automatic future membership policies, full UI parity and whole-scan recovery remain
open. [Verification and limits](validation/2026-10-23/manual-baseline/README.md).


### Terminal scan publication

Native scans and SARIF imports now use one database completion transaction for
terminal status, canonical vulnerability/evidence reconciliation, the final gate
and its artifact reference, and the optional first-success baseline. Imports commit
their checksum claim there too. Lease/cancellation, owner/branch and finding revision
must still match. Required report files are written first; the final gate file has
no downloadable database reference until the transaction commits.

Migration 067 adds gate `scan_status`. Gate writes and cache reads require both the
same execution status and finding revision. Unbound legacy gates require reevaluation.
A gate evaluated while running cannot overwrite a terminal evaluation. The current
gate row uses the current evaluation's ID; historical artifact snapshots keep the
identity and bytes recorded at their publication time.

Failed imports remain failed rather than appearing completed, and unsuccessful
publication does not reserve the checksum. An identical upload can retry. An already
successful duplicate still returns 409, with no additional completed scan. Clean
SARIF runs retain their tool names even when they contain no findings. Run-record
persistence failures propagate. API/workers must be upgraded together for the new
gate binding and completion contract.

Raw findings and non-gate artifacts remain provisional during execution. Partial
bundle/orphan cleanup, the broader process-kill/deployment matrix, native run-metadata
and source/coverage evidence, and the broader
baseline/import compatibility criteria remain open.
[Verification and limits](validation/2026-10-23/scan-completion/README.md).


### Publication retries and terminal recovery

Native scans and SARIF imports share the same final snapshot/report/gate publisher.
A finding-revision conflict rerenders the bundle from fresh persisted findings and
retries the terminal commit. Successful scanners and their evidence are not rerun
or reinserted. Saved historical artifact bytes remain unchanged.

The publisher allows three attempts. Continuous conflicts fail explicitly with
retained evidence. Cancellation, lease loss, policy errors and publication failures
stop immediately; a lost executor cannot overwrite the new owner or cancellation.
Original SARIF bytes use the existing confined immutable publisher directly.

Four executor-process kill checks cover SQLite/PostgreSQL immediately before and
after commit. A pre-commit kill permits lease reclaim and recovery with the original
finding IDs, history, scores and triage; completed tools are skipped. A post-commit
kill leaves a completed scan that cannot be reclaimed. These checks use controlled
finding plugins and do not qualify deployed scanner images or worker services.

Durable retries under sustained contention, policy/coverage/ignore-file versioning,
other crash boundaries, retention and deployment/power-loss behavior remain open.
[Verification and limits](validation/2026-10-23/completion-retry/README.md).


### Lockfile suppression

Built-in lockfile rules suppress SAST/secret noise only. SCA findings in Cargo.lock,
package-lock.json and go.sum remain visible and count toward the security gate.
License, container and unknown categories are not hidden by the lockfile rule.
Rules use explicit analysis categories; a custom reason label cannot change their
matching behavior. Explicit repository rules and other independent defaults still
apply, and `WOLF_DISABLE_DEFAULT_SUPPRESSIONS=1` disables built-in rules.

Finding detail, cluster evidence and status-update responses now apply the existing
shared path matcher, matching list visibility. JSON/SARIF retain all audit results
with suppression markers. FIX-ALL retains suppressed rows in its appendix;
FIX-HIGH also retains uncategorized high-priority findings. Immutable saved reports remain historical.
Completed scans retain scan-time ignore decisions; later file edits affect new scans.
Durable Wolf rules remain editable, with their lifecycle described below. [Verification and limits](validation/2026-10-23/lockfile-suppression/README.md).


### Durable suppression lifecycle

Creating or revoking a server suppression now commits its audit entry, affected
finding flags, scan counts/revisions and existing canonical suppression projection
in the same database transaction. Failures return an error and leave no partial
rule change. Revocation records the caller and is idempotent. Historical saved
artifacts remain unchanged; live gates/manifests recompute after the revision changes.

The server's existing schedule loop expires due rules at startup and once per
minute. Each repository expires atomically; a 30-second job timeout or database
failure is logged and retried on the next poll. Flags controlled by an expired rule
fall back to another active matching rule, otherwise clear. Independent/imported
suppression decisions remain. New native/import observations read current rules
inside the insert transaction, and stale enrichment cannot undo a durable rule.

Preview uses only the latest scan in the requested branch, or the repository's
latest scan if no branch is supplied. It returns `scan_id` and `branch` with its
count/findings; an empty newest scan does not fall back to history. The durable rule
still applies to historical matching observations and future observations. Branch
rules require a known exact branch, and repository scope is checked by the matcher.
Past expiry timestamps are rejected. `.wolfignore` negation and the API/CLI
`package_advisory` scope remain unsupported; misleading help/comments were removed.

D05.2–D05.4 qualify partial-cluster rules, `.wolfignore` errors and native Git ignore
evaluation. Captured scan policies bind their rules and Git path decisions to the scan source; reads require no mutable checkout. The expiry polling interval is not a guarantee of
immediate visibility at the exact expiration instant or during a database outage.
[Verification and limits](validation/2026-10-23/suppression-lifecycle/README.md).


### Partial-cluster suppression

A durable rule can match one scanner observation without hiding another scanner's
unsuppressed observation in the same persisted cluster. Rule creation, revocation
and expiry now select the representative with the same open/unsuppressed preference
as native deduplication. Existing representatives win equal ties. When a better
representative is needed, its original finding ID becomes the primary; old finding
URLs and fix references still resolve, and canonical issue IDs/evidence stay intact.

Promoted scores use that member's own stored inputs. Native cluster confidence and
contributors are retained; manual groups do not acquire synthetic confidence.
Pinned baseline history is rederived in the same transaction. For unpinned legacy
clusters, only affected unverifiable derived history clears. Existing canonical
metadata follows the selected representative, while reconciliation preserves its
published canonical key instead of creating a replacement issue.

Suppression preview keeps persisted groups, including manual ones. `count` and
`findings` describe matched current issues, `matched_evidence_count` counts matched
native observations, and `hidden_count` counts currently visible issues that would
become fully suppressed. Matching only a non-primary observation is included in the
preview. The confirmation distinguishes matched from newly hidden issues, and
success reports that the suppression was saved.

Captured ignore policies retain their source binding. Fleet capacity is qualified separately under P04. Saved report bytes remain historical, and a changing observation
between preview and creation can change the result. [Verification and limits](validation/2026-10-23/cluster-suppression/README.md).


### Invalid repository suppression rules

Shared `.wolfignore` parsing rejects malformed or unsupported directives and
read/type/confinement errors before mutating a finding projection. The CLI/report
writer, current finding/detail/statistics/result views, preview, live reports,
gates and manifest validation propagate errors. Known-invalid rules are checked
before single-finding triage on uncaptured live scans. Captured scans use their stored policy.
Durable rule create/revoke/expiry rolls back flags, revisions, canonical data and
audit when a required path projection fails. Correcting the file allows retry,
including identical SARIF import after failed publication. Preview evaluates its
before/after projections with one parsed ruleset.

The scan statistics API counts visible primaries and reporting tools with the same
matcher as the list; the database audit aggregation remains unchanged. Recorded
artifact downloads remain historical. `.wolfignore` negation is explicitly rejected,
not silently skipped. D05.4 qualifies Git ignore failure handling. Captured policies remain usable after source cleanup; source mutation while preparing/capturing a scan remains a separate source-integrity concern.


### Git ignore evaluation

The shared Git boundary handles literal NUL-delimited paths, native tracked/nested/
negation behavior, explicit source discovery, cancellation and a ten-second command
ceiling. Host/global excludes and Git environment redirection are disabled. Metadata
resolution reuses existing linked-worktree/common-directory readers; relevant policy
files are validated before native matching. Failures or diagnostics yield no partial
projection. Native finalization, CLI/report publication, database transactions and
API consumers pass their operation context. Recorded manifest responses validate
actual candidate paths so native evaluation errors cannot bypass current checks.

Capture stores native Git decisions for observed paths together with the effective Wolf rules. Completed scans do not reread later ignore-file edits or require the checkout to remain present. Uncaptured live scans still use the native matcher. Native configuration/filesystem confinement remains part of the broader R03 review.


### Per-operation suppression projection and live manifests

Gate evaluation now consumes its caller's projected findings. Scan result totals
and gates share that projection; publication's gate uses the same decisions already
written to its report bundle. Finding detail projects its requested observation and
evidence together. These paths do not reread mutable ignore rules between related
parts of a response.

The live manifest endpoint refreshes counts/revision/baseline from one projected DB
snapshot. Uncaptured live scans may reflect file edits without a revision change; captured scans retain their recorded rules. Refresh
preserves producer metadata, including unknown top-level fields and JSON number
precision; saved raw counts and original artifact downloads remain historical. An
unchanged manifest can retain its bytes. Invalid/mismatched manifest content fails
rather than returning another scan's data. Live responses are private/no-store.
Fallback manifests reuse the same snapshot instead of loading/projecting it again.

Completed/captured scans retain their policy snapshot and source binding. Durable rule edits advance the persisted visibility/revision; related counts, gates and findings share one projection. Only uncaptured live scans read mutable ignore files. No cross-user or cross-request policy cache is introduced.

## Audit history

`/audit` is the canonical administrator table; `/settings?tab=audit` redirects there.
REST `GET /api/v1/audit-log` filters the full history before paging, using `q`,
`method`, `category`, `severity`, `event`, `actor` (user or token ID), and `ip`.
`from` is inclusive and `before` exclusive; both accept UTC dates or RFC3339 times.
`page`/`per_page` control pagination (default 25, maximum 1000), with `sort=time|status`
and `order=asc|desc`; stable ID tie-breaks distinguish equal timestamps. The UI uses
50-row pages and preserves its filters in the URL. Classification fields can be
empty on legacy entries. Search matches paths, actions, methods, events, actors and
IP addresses. Access remains restricted by administrator scope and role.

## Scanner normalization

Detect-secrets findings with a valid native hash use version 3 identities based on
that hash and detector type. Added lines and reordered output do not change the
identity; separate hashes remain separate issues, and repeated locations remain
cluster evidence. Missing/malformed hashes fall back to source location. Stored
legacy identities are retained; historical backfill and cross-tool secret
correlation remain separate work under D19/X01.

OSV uses matching affected-package CVSS when provided, otherwise advisory CVSS,
then database severity. CVSS 2/3/4 vectors and numeric scores are supported using
`github.com/pandatix/go-cvss` (MIT), avoiding a home-grown scoring algorithm.
Npm uses each advisory's severity before the package aggregate, with nonempty
CVE/URL/source/title/package rule-ID fallbacks. Pip-audit uses severity/CVSS when
provided by enriched output; its native JSON currently omits these fields.
If no valid severity is supplied, compatibility defaults remain High for OSV/npm
and Medium for pip-audit. Every finding explicitly identifies this as Wolf's
fallback, not an upstream assessment. Original selected vectors/ratings and the
normalization explanation travel in descriptions through persistence and export;
native JSON is also retained through the existing raw-artifact mechanism.

Common normalization preserves repository-relative slash paths, canonical CWE
numbers and case-insensitive language extensions (`.C` remains C++). Category
fallbacks match namespace/family tokens instead of incidental substrings.
Repeated findings from one tool retain low confidence; duplicate occurrences do
not constitute independent corroboration.

## Live scan execution

Scan detail and compact results expose `attempt`, `failure_code`,
`failure_message`, `completeness` (pending/complete/incomplete),
`completion_reason`, and `terminal_reason` (empty before terminal state).
`scanner_scopes` retains each tool's requested/effective selectors and explanation.
Completeness reuses the quality gate's execution checks; it does not mean that
security policy passed. Zero successful scanners remains incomplete.

Inline and queued scans use the same durable event endpoint. Native EventSource
reconnect retains Last-Event-ID; keepalives prevent idle buffering, and terminal
snapshots cover workers that stopped without emitting a final event. Detail uses
polling while disconnected, displays early findings, and keeps the most recent
200 received tool-output lines (2 KiB per displayed line). Durable output quotas
still apply; full tool artifacts remain the authoritative output. The `/live`
route remains a redirect to the same detail view.

Runtime capabilities include worker availability for queued execution, matching
the selected backend and ready/busy heartbeats from the past minute. Free slots,
busy/offline/unknown states and the last active heartbeat drive a shared list and
detail notice. Submission warns when no worker is available; creating a queued
scan remains permitted. Inline execution needs no separate worker. This signal
is not a check of source credentials, images, node capacity or artifact storage.

### Bounded findings and vulnerability pages

Findings lists filter and sort before applying `page` / `per_page` (default 50, maximum 200), with an ID tie-break. Native SARIF is omitted unless `include_sarif=true`. Scan totals and severity/tool/category statistics share persisted scan-time suppression decisions; durable Wolf suppression edits still update visibility. Uncaptured live scans evaluate current rules before paging.

Vulnerability lists accept literal `q`, `repo_id`, `scan_id`, `sort=composite_score|severity|title|evidence|baseline` and `order=asc|desc`. The UI keeps search, sort and page in the URL. Counts and rows use one database snapshot. Global findings preserve branch/history eligibility before SQL pagination; compact history is still proportional to retained scan evidence. [Checks and measured limits](validation/2026-10-23/pagination.md).

Disk-backed SQLite now uses a bounded query-only pool for read snapshots, so list reads do not reserve the writer lock. In-memory/private temporary stores retain their existing connection behavior. [Supported two-reader load, cold/warm database-cache measurements and SLOs](validation/2026-10-23/fleet-capacity.md) cover up to 110k retained observations on the recorded host; larger operational workloads need separate qualification.


### Fix publication

Fix jobs default to `dry_run`: a local branch and diff for review. Explicit
`mode: push` (CLI `--mode push`) or a paused job's `action: push` publishes the
reviewed branch and opens or recovers its GitHub pull request. The UI names both
consequences. A generic failed verification cannot use the publication retry path.

Before publishing, the fixer checks its GitHub CLI and the repository owner's
configured token and push permission. GitHub remains authoritative for actual
branch protection and pull-request write permission; a rejection retains the
branch for retry. The job summary records `publication_error` as
`publication_not_ready`, `push_failed`, or `pr_create_failed`, and stores a
successful `pr_url`. PR-only retries do not push the branch again.

The PR includes job/scan identity, finding/tool/rule/location, and recorded
per-finding validation stages. Skipped tests and absent historical evidence are
explicit. The saved review summary supplies these details on retry; later finding
edits do not rewrite that evidence. Large finding lists are abbreviated in the PR
with the complete list retained in the Wolf job summary. This does not certify a
whole-branch verifier or a real production PR journey (PLAN F07.6/F15).


### Retrying fix creation

`POST /fixes` accepts `Idempotency-Key` (up to 255 characters). Reuse the same key
and request after a lost response: the server returns the original job with HTTP
200, including its original configuration, selected findings and branch. New jobs
return 201. Reusing a key with changed intent, including changing `dry_run` to
`push`, returns 409. Keys are scoped to the caller and remain durable with the job.
Legacy REST clients may omit the key; those requests do not have a replay guarantee.

`wolf fix create` and `wolf agent create` accept `--idempotency-key` and generate a
key if omitted. Supply a stable key in scripts that retry across command invocations.
The two UI fix actions retain their key across failed responses while mounted;
a changed request gets a new key. Browser reload does not preserve the pending UI
key. MCP fix creation requires a key.

Job creation, remediation attachment and superseding an older review job commit
in one SQLite/PostgreSQL transaction. A failed enqueue leaves no new remediation.
Explicit finding selections do not expand to a child scan. Whole-scan remediation
may use the latest completed child of the existing branch session. Custom target
branches are honored for a new session; changing an existing session's branch
returns a conflict. `main`, `master`, and the repository default branch are rejected at submission.

### Cancelling fix jobs

`DELETE /fixes/{id}` records cancellation before removing workspace data. A running
worker must stop its engine before acknowledging cleanup. While `status` is
`cancelled` and `pause_reason` is `cancellation_cleanup`, the retained branch and
workspace remain reserved; new work cannot reuse them. The job page keeps polling
and distinguishes this pending cleanup from a completed discard.

A cleanup failure retains the recovery paths and reports `cleanup_pending` with
HTTP 500 (or 409 for competing state changes). Resolve the cleanup error and retry
the same DELETE, or use **Retry cleanup** on the job page once the worker has
stopped. This retries cleanup only. Repeating DELETE after successful cleanup is
idempotent. The branch/job metadata and remediation discard commit together after
cleanup succeeds. Automatic follow-up jobs use the same guarded, idempotent
creation transaction and cannot reopen a cancelled predecessor.

A worker that disappears before acknowledging cancellation remains pending;
automatic recovery of abandoned engines is still being qualified under F09.

### Accepting a local fix branch

Accepting a remediation preserves its commit on the local origin branch before
removing the worktree. It waits for active agents, pending cancellation cleanup
and child scans. Dirty worktrees, locked worktrees and unpublished clones remain
intact and return an error. On success, the remediation becomes frozen, paused
jobs finish, and their workspace paths are cleared together. Repeating acceptance
is safe; a failed acknowledgement can retry against the preserved origin branch.
Closed remediations cannot be reopened by stale updates or late child scans.

Fixer consoles: heartbeats and output/status/input updates are tied to the claimed worker and claim timestamp. Expired sessions finish with an error and require a new console; queued keystrokes are never replayed automatically into a replacement session. A cancellation racing a claim returns 409 for refresh/retry. This is database claim fencing; detached PTY and worker-death process recovery remain tracked in F09.

Published remediation cleanup: a saved successful push/PR releases its clean checkout after publication is durable. Cleanup failure retains the PR link and recovery path; the existing remediation accept endpoint and job-page **Retry cleanup** action retry removal without republishing or running an agent. Failed PRs retain their workspace for PR retry.
