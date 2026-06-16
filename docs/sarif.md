# SARIF

Wolf supports SARIF export from completed scans and SARIF import from external
tools.

## Export

Use:

```sh
wolf sarif export --scan <scan-id>
wolf scan sarif <scan-id>
```

The API endpoint is:

- `GET /api/v1/scans/{scanID}/sarif`

Exported SARIF uses normalized findings and Wolf fingerprints where available.

## Import

Use:

```sh
wolf sarif import --repo <repo-id> --file results.sarif --source github-code-scanning
```

The API endpoint is:

- `POST /api/v1/sarif/import`

Imported SARIF creates a completed scan, stores import metadata, persists imported
findings, writes scan artifacts, and records scanner run rows with status
`imported`.

## Validation

Run:

```sh
go test ./internal/finding/sarifio ./internal/api/routes ./internal/scan/report
```
