# Scanner Planning

Scanner planning explains which scanners run, which scanners skip, and why.

## Manifest Source

`scanners/tools.yaml` is the source of truth for scanner metadata:

- display name
- category
- integration tier
- pinned version
- image or install strategy
- update source
- resource class
- default timeout
- network requirement
- exclusive execution requirement

Validation fails if a registered scanner is missing required metadata.

## Resource Classes

Supported resource classes:

- `light`
- `medium`
- `heavy`
- `network`
- `exclusive`

The runner enforces global concurrency plus class-specific controls:

- heavy scanner concurrency defaults to `1`
- network scanner concurrency defaults to `2`
- exclusive scanners run one at a time
- manifest `default_timeout` overrides the scan-level timeout for that tool

## CLI

```sh
wolf scan --repo <path> --plan-only
wolf scan --repo <path> --heavy-concurrency 1 --network-concurrency 2
wolf scanners explain --repo <path>
```

## Server Settings

The server reads:

- `scan_concurrency`
- `heavy_scanner_concurrency`
- `network_scanner_concurrency`

## Validation

Run:

```sh
make scanners-validate
go test ./internal/scan/planner ./internal/scan/runner ./internal/scannertools/manifest
```
