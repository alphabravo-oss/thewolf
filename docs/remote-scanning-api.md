# Remote code-scanning API

Wolf can run as a durable, headless code-scanning service. A caller submits a
registered repository, a one-shot Git source, or a one-shot SSH source to
`POST /api/v1/scans`; an independent worker prepares the source and executes
the scanners. The existing web console uses the same API and remains available
unless the server is started with `--api-only`.

## Authentication and scopes

Use a JWT session token or a `wolf_` API token:

```bash
export WOLF_URL=https://wolf.example.com/api/v1
export WOLF_TOKEN=wolf_REDACTED
```

The common automation scopes are:

- `write:scans` to submit and cancel scans.
- `read:scans` to poll, stream, and retrieve results.
- `read:findings` to list findings.
- `write:credentials` and `read:credentials` to manage source credentials.
- `read:repos` to inspect repositories materialized from one-shot sources.

Every request below uses:

```bash
-H "Authorization: Bearer $WOLF_TOKEN" -H "Content-Type: application/json"
```

## Submit a scan

Registered repository:

```bash
curl -sS -X POST "$WOLF_URL/scans" \
  -H "Authorization: Bearer $WOLF_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: build-98312" \
  -d '{
    "repo_id": "repository-id",
    "branch": "main",
    "client_reference": "build-98312"
  }'
```

Public one-shot Git:

```bash
curl -sS -X POST "$WOLF_URL/scans" \
  -H "Authorization: Bearer $WOLF_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: build-98313" \
  -d '{
    "source": {
      "kind": "git",
      "name": "payments",
      "url": "https://git.example.com/acme/payments.git",
      "ref": "refs/heads/main"
    },
    "profile": "full",
    "client_reference": "build-98313"
  }'
```

Submission returns `201` with the existing `{"data": <scan>}` envelope and an
initial public status of `pending`. Source preparation happens in the worker,
after the response. Exactly one of `repo_id` and `source` is required.
One-shot sources are upserted as caller-owned repositories, so their history,
findings, baselines, and trends also appear in the existing UI.

`Idempotency-Key` is optional and may contain up to 255 characters. Repeating
the same normalized request with the same caller and key returns the original
scan with `201`. Reusing the key for a different request returns
`409 idempotency_conflict`.

## Private Git credentials

Credentials are encrypted at rest and referenced by ID. Their plaintext is
never copied into a scan request row, event, finding, log, or artifact.

HTTPS token/password:

```bash
CREDENTIAL_ID=$(
  curl -sS -X POST "$WOLF_URL/credentials" \
    -H "Authorization: Bearer $WOLF_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
      "type": "git_https",
      "name": "CI Git credential",
      "username": "scanner-bot",
      "secret": "REDACTED",
      "allowed_hosts": ["git.example.com"]
    }' | jq -r .data.id
)
```

Git over SSH:

```bash
CREDENTIAL_ID=$(
  curl -sS -X POST "$WOLF_URL/credentials" \
    -H "Authorization: Bearer $WOLF_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
      "type": "ssh_private_key",
      "name": "CI deploy key",
      "secret": "-----BEGIN OPENSSH PRIVATE KEY-----\nREDACTED\n-----END OPENSSH PRIVATE KEY-----",
      "known_hosts": "git.example.com ssh-ed25519 AAAA...",
      "allowed_hosts": ["git.example.com"]
    }' | jq -r .data.id
)
```

Reference the credential in the source:

```json
{
  "source": {
    "kind": "git",
    "url": "ssh://git@git.example.com/acme/payments.git",
    "ref": "v2.4.1",
    "credential_id": "credential-id"
  }
}
```

Git accepts only `https://` and `ssh://` transports. Inline credentials,
queries, fragments, local paths, `file://`, `git://`, and custom Git helpers
are rejected. Credential types must match the URL transport and their
`allowed_hosts` binding. Loopback, link-local, multicast, unspecified, and
unapproved private destinations are blocked. Permit intentional private Git
or API-created SSH networks with `WOLF_GIT_ALLOWED_CIDRS`, for example
`10.20.0.0/16`.

## One-shot SSH working tree

Use an existing caller-owned node:

```json
{
  "source": {
    "kind": "ssh",
    "name": "payments-host",
    "node_id": "node-id",
    "path": "/srv/payments",
    "ref": "main"
  }
}
```

Or register the connection with the scan:

```json
{
  "source": {
    "kind": "ssh",
    "name": "payments-host",
    "host": "build.example.com",
    "port": 22,
    "username": "scanner",
    "path": "/srv/payments",
    "base_path": "/srv",
    "credential_id": "credential-id",
    "known_hosts": "build.example.com ssh-ed25519 AAAA..."
  }
}
```

New connections require explicit host-key material. The requested path must be
absolute and inside `base_path`. Wolf archives the selected working tree into
an isolated workspace and applies the configured dirty-tree policy. Loopback,
link-local, multicast, unspecified, and unapproved private destinations are
rejected with the same `WOLF_GIT_ALLOWED_CIDRS` policy used for Git.

## Profiles and partial scans

- Omit `profile` to preserve legacy automatic or exact-`tools` behavior.
- `standard` uses normal language detection.
- `full` selects every applicable repository scanner, including heavy tools;
  DAST remains excluded without a runtime target.
- `targeted` requires at least one tool, category, or include-path selector.

Example:

```json
{
  "repo_id": "repository-id",
  "profile": "targeted",
  "categories": ["sast", "secrets"],
  "tools": ["semgrep", "gitleaks"],
  "include_paths": ["src/**", "cmd/**"],
  "exclude_paths": ["vendor/**", "**/*_test.go"]
}
```

Scope patterns must be relative and may not traverse with `..`. Requested and
effective scope are recorded on every scanner run. A plugin that cannot
narrow its native scan may inspect the full read-only snapshot and reports the
effective scope in `/scans/{id}/scanner-runs`.

## Poll, stream, cancel, and retrieve

Poll the durable scan:

```bash
curl -sS "$WOLF_URL/scans/$SCAN_ID" \
  -H "Authorization: Bearer $WOLF_TOKEN"
```

Public statuses remain `pending`, `running`, `completed`, `failed`, and
`cancelled`. The additive `phase` field provides queue/preparation detail.

Stream durable SSE:

```bash
curl -N "$WOLF_URL/scans/$SCAN_ID/stream" \
  -H "Authorization: Bearer $WOLF_TOKEN"
```

Each event has a monotonic `id`. Reconnect without losing progress:

```bash
curl -N "$WOLF_URL/scans/$SCAN_ID/stream" \
  -H "Authorization: Bearer $WOLF_TOKEN" \
  -H "Last-Event-ID: 42"
```

Cancel a scan or one tool:

```bash
curl -sS -X DELETE "$WOLF_URL/scans/$SCAN_ID" \
  -H "Authorization: Bearer $WOLF_TOKEN"

curl -sS -X DELETE "$WOLF_URL/scans/$SCAN_ID/tools/semgrep" \
  -H "Authorization: Bearer $WOLF_TOKEN"
```

Retrieve the compact automation result:

```bash
curl -sS "$WOLF_URL/scans/$SCAN_ID/result" \
  -H "Authorization: Bearer $WOLF_TOKEN"
```

The result contains status, phase, immutable source provenance, severity/tool
totals, scopes, quality-gate summary, and links. Findings remain paginated at
`/scans/{id}/findings`; SARIF, manifest, report, raw output, and artifact
downloads retain their existing routes.

To compare an existing scan under a newly approved scanner release, create a
distinct pinned re-scan rather than changing or retrying the original:

```bash
curl -sS -X POST "$WOLF_URL/scans/$SCAN_ID/release-rescans" \
  -H "Authorization: Bearer $WOLF_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: release-comparison-42" \
  -d '{"release_id":"RELEASE_ID","reason":"compare approved release"}'
```

This requires `write:scans` and `operate:scanner-supply-chain`. The returned
scan has its own ID, `rescan_of_scan_id`, release ID, and manifest digest.
Automatic worker retry still operates on one scan row and remains pinned to
that row's original release.

## Runtime and worker status

```bash
curl -sS "$WOLF_URL/scanners/runtime-capabilities" \
  -H "Authorization: Bearer $WOLF_TOKEN"

curl -sS "$WOLF_URL/scanners/workers" \
  -H "Authorization: Bearer $WOLF_TOKEN"
```

The UI uses runtime capabilities to hide Docker image pull/build actions in a
Kubernetes deployment while preserving those actions in Docker Compose.

## Operations

Docker Compose enables queue mode by default and starts one Docker-backed
worker. SQLite is supported with one effective worker. For multiple workers,
enable PostgreSQL and set `WOLF_SCAN_WORKERS`:

```bash
WOLF_DB_DRIVER=postgres \
WOLF_DB_DSN='postgres://wolf:REDACTED@postgres:5432/wolf?sslmode=disable' \
WOLF_SCAN_WORKERS=3 \
docker compose --profile postgres up -d --build
```

`WOLF_SCAN_EXECUTION_MODE=inline` is the one-release rollback mode. It keeps
the previous API-process execution behavior and the same UI contract.

Run the API without the SPA:

```bash
wolf serve --bind 0.0.0.0:8778 --api-only
# or WOLF_API_ONLY=true wolf serve --bind 0.0.0.0:8778
```

For Kubernetes installation and storage/RBAC constraints, see
[`deployment.md`](deployment.md#kubernetes-native-scanner-jobs). The live
OpenAPI document remains available at `/api/v1/openapi.json`, with Swagger UI
at `/api/v1/docs`.
