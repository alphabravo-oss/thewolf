# Deploying The Wolf

Wolf Community ships as a Docker Compose stack. This guide covers running it on a
public server with TLS, and the hardened Docker-access path. Kubernetes and Helm
packaging is part of Wolf Enterprise (see [Kubernetes](#kubernetes)).

## TL;DR

```bash
cp .env.example .env        # then edit it (admin password, domain, email, secret)

# Local / private (HTTP on localhost):
docker compose up -d --build

# Public server with automatic HTTPS (Let's Encrypt):
docker compose --profile proxy up -d --build

# Public + HTTPS + hardened Docker access (recommended for shared hosts):
docker compose -f docker-compose.yml -f docker-compose.hardened.yml \
  --profile proxy up -d --build
# Scanner containers default to --network none; see docs/scan-isolation.md.
```

Add `--profile postgres` to any of the above to use Postgres instead of SQLite.

Queue-mode API usage, one-shot Git/SSH sources, credentials, idempotency, SSE,
and result retrieval are documented in
[`remote-scanning-api.md`](remote-scanning-api.md).

Exact-image API/worker qualification across native and Compose
topologies is documented in
[`remote-scan-deployment-qualification.md`](remote-scan-deployment-qualification.md).

---

## TLS

Wolf itself speaks HTTP only — TLS is terminated by the bundled **Caddy** reverse
proxy (the `proxy` profile). Caddy sets `X-Forwarded-Proto`. Wolf accepts it only
when the socket peer is explicitly trusted by `WOLF_TRUSTED_PROXIES`, then marks
session cookies `Secure`. There are two modes, both driven by `.env`.

Before enabling public HTTPS, set `WOLF_TRUSTED_PROXIES` to a comma-separated list
of the immediate proxy's **actual source IP addresses or CIDRs as seen by Wolf**.
For a native reverse proxy on the same host, this might be
`127.0.0.1/32,::1/128`. A Compose Caddy container normally uses a Docker-network
address, not loopback: use its assigned stable address, or a dedicated subnet
containing only trusted proxy hops. Do not copy loopback values for a container
proxy, trust all private networks, or use `0.0.0.0/0` / `::/0`. Compose passes the
variable from `.env`; native deployments set it in the API service environment.
The Wolf Enterprise Helm chart supplies it through `extraEnv`.

The default is empty: no forwarded addresses or protocols are trusted. Invalid
configuration disables all proxy trust and emits a startup warning. Restart the
API after changing this setting. If proxy addresses change, update the allowlist;
a stale address fails closed. Existing proxy deployments must configure this
setting on upgrade to retain per-client rate limits, Secure cookies and HTTPS
SSO callback URLs.

The final trusted proxy must overwrite `X-Forwarded-Proto` with one `http` or
`https` value and append the socket client to `X-Forwarded-For` (or replace it
with that address). Wolf traverses the chain from right to left, through only
configured trusted hops, stopping at the nearest untrusted address. It ignores
`X-Real-IP` and `Forwarded`. Malformed trusted-hop addresses fall back to the
socket peer; duplicate/ambiguous protocol values do not assert HTTPS. Audit
entries and rate limits share the resolved address; HTTP request logs retain the
socket peer. Without a trusted proxy, clients behind it share its rate bucket.
Restrict direct access to the API port at the deployment boundary.

### Mode 1 — Automatic (Let's Encrypt)  *(default)*

```ini
WOLF_DOMAIN=wolf.example.com     # must resolve to this host's public IP
ACME_EMAIL=you@example.com       # ACME contact / renewal notices
```

```bash
docker compose --profile proxy up -d
```

Caddy obtains and auto-renews a certificate for `WOLF_DOMAIN`. Ports 80 and 443
must be reachable from the internet (80 is used for the ACME challenge and a
redirect to 443).

### Mode 2 — Bring your own certificate

Use this for an internal CA, a wildcard cert, or a cert you already manage.

```ini
CADDYFILE=./deploy/Caddyfile.byocert
WOLF_DOMAIN=wolf.example.com
WOLF_TLS_DIR=/absolute/path/to/your/certs   # must contain cert.pem + key.pem
```

- `cert.pem` is the full chain (leaf + intermediates), `key.pem` the private key.
- `ACME_EMAIL` is ignored in this mode.

```bash
docker compose --profile proxy up -d
```

The directory is mounted read-only into Caddy at `/certs`. To rotate the cert,
replace the files and `docker compose restart caddy`.

---

## Hardened Docker access (socket-proxy)

Wolf spawns scanner containers, which normally requires the host Docker socket
(`/var/run/docker.sock`) — and that socket is effectively **root on the host**.
The `docker-compose.hardened.yml` override puts a
[docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy) in front
of the real socket. Only the proxy mounts the socket; Wolf talks to a filtered
TCP API over the internal network and the direct socket mount is removed.

```bash
docker compose -f docker-compose.yml -f docker-compose.hardened.yml \
  --profile proxy up -d --build
```

The proxy allows only the API groups Wolf needs (containers, images, build,
version) and denies the rest (swarm, secrets, services, volume management,
networks, …). Requires Docker Compose v2.24+ (for the `!override` tag).
The checked-in default is pinned to a reviewed multi-platform image digest so
restarts cannot silently change this privileged boundary. To upgrade, inspect
the new manifest, validate the hardened profile, and set
`WOLF_DOCKER_SOCKET_PROXY_IMAGE` to an explicit `tag@sha256` reference.

> **This reduces, but does not eliminate, the risk.** A compromised Wolf could
> still create a container with host bind-mounts. For full isolation, run Wolf
> on a **dedicated host**, and consider **rootless Docker** or **sysbox**.

If you don't run the in-app scanner-image builder, you can tighten the proxy
further by removing `BUILD`, `SESSION`, and `DISTRIBUTION` from its environment
in `docker-compose.hardened.yml`.

---

## Public-server checklist

1. **Set a strong `WOLF_MASTER_KEY`** (32 random bytes encoded as 64 hexadecimal
   characters). Without it the generated key must come from the shared data
   volume; every API/worker replica needs the same key.
2. **Change the admin password** (`WOLF_ADMIN_PASSWORD`). Self-service
   registration is **off by default** — add users from Settings → Users.
3. **Front with the proxy** and don't expose Wolf's own port publicly. Keep
   `WOLF_BIND=127.0.0.1` (or remove the port mapping); only Caddy faces 80/443.
4. **`WOLF_CORS_ORIGINS=https://your-domain`** so the browser origin matches.
5. **Use the hardened socket-proxy override** on shared/multi-tenant hosts.
6. **Use Postgres** (`--profile postgres`) for durability and concurrency; set a
   real `POSTGRES_PASSWORD` (or a Docker secret).
7. **Firewall:** open only 80 + 443. Postgres binds to localhost by default.
8. **Scanner images:** make sure `WOLF_SCANNERS_TAG` resolves for your host's
   architecture (publish multi-arch images via `make scanners-buildx-all` or
   the `scanners-image` CI workflow).
9. **Back up** the `wolf-data` (and `pg-data`) volumes.

### Persisted keys and startup failures

Without `WOLF_MASTER_KEY`, Wolf persists two files under `~/.wolf`: `master.key`
contains the 32-byte encryption key as 64 hexadecimal characters; `jwt-secret`
contains the raw JWT signing key. Preserve both files with the database and make
the same keys available to every relevant replica. Newly generated files have
mode `0600`. Concurrent first starts publish one complete key without replacing
the winner; creation requires filesystem support for hard links and directory
sync. Key-file links must resolve within the opened `~/.wolf` directory.

The API, scan worker and fixer stop on key-loading errors. An unreadable,
malformed or unwritable key is an operator error; startup does not overwrite it
or fall back to a temporary key. Check the configured home, mount, ownership and
permissions, or restore the original key from backup. Do not delete an existing
key to bypass the error: a new encryption key cannot decrypt stored credentials,
and a changed JWT key invalidates existing tokens. Planned key rotation and
versioned encryption migration remain tracked in PLAN G04.

## Kubernetes

Community deploys with Docker Compose (above). Kubernetes and Helm packaging is
part of Wolf Enterprise: the Helm chart, its security render checks, and the
kind-cluster remote-scan qualification live in the private
`AlphaBravoCompany/wolf-enterprise` repository and are delivered with the
Enterprise image under a commercial agreement. On Kubernetes, scanner tools run
as native per-tool Jobs and the API never mounts a Docker socket.

## Scanner release factory

Scanner image/toolchain updates use separate durable scheduler, discovery,
proposal, builder, and rollout-controller roles; they do not execute inside
the API process or the normal code-scan worker. Compose exposes the scheduler,
HTTPS-only discovery worker, builder, and database-backed rollout controller
through the opt-in `scanner-release` profile. Proposal execution has a separate
`scanner-release-proposal` profile because its repository write credentials
are a privileged trust boundary. The profile uses the built-in
`proposal-runtime` image and `/usr/local/bin/wolf scanner-proposal-executor`;
it does not require an installation-specific host executable. The Wolf Enterprise Helm chart exposes independent
`scannerRelease.scheduler.enabled`, `scannerRelease.discovery.enabled`,
`scannerRelease.proposal.enabled`, `scannerRelease.rollout.enabled`, and
`scannerRelease.builder.enabled` deployments, all disabled by default.

The API and UI exposure is controlled independently from those worker
deployments:

| Compose environment | Helm value (Enterprise chart) | Default | Effect |
| --- | --- | --- | --- |
| `WOLF_SCANNER_RELEASE_MODE` | `scannerRelease.mode` | `read_only` | Enables `disabled`, `read_only`, `candidate`, `canary`, or full `stable_control` capability levels. Each level includes the capabilities before it; stable control is never implicit. |
| `WOLF_SCANNER_LEGACY_BUILD_ENDPOINTS` | `scannerRelease.legacyBuildEndpoints` | `true` | Keeps the deprecated scanner-image build compatibility streams available. `false` returns `410 Gone` with a successor link to durable Custom builds. |

The defaults are intentionally upgrade-compatible: an existing UI does not
lose controls after an upgrade, and existing build automation keeps working
while it migrates. For a new or deliberately staged deployment, begin with
`read_only`, verify overview/inventory/policy visibility, then advance through
`candidate`, `canary`, and `stable_control` only after the corresponding
operations are qualified. `disabled` hides the entire scanner release API
behind a deterministic `409 scanner_release_mode_restricted` response.

Example staged Helm configuration (Enterprise chart):

```yaml
scannerRelease:
  mode: read_only
  legacyBuildEndpoints: true
```

Changing the mode does not delete releases, candidates, policy revisions, or
rollout history. To roll back an enablement stage, set the preceding mode and
redeploy the API; in-flight worker roles should also be disabled or scaled down
if their work is no longer desired. To restore the exact pre-upgrade surface,
set `scannerRelease.mode=stable_control` and
`scannerRelease.legacyBuildEndpoints=true` (or the equivalent Compose
variables). Re-enable the legacy endpoint before rolling back a client that
still calls it.

V2 offline bundle imports use two independent public trust policies: the
portable release-manifest signature and the OCI image signatures. Configure
`scannerRelease.offlineBundles.portableTrustPolicySecret` for the first. Enable
`scannerRelease.offlineBundles.imageVerifier`, bake its absolute executable
path into the immutable API image, and mount its separate trust-policy Secret
for the second. The chart leaves both unmounted by default and fails rendering
when image verification is enabled without its trust policy. See
[offline export and import](scanner-release-offline-bundles.md#offline-image-signature-verifier)
for the exact adapter protocol and Compose paths.

The standard image ships the immutable discovery manifest and lock under
`/usr/share/wolf/scanners`. The Enterprise Helm chart can instead mount an operator-managed ConfigMap.
Production replicas require PostgreSQL, an immutable definition commit, an
enabled release policy, and dedicated proposal/builder images containing their
configured shell-free executors. Child-process credentials must be passed only
through explicitly allowlisted environment names; prefer secret references or
workload identity.

For a full remotely operated release pipeline, use
`scannerRelease.builder.backend=managed`. This is an explicit opt-in and does
not change API or UI behavior. It requires immutable coordinator, step, fixed,
quality, and integration images; primary and mirror registry target IDs; a
target-bound Docker configuration Secret; mandatory per-adapter credential
Secrets (fixed `config.json`; quality/integration registry plus remote-engine
mTLS files); signing profile and credentials (or signer workload identity); an
RWX workspace PVC; and distinct release-Job and
Buildx namespaces. The chart gives a dedicated coordinator service account
namespace-scoped Job permissions and binds it to a separate least-privilege
Buildx Role in the Buildx namespace. BuildKit, fixed, quality, integration, and
signing each use a distinct service account. The API and ordinary scan worker
remain token-free and receive no release signing or registry credential mount.

Generated release Jobs are release- and lane-labeled. Dedicated fixed, quality,
integration, and signer NetworkPolicies allow cluster DNS plus only
operator-configured destination CIDR/port pairs; no hostname or port-only
all-destination egress is generated. The chart fails rendering when a managed
image is mutable, a required adapter Secret or lane egress destination is
absent, NetworkPolicy is disabled, the two namespaces are equal, service
accounts or credential Secrets are reused across trust lanes, push is disabled,
or both required platforms are not configured. See the
[scanner release execution backend guide](scanner-release-execution-backends.md#managed-release-backend)
for the complete values example, Compose gate, RBAC boundaries, and production
qualification procedure.

See [Scanner release worker and scheduler](scanner-release-worker.md) for the
lease/retry model, executor JSON protocol, schedule configuration, hardened
deployment topology, and validation model. See the
[scanner release execution backend guide](scanner-release-execution-backends.md)
for local, BuildKit/buildx, Kubernetes Job, and managed full-release
configuration, resource enforcement, immutable bindings, and qualification. See the
[scanner release operations runbook](scanner-release-operations-runbook.md)
for staged enablement, incident response, backup/restore, and disaster
recovery.

Operator-requested local/push image builds use the separate durable
[Custom build workflow](scanner-custom-builds.md). Its Compose and (Enterprise) Helm worker
are independently opt-in; only that worker receives the custom-build engine
socket and registry-secret resolution capability.

## PostgreSQL 18 upgrade

Compose now pins PostgreSQL 18.6; Go database connections use pgx v5 through the
existing store interface. SQLite remains available for the Community laptop
path. PostgreSQL migrations acquire a database advisory lock so concurrent
control-plane starts cannot race schema changes.

Do not start PostgreSQL 18 against a PostgreSQL 16 data directory. The Compose
volume path is deliberately retained, with explicit PGDATA, so an incompatible
old volume fails visibly rather than appearing to be an empty new database.
For an existing installation: stop application/worker writes; take and verify
backups with the old database version; restore a logical dump into a separate
empty PostgreSQL 18 volume; start Wolf against that database; verify users,
repositories, findings, queued jobs and a scan/gate cycle; then switch the
service. Keep the old database and backups until acceptance. An in-place major
upgrade needs a separately tested pg_upgrade procedure. This source change
has not migrated any existing deployment or deleted any volume.

### Automatic scanner scheduling

Unset or `0` values for `scan_concurrency`, `heavy_scanner_concurrency`, and
`network_scanner_concurrency` select automatic sizing. Settings → General shows
host capacity and effective limits; `GET /api/v1/settings/scheduling` exposes the
same information. Positive values (1–32) impose per-scan ceilings. Changes apply
to new scans.

The Docker worker queries its selected daemon's CPU count and memory, including
remote daemons. It reserves 25% for Wolf, the database and the OS (at least one
CPU and 2 GiB where capacity permits). Tools claim CPU, memory, heavy-tool and
network slots together; queued tools do not occupy execution slots. Concurrent
scans in one worker share the daemon budget. The memory reservation matches each
container's cap, including the larger Bearer, gosec, Grype and CodeQL limits.

Compose defaults to two CPUs per tool and one scan worker. On a 16-CPU, 30-GiB
host this allows six tools, three heavy tools and four network tools per scan,
subject to available budget. `WOLF_SCAN_WORKERS` divides the budget between
Compose replicas; scale with that variable, not a separate `--scale` value.
Separate deployments/CLI processes on the same daemon do not coordinate budgets.
This is capacity sizing at scan start, not live host-pressure autoscaling; other
large workloads require additional capacity reservation or a dedicated host.
If Docker capacity cannot be read, Wolf reports the failure and falls back to
two tools, one heavy tool and two network tools. Kubernetes keeps conservative
or explicit limits and delegates placement to the cluster scheduler.

Tools declared `network_required` use `WOLF_SCANNERS_ALLOW_NETWORK` (default
`bridge`); other tools use `WOLF_SCANNERS_NETWORK` (default `none`). Semgrep needs
network access for its automatic rule configuration. Network policy is copied
per scan so concurrent scans cannot overwrite each other's permissions.

Go analyzers reuse module and build caches on `WOLF_SCANNERS_DB_VOLUME`. CodeQL's
image includes the Go toolchain and exposes bundled precompiled queries to the
runtime UID, avoiding a full query recompilation per scan. Mypy uses a writable
cache under `/tmp`. Detect-secrets scans archive snapshots without requiring Git,
skips the usual dependency/build directories, and avoids online verification and
KeywordDetector work whose findings Wolf already discarded. Its timeout is 15
minutes for large repositories; tool timeouts remain explicit in `tools.yaml`.

Semgrep retains findings when individual files cannot be parsed and reports the
scan as incomplete. GoKart 0.5.1 needs Wolf's checked-in compatibility patch for
the pinned `golang.org/x/tools` graph: run SSA prerequisites with the upstream
analysis driver and handle synthetic functions without a package. The installer
applies this patch in a temporary source copy without changing dependency pins
or the module cache. Wolf requests SARIF on stdout and distinguishes findings
exit 1 from execution failures and retains raw output. The patch also compares
source/sink positions instead of source-line text to preserve same-line findings.
Native image smoke tests run unsafe, same-line unsafe, safe, and invalid-source
fixtures. Older images still contain the crashing binary; rebuild
and qualify the default scanner image before rollout (current plan R02).
