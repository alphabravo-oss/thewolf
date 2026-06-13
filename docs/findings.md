# Findings

Wolf normalizes scanner output into durable findings before UI, API, CLI,
reports, baselines, suppressions, gates, and SARIF export consume it.

## Identity Fields

Each persisted finding can carry three identity values:

- `location_fingerprint`: exact-ish path, line, tool, and rule identity.
- `semantic_fingerprint`: rule/category identity with more tolerance for line movement.
- `stable_fingerprint`: primary identity used for baselines and suppressions.

`identity_version` records the fingerprint algorithm version. New algorithms should
be versioned instead of silently changing historical matching behavior.

## Lifecycle Fields

Findings can be classified as:

- `new`
- `existing`
- `fixed`
- `resurfaced`
- `suppressed`
- `accepted_risk`
- `ignored_by_policy`

Suppression state is stored on findings with `suppressed`, `suppression_id`, and
`suppressed_reason`. Source provenance is stored with `source_kind` and
`source_ref`.

## Normalization Expectations

Scanner integrations should populate:

- scanner/tool name
- rule ID and title
- severity
- file path and line range when available
- CWE/category/fine category when available
- SARIF metadata when parsed from SARIF-producing tools

The runner applies deterministic enrichment and fingerprints before deduplication.

## Validation

Run:

```sh
go test ./internal/finding/... ./internal/scan/runner ./internal/db
```

