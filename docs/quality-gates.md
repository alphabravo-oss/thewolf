# Quality Gates

Quality gates evaluate normalized findings against a policy and produce a
deterministic pass, warn, or fail result.

## Default Behavior

If no stored policy applies, Wolf evaluates the default policy from
`internal/finding/gates`. Gates can be evaluated after a scan without rescanning.

## API

- `GET /api/v1/scans/{scanID}/gate`
- `GET /api/v1/policies`
- `POST /api/v1/policies`
- `PUT /api/v1/policies/{id}`

## CLI

```sh
wolf scan gate <scan-id>
wolf scan gate <scan-id> --fail-exit-code
wolf policy list
wolf policy put --name default-security --mode enforce --rules-file policy.json
```

When `--fail-exit-code` is set, a failed gate returns exit code `2`.

## Policy Shape

Policies match findings by severity, confidence, category, fine category,
baseline state, tool, path, package metadata, fix availability, known exploited
status, and suppression state where supported.

## Artifacts

Gate evaluation writes `gate-result.json` as an internal report artifact when an
artifact directory is available.

## Validation

Run:

```sh
go test ./internal/finding/gates ./internal/api/routes ./internal/cli
```

