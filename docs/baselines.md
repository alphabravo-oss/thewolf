# Baselines

Baselines let engineers compare a current scan against a known scan and focus on
what changed.

## Workflow

1. Create or identify a completed scan for the repo and branch.
2. Create a named baseline for that scan.
3. Compare future scans against the baseline scan.
4. Use comparison output to review new, existing, fixed, and resurfaced findings.

## API

- `GET /api/v1/repos/{repoID}/baselines`
- `POST /api/v1/repos/{repoID}/baselines`
- `POST /api/v1/scans/{scanID}/compare`
- `GET /api/v1/scans/{scanID}/compare/{baselineScanID}`
- `GET /api/v1/scans/{scanID}/diff?baseline_scan_id=<id>`

## CLI

```sh
wolf baseline list --repo <repo-id>
wolf baseline create --repo <repo-id> --scan <scan-id> --name main
wolf baseline compare --scan <scan-id> --baseline <baseline-scan-id>
wolf scan compare <scan-id> <baseline-scan-id>
```

## Compatibility

Wolf rejects comparisons across different repositories. Source provenance is also
checked so incompatible source kinds, or different SSH remote nodes, are rejected
by default.

## Validation

Run:

```sh
go test ./internal/finding/diff ./internal/api/routes ./internal/db
```
