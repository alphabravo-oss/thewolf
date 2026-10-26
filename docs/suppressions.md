# Suppressions

Durable suppressions let teams hide accepted or irrelevant findings without
rewriting historical scan evidence.

Path suppressions (built-in defaults, repo `.wolfignore`, and gitignore) use
one matcher for the findings API, scan artifacts, and quality gates. A finding
the inbox hides must not fail CI. `WOLF_DISABLE_DEFAULT_SUPPRESSIONS=1` skips
built-in path rules. It does not disable explicit `.wolfignore` or durable rules.

`.wolfignore` accepts one path glob followed by optional `category=` (fine
categories), `rule=` (rule IDs), and `reason=` fields. Filters accept comma-separated
nonempty values without spaces. `categories`, `rules`, `rule_id`, and `rule-id`
are aliases. Blank lines and comments starting with `#` are ignored; a comment
can follow a rule after whitespace. Predicates on a rule are combined with AND.

```text
**/legacy/** category=xss,sql-injection rule=R1,R2 reason=accepted
```

Unknown or empty filters, invalid globs, read failures, and unsupported `!`
negation reject the entire file. No earlier lines or built-in decisions are
applied to that failed projection. Fix the file and retry; the local CLI returns
a failure, and affected uncaptured live API/report/gate requests return an error. Completed scans use their captured policy instead. A rule
file must be regular and remain inside its source directory; outside symlinks,
directories and FIFOs are rejected. Existing historical artifact downloads retain
their original bytes.

The scanner's existing approximately 64 KiB line limit still applies. Whitespace
inside paths/values is not supported. A missing file remains optional. Policy capture occurs after tool results have been persisted and before final grouping, baseline derivation and report publication. Capturing separately read files does not make a mutable checkout atomic during a running scan; source preparation and recovery qualification remain in `PLAN.md`.

## Git ignore decisions

Wolf uses native Git matching for nested `.gitignore` files and `.git/info/exclude`,
including negation, anchoring and tracked-file semantics. Tracked files remain
visible even if a pattern would ignore an untracked file with that name. A registered
source must have its own `.git` directory or worktree pointer; Wolf does not inherit
policy from a repository above that source. Sources without Git metadata currently
receive no Git-derived suppression.

Machine/global excludes, `core.excludesFile` overrides and inherited `GIT_*`
redirection do not control these decisions. Move intentional project rules into
`.gitignore` or `.wolfignore` instead. Existing linked worktrees and common-directory
exclude files are supported. Ignore policy files must be regular, readable files;
symlinks, directories and FIFOs return errors.

Paths are exchanged with Git as NUL-delimited literal names. A newline, quote,
Unicode character or embedded `..` in a filename does not split, quote or discard
that filename. Non-relative paths, `..` path segments and NUL-containing paths are
excluded from Git evaluation and remain visible unless another rule matches.

Git failures, diagnostics or malformed output reject the entire projection without
leaving default/file decisions partly applied. Cancellation propagates from the
caller, and the native command has a ten-second ceiling per batch. The packaged
server runtime includes Git; local Git sources require it on PATH. Saved artifact
downloads remain historical. Stored Git decisions cover every observed finding path, including paths temporarily hidden by a durable suppression. New paths cannot be inserted into a captured scan without a matching policy decision.

## Scan-time visibility and recorded artifacts

Completed scans retain the effective ignore rules used during finalization.
Changing `.wolfignore`, `.gitignore`, Git excludes or the default-rule environment
changes new scans, not completed scans. Native scans capture from their prepared
source; SARIF imports capture the registered repository policy at import time.
The database retains the ordered default/file rules and native Git decisions for
all observed paths. Manifests include the same `ignore_policy` snapshot.
Historical views therefore work after the original checkout is edited or removed.

Wolf-managed suppressions remain editable. Creating, revoking or expiring a rule
recomputes finding flags, representatives and counts using the captured source
policy. Revoking a durable rule cannot unhide a finding independently hidden by
that scan's ignore rules. Durable/imported decisions keep their existing priority.

Capture is lease-checked and transactional with source flags and the finding
revision. Repeated capture preserves the original snapshot. Ordinary scan updates
cannot replace it; mismatched source identity, unknown snapshot versions, malformed
snapshots and uncaptured finding paths return errors. Publication uses the same
snapshot for artifacts and gates, and completion verifies its revision and source.

**Legacy scans:** migration 069 does not invent old rules or reread today's files.
Completed scans without a snapshot retain their persisted suppression decisions.
Migration recalculates legacy headers from stored primary-row decisions and advances the finding revision when the count changes. Their historical rule inputs are unavailable; stored decisions may not reproduce
previous dynamically projected views. Failed/cancelled scans without a captured
policy likewise retain stored evidence. Running scans that have not captured a
policy still evaluate live rules and report policy-read failures.

The live manifest endpoint refreshes counts, finding revision and baseline using
current durable decisions and the fixed source policy, preserving producer
metadata and the original raw count. It uses `Cache-Control: private, no-store`.
Explicit artifact downloads retain their original bytes and recorded revision.

## Scope Types

Supported scopes:

- `fingerprint`
- `stable_fingerprint`
- `rule`
- `fine_category`
- `path_glob`

Suppressions can be scoped to a repo and branch, include a required reason, and
optionally expire.

## API

- `GET /api/v1/suppressions?repo_id=<repo-id>`
- `POST /api/v1/suppressions/preview`
- `POST /api/v1/suppressions`
- `DELETE /api/v1/suppressions/{id}`

## CLI

```sh
wolf suppress list --repo <repo-id>
wolf suppress preview --repo <repo-id> --scope-type stable_fingerprint --scope-value <fp> --reason "accepted risk"
wolf suppress add --repo <repo-id> --scope-type stable_fingerprint --scope-value <fp> --reason "accepted risk" --expires-at 2026-12-31T00:00:00Z
wolf suppress revoke <suppression-id>
```

## Audit

Create and revoke operations write suppression audit records. Findings are updated
to reflect the active suppression, but completed scan artifacts remain immutable.

## Validation

Run:

```sh
go test ./internal/finding/suppression ./internal/api/routes ./internal/db
```
