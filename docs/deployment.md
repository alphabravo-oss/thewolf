# Deploying The Wolf

Wolf ships as a Docker Compose stack. This guide covers running it on a public
server with TLS, and the hardened Docker-access path.

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

Exact-image API/worker qualification across native, Compose, and Kind
topologies is documented in
[`remote-scan-deployment-qualification.md`](remote-scan-deployment-qualification.md).

---

## TLS

Wolf itself speaks HTTP only — TLS is terminated by the bundled **Caddy** reverse
proxy (the `proxy` profile). Caddy sets `X-Forwarded-Proto`, which Wolf uses to
mark session cookies `Secure`. There are two modes, both driven by `.env`.

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

## Kubernetes native scanner Jobs

The Helm chart in `deploy/helm/wolf` deploys the API, PostgreSQL, durable scan
workers, RWX workspace/artifact claims, least-privilege service accounts/RBAC,
probes, NetworkPolicies, and optional worker autoscaling. Scanner tools run as
native per-tool Kubernetes Jobs; there is no Docker socket or DinD.

Requirements:

- A Kubernetes cluster with a default or explicitly selected RWX storage class.
- PostgreSQL. SQLite is intentionally rejected for Kubernetes workers.
- Scanner images reachable by cluster nodes.
- Immutable SHA-256 digests for the Wolf control-plane and PostgreSQL images.
- A 32-byte master key encoded as 64 hexadecimal characters.
- Worker egress to the Kubernetes API, PostgreSQL, DNS, and approved Git/SSH
  sources. Scanner Jobs are default-denied; only tools classified as
  network-required receive the chart's limited egress policy.

Install:

```bash
helm upgrade --install wolf deploy/helm/wolf \
  --namespace wolf --create-namespace \
  --set-string masterKey="$(openssl rand -hex 32)" \
  --set-string postgres.password="REPLACE_WITH_A_SECRET" \
  --set-string image.digest="sha256:REPLACE_WITH_64_HEX_CHARACTERS" \
  --set-string postgres.digest="sha256:REPLACE_WITH_64_HEX_CHARACTERS" \
  --set workspace.storageClassName="rwx-storage-class" \
  --set artifacts.storageClassName="rwx-storage-class"
```

For a headless deployment, add `--set apiOnly=true`. Set
`image.repository`/`image.digest` for Wolf and
`scanner.defaultImage`/`scanner.jvmImage`/`scanner.rustImage`/
`scanner.codeqlImage` for scanner images in your registry mirror.
Set `scanner.dockerHubMirror=mirror.gcr.io` to route Docker Hub-hosted
third-party scanner images through Google's public Docker Hub cache. That cache
does not contain every image/tag, so authenticated Docker Hub pulls or a
private pull-through cache are still the reliable options for strict
production environments.
The chart refuses a tag-only Wolf or PostgreSQL image by default.
`image.allowMutableTag` and `postgres.allowMutableImage` are development-only
escape hatches and should not be enabled in a qualified environment.

Ingress is off by default. The API NetworkPolicy already allows TCP 8778, so
enabling Ingress does not require a NetworkPolicy change:

```bash
helm upgrade --install wolf deploy/helm/wolf \
  --namespace wolf --create-namespace \
  --set-string masterKey="$(openssl rand -hex 32)" \
  --set-string postgres.password="REPLACE_WITH_A_SECRET" \
  --set-string image.digest="sha256:REPLACE_WITH_64_HEX_CHARACTERS" \
  --set-string postgres.digest="sha256:REPLACE_WITH_64_HEX_CHARACTERS" \
  --set ingress.enabled=true \
  --set-string ingress.className=nginx \
  --set-json 'ingress.hosts=[{"host":"wolf.example.com","paths":[{"path":"/","pathType":"Prefix"}]}]'
```

Network-required scanner Jobs also default to no egress. Populate
`networkPolicy.scannerEgressCIDRs` with the smallest approved destination set,
including the cluster DNS resolver CIDR where DNS is required. A port-only
all-destination scanner egress rule is never generated.

The API service account has token automount disabled and no scanner-Job RBAC.
Only the worker service account may create, inspect, and delete Jobs and read
pod status/logs. Scanner Jobs use a separate service account with token
automount disabled, non-root execution, read-only roots, dropped capabilities,
no privilege escalation, RuntimeDefault seccomp, read-only source mounts, and
bounded resources/timeouts.

The worker reconciles abandoned Jobs at startup using scan/tool/attempt/lease
labels. Completed Jobs are foreground-deleted after result collection and also
carry a TTL as a final cleanup backstop.

## Scanner release factory

Scanner image/toolchain updates use separate durable scheduler, discovery,
proposal, builder, and rollout-controller roles; they do not execute inside
the API process or the normal code-scan worker. Compose exposes the scheduler,
HTTPS-only discovery worker, builder, and database-backed rollout controller
through the opt-in `scanner-release` profile. Proposal execution has a separate
`scanner-release-proposal` profile because its repository write credentials
are a privileged trust boundary. The profile uses the built-in
`proposal-runtime` image and `/usr/local/bin/wolf scanner-proposal-executor`;
it does not require an installation-specific host executable. The Helm chart exposes independent
`scannerRelease.scheduler.enabled`, `scannerRelease.discovery.enabled`,
`scannerRelease.proposal.enabled`, `scannerRelease.rollout.enabled`, and
`scannerRelease.builder.enabled` deployments, all disabled by default.

The API and UI exposure is controlled independently from those worker
deployments:

| Compose environment | Helm value | Default | Effect |
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

Example staged Helm configuration:

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
`/usr/share/wolf/scanners`. Helm can instead mount an operator-managed ConfigMap.
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
[Custom build workflow](scanner-custom-builds.md). Its Compose and Helm worker
are independently opt-in; only that worker receives the custom-build engine
socket and registry-secret resolution capability.
