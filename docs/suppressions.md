# Suppressions

Durable suppressions let teams hide accepted or irrelevant findings without
rewriting historical scan evidence.

Path suppressions (built-in defaults, repo `.wolfignore`, and gitignore) use
one matcher for the findings API, scan artifacts, and quality gates. A finding
the inbox hides must not fail CI. `.wolfignore` lines starting with `!` are
ignored (negation is not implemented). `WOLF_DISABLE_DEFAULT_SUPPRESSIONS=1`
skips the built-in path rules.

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
