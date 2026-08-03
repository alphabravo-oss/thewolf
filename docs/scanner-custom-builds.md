# Durable custom scanner-image builds

Wolf's Custom build surface is a durable administrative operation for building
the fixed scanner image variants embedded in the Wolf binary. It is separate
from managed scanner release candidates: a custom build is an operator-requested
local image load or registry publication, while a release candidate runs the
complete discovery, evidence, approval, signing, and rollout pipeline.

The API process validates and enqueues requests only. A dedicated
`scanner-custom-build-worker` owns Docker/buildx access, resolves the opaque
registry secret reference immediately before execution, persists bounded logs
and variant results, and clears its lease when the aggregate becomes terminal.
API/UI disconnection never cancels an operation.

## Supported contract

The only accepted variants and build inputs are:

| Variant | Embedded Dockerfile | Local load | Registry push |
| --- | --- | --- | --- |
| `default` | `Dockerfile` | yes | yes |
| `jvm` | `Dockerfile.jvm` | yes | yes |
| `rust` | `Dockerfile.rust` | yes | yes |
| `codeql` | `Dockerfile.codeql` | yes | **no** |

CodeQL is hard local-only. A request containing CodeQL and `push:true`,
including `variants:["all"]`, fails before version reservation or worker
execution. This restriction is not configurable.

Platforms are limited to `linux/amd64` and `linux/arm64`. A local load accepts
zero or one platform because buildx cannot load a multi-platform manifest into
a local daemon. A pushed build may select one or both. Wolf does not accept a
caller Dockerfile, context archive, command, build argument, secret value,
registry host, or arbitrary image name.

Each publish request reserves the next scanner image patch version in the same
serializable transaction that creates the operation. A failed or cancelled
publish can therefore leave an intentional version gap. Versions are never
reused because a worker may have published content before losing its database
lease.

## API

All routes require an administrator role. Reads require `read:config`; writes
require `write:config`.

```http
POST /api/v1/scanners/custom-builds
Authorization: Bearer …
Idempotency-Key: custom-build-2026-07-30-01
Content-Type: application/json

{
  "variants": ["default", "jvm", "rust"],
  "push": true,
  "platforms": ["linux/amd64", "linux/arm64"],
  "namespace": "acme-security",
  "credential_secret_id": "opaque-dockerhub-token-secret-id",
  "reason": "Rebuild approved scanner variants after weekly lock refresh"
}
```

The response is `202 Accepted`:

```json
{
  "id": "8adba242-60de-48ee-bdb5-b7da1af42a17",
  "state": "queued",
  "status_url": "/api/v1/scanners/custom-builds/8adba242-60de-48ee-bdb5-b7da1af42a17",
  "events_url": "/api/v1/scanners/custom-builds/8adba242-60de-48ee-bdb5-b7da1af42a17/events"
}
```

Exact retries with the same idempotency key return the original operation and
`Idempotent-Replay: true`. Reusing the key for different normalized inputs
returns `409`. The secret ID is used to validate ownership/type on enqueue and
is persisted as an opaque worker-only reference; it and the publish reservation
are never serialized in API responses.

Other operations:

- `GET /api/v1/scanners/custom-builds?state=&cursor=&limit=`
- `GET /api/v1/scanners/custom-builds/{id}` (returns `ETag`)
- `GET /api/v1/scanners/custom-builds/{id}/events`
- `POST /api/v1/scanners/custom-builds/{id}/cancel`
- `POST /api/v1/scanners/custom-builds/{id}/retry`

Cancel and retry require `Idempotency-Key`, `If-Match`, and `{"reason":"…"}`.
A queued cancellation is immediately terminal. Claimed/running cancellation is
cooperative: heartbeat observes the request, cancels buildx, marks unfinished
variants cancelled, and finalizes the aggregate. Retry accepts only `failed`
or `partial`, honors the aggregate attempt budget, and rebuilds failed variants
without rebuilding completed variants.

The SSE stream contains persisted `log` events with monotonic IDs 1–4000 and
one stable terminal `done` or `error` event with ID 4001. Reconnect with
`Last-Event-ID`; sending 4001 after terminal completion returns an empty stream
instead of replaying the terminal event. Logs are capped at 4,000 lines,
4 MiB per operation, 8 KiB per line, and 500 rows per read. Invalid UTF-8 and
control characters are normalized, credential-shaped/plain/base64 token data
is redacted, and a single truncation marker is attempted when the budget is
exhausted. Unexpected log persistence or lease failures cancel execution and
leave the operation for safe lease recovery.

## CLI

```bash
wolf scanner custom-build create \
  --variant default --variant jvm --variant rust \
  --push \
  --platform linux/amd64 --platform linux/arm64 \
  --namespace acme-security \
  --credential-secret-id "$DOCKERHUB_SECRET_ID" \
  --reason "Weekly approved scanner refresh" \
  --idempotency-key CHG-1234-custom-build \
  --watch

wolf scanner custom-build list --state running
wolf scanner custom-build show "$BUILD_ID"
wolf scanner custom-build events "$BUILD_ID" --after 42
wolf scanner custom-build cancel "$BUILD_ID" \
  --if-match 4 --reason "Registry maintenance started"
wolf scanner custom-build retry "$BUILD_ID" \
  --if-match 9 --reason "Registry service recovered"
```

CLI output uses the normal stable JSON/YAML/table renderer. Event watch resumes
from persisted IDs and polls the status resource if a stream reconnect fails.

## Compatibility endpoints

`POST /api/v1/scanners/images/{variant}/build` and
`POST /api/v1/scanners/images/build-all` now enqueue the same durable
aggregate and stream persisted logs. They return `X-Wolf-Operation-ID`,
`Location`, and `X-Wolf-Events-URL`. Existing callers may omit
`Idempotency-Key`; Wolf generates a compatibility key. Disconnecting the old
SSE console does not cancel work, and the first-class events URL can reconnect.
A pushed build-all fails closed because it contains CodeQL.

Production leaves the API's Docker executor seam nil. The only synchronous
execution path is an injected test hook used by Docker-free characterization
tests.

## Worker and deployment

Run a worker directly:

```bash
wolf scanner-custom-build-worker \
  --worker-id=custom-builder-a \
  --poll-interval=2s \
  --heartbeat-interval=10s \
  --lease-duration=45s \
  --operation-timeout=2h
```

Equivalent environment variables are:

- `WOLF_SCANNER_CUSTOM_BUILD_WORKER_ID`
- `WOLF_SCANNER_CUSTOM_BUILD_ONCE`
- `WOLF_SCANNER_CUSTOM_BUILD_POLL_INTERVAL`
- `WOLF_SCANNER_CUSTOM_BUILD_HEARTBEAT_INTERVAL`
- `WOLF_SCANNER_CUSTOM_BUILD_LEASE_DURATION`
- `WOLF_SCANNER_CUSTOM_BUILD_OPERATION_TIMEOUT`
- `WOLF_SCANNER_CUSTOM_BUILD_OBSERVABILITY_ADDR`

Compose is opt-in:

```bash
docker compose --profile scanner-custom-build up -d \
  scanner-custom-build-worker
```

Prefer PostgreSQL when API and worker run as separate containers. Use the
hardened Compose override to route access through the scoped socket proxy, or
point the worker at a dedicated/rootless engine. The base Compose API retains
its existing scanner-runtime socket for backward-compatible local scanning and
image troubleshooting; custom-build code never executes in that process.

Helm is opt-in with `scannerRelease.customBuild.enabled=true` and the explicit
`scannerRelease.customBuild.engineMode=node-local`. The chart mounts the
configured node-local socket only into the custom-build worker, never the API
pod, disables service-account token automount, drops all capabilities, and uses
a read-only root filesystem. Prefer a dedicated/rootless socket. The bundled
chart rejects remote or unauthenticated TCP Docker endpoints; provide a
separately hardened, mutually authenticated TLS deployment for that topology.

Every build creates a private per-operation Docker config directory beneath
its temporary operation directory. Both `docker login` and `docker buildx`
receive the same `--config` path, the token is supplied only on stdin, the
username is not logged, and deferred cleanup removes config/context on success,
failure, timeout, or cancellation. No process-global Docker environment or
home-directory config is mutated.

## Persistence, recovery, and backup

Migration 046 creates `scanner_custom_builds`,
`scanner_custom_build_variants`, and `scanner_custom_build_logs` on SQLite and
PostgreSQL. Aggregate transitions and correlation events commit atomically.
Claims use opaque exact-match tokens, heartbeat/expiry, attempt budgets, and
optimistic resource versions. On startup/poll, workers reclaim expired
operations: within budget, running variants return to queued; after exhaustion
the operation fails `worker_lost`; a pending cancellation becomes cancelled.
Raw lease tokens never enter event/audit idempotency keys—only one-way
fingerprints are persisted.

Scanner release backup format v1 requires migration 046. It includes custom
build aggregates, variants, logs, and correlation events, but sanitizes worker
ID, lease token, heartbeat, and lease expiration. A restored in-flight custom
build must be reclaimed before workers resume. Registry secret backing objects
remain part of the deployment's encrypted secret recovery process.

## Validation

Before enabling the worker:

```bash
go test -count=1 ./internal/scannerbuild \
  ./internal/scannercustombuildworker ./internal/db ./internal/api ./internal/cli
go test -race -count=1 ./internal/scannerbuild \
  ./internal/scannercustombuildworker ./internal/db
bash deploy/helm/wolf/tests/render-security.sh
```

Set `WOLF_TEST_POSTGRES_DSN` and run the custom-build repository and
backup/restore PostgreSQL contracts against an isolated disposable schema.
Then perform a non-production engine smoke test for one local default build,
one multi-platform push, an active cancellation, worker termination followed
by lease recovery, SSE reconnect from a mid-log ID, and secret canary searches
across API output, durable logs, events, the worker filesystem, and backup
payload.
