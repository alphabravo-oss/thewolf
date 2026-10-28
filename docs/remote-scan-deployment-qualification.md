# Exact remote-scan deployment qualification

Kubernetes/Helm qualification (a kind cluster running the Helm chart) is part of
Wolf Enterprise and lives in the private `wolf-enterprise` repository. This
guide covers the Community native and Compose topologies.

Wolf qualifies the remote code-scanning API as a distributed system, not only
as an HTTP handler. The qualification matrix starts a real API role and a
separate durable scan worker, submits a targeted Bandit scan through the
public API, retries it with the same idempotency key, verifies Git commit/tree
provenance and exact scanner-image provenance, restarts both roles, and repeats
the scan against the same persisted state.

The protected GitHub Actions entrypoint is
`.github/workflows/remote-scan-deployment-qualification.yml`. It accepts only
three `repository@sha256:<64 hex>` inputs from `main`:

- the qualified Wolf runtime image;
- the qualified default scanner image from a complete scanner release; and
- a qualified PostgreSQL image.

Configure the `scanner-release-qualification` GitHub environment with required
reviewers. A single authorized dispatch runs both receipts (SQLite is no longer supported, so only PostgreSQL is qualified):

| Receipt | Database | Execution path | Restart proof |
|---|---|---|---|
| Native | PostgreSQL | exact runtime binary + Docker scanner | API and worker processes |
| Compose | PostgreSQL | published runtime/PostgreSQL containers + Docker scanner | API and worker services |

Every job retains its JSON receipt for 365 days. The closure job requires two
passing receipts, restart recovery in each, and writes SHA-256 checksums for
the change record. A failed or skipped lane prevents closure.

## Receipt assertions

The shared API probe checks all of the following:

1. readiness and authentication/first-user bootstrap;
2. creation of a local Git repository target;
3. targeted `bandit` scan submission through `POST /scans`;
4. external idempotency-key replay resolving to the same scan;
5. durable queue completion by the separate worker;
6. a 40-character Git commit and SHA-256 tree digest;
7. a completed Bandit scope and scanner-run record;
8. the expected `docker` or `kubernetes` execution backend; and
9. persistence of the exact scanner child digest in runtime provenance.

The second pass occurs only after both application roles are restarted. It
therefore also covers migration idempotency, authentication persistence,
queue/lease recovery, shared workspace and artifact storage, and ordinary
deployment restart behavior.

## Local execution

All harnesses are opt-in and reject mutable image references. Examples below
use placeholders deliberately; resolve exact child manifests from the signed
release aggregates before running them.

```sh
export WOLF_E2E_RUNTIME_IMAGE='registry.example/wolf-runtime@sha256:<digest>'
export WOLF_E2E_SCANNER_IMAGE='registry.example/wolf-scanners@sha256:<digest>'
export WOLF_E2E_POSTGRES_IMAGE='registry.example/postgres@sha256:<digest>'
```

Native PostgreSQL launches `WOLF_E2E_POSTGRES_IMAGE` on an isolated tmpfs by
default. Set `WOLF_E2E_POSTGRES_DSN` to qualify an already-running customer
database instead:

```sh
WOLF_RUN_REMOTE_SCAN_NATIVE_E2E=1 \
scripts/e2e/remote-scan-native.sh
```

Compose owns its database lifecycle:

```sh
WOLF_RUN_REMOTE_SCAN_COMPOSE_E2E=1 \
WOLF_E2E_DATABASE=postgres \
scripts/e2e/remote-scan-compose.sh
```

## Failure evidence and cleanup

Native and Compose failures print bounded API/worker or service logs. Each
harness uses a unique temporary directory/project, terminates only the
processes it started, and
removes its own disposable volumes. It never performs a broad Docker prune.

Repository-controlled success is necessary but not sufficient for production
acceptance. The protected workflow must use the final signed/mirrored release
digests, and its closure receipt must be reviewed alongside registry signature,
provenance, SBOM, mirror-parity, and independent security/UX evidence.
