# Artifacts

Wolf persists scan artifacts so completed scans remain debuggable and auditable
without rerunning tools.

## Common Files

Scan artifact directories can include:

- `manifest.json`
- `findings.json`
- `RAW.md`
- `FIX-HIGH.md`
- `FIX-ALL.md`
- `combined.sarif`
- `diff.json`
- `gate-result.json`
- per-tool logs and raw JSON/SARIF/XML/text output

## Metadata

`scan_artifacts` records include artifact type, file path, size, SHA-256 checksum,
redaction level, and timestamps.

## Access Control

Artifact downloads require access to the owning scan. Raw and internal report
artifacts should be treated as sensitive because scanner output can contain source
paths, snippets, dependency names, and secret findings.

## Retention

The server can clean up old artifact directories with the `artifact_retention_days`
setting. Cleanup removes files only; database scan history remains.

## Validation

Run:

```sh
go test ./internal/artifacts ./internal/api/routes ./internal/db
```

