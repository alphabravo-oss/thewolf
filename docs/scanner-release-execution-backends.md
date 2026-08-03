# Scanner release execution backends

Scanner release builds run outside the API process. A durable
`scanner-release-worker --role=build` claims a persisted pipeline step and
dispatches it through either the original command executor or a built-in
secure backend.

The original behavior remains the default:

```bash
wolf scanner-release-worker \
  --role=build \
  --executor-backend=command \
  --executor=/opt/wolf-release-executor/runner
```

`command` keeps the existing bounded, shell-free `StepRequest`/`StepResult`
JSON contract. Existing installations do not change unless they explicitly
select a built-in backend.

## Backend selection

| Backend | Intended topology | Supported scope | External idempotency |
| --- | --- | --- | --- |
| `local` | Offline administration on a dedicated rootless container host | Checkout, validation, tests, scans, evidence, policy, and Compose integration | Does not advertise build, publish, sign, or mirror |
| `buildx` | Customer-managed BuildKit using the buildx Kubernetes driver | `build/<variant>/<platform>` plus the local partial action set | Build pushes use an operation-ID registry tag |
| `kubernetes-job` | Isolated elastic execution for the built-in offline/evidence action set and configured signing | Same partial set as local, plus signing when a signer is configured | Signing uses the operation-ID journal; publish and mirror are not advertised by the shipped step command |
| `managed` | Wolf-managed orchestration over Kubernetes Jobs, the buildx Kubernetes driver, and lane-specific adapters | Full configured release pipeline: build, fixed validation, quality, integration, signing, primary publication, and mirroring | Every external lane receives and must acknowledge the deterministic operation ID |
| `command` | Existing customer/managed executor | Defined by the existing executor | Existing compatibility contract |

An unsupported action fails explicitly. It is never converted into a shell
command and never falls through to a less restrictive backend.

Validate configuration and view the actual advertisement before starting a
worker:

```bash
wolf scanner-release-backend capabilities \
  --backend=kubernetes-job \
  --platform=linux/amd64 \
  --platform=linux/arm64
```

The JSON reports actions, step kinds, platforms, maximum resource capacity,
concurrency, cancellation, and idempotency guarantees.

## Shared security contract

Every built-in backend is wrapped by the same executor. Before execution it:

1. Requires a full lowercase definition commit, sha256 lock digest, positive
   policy revision, valid durable IDs, and a real absolute workspace.
2. Maps the persisted step key and kind through an exhaustive built-in policy.
   Unknown keys, kind mismatches, malformed components, and malformed
   platforms fail closed.
3. Computes an operation ID from build, candidate, attempts, action, commit,
   lock, and policy binding.
4. Requires the backend to advertise the exact action, kind, platform,
   capacity, resource controls, cancellation, and idempotency guarantees.
5. Applies the smaller of the persisted timeout and policy timeout and acquires
   a per-kind concurrency permit.
6. Sends candidate data in bounded JSON over stdin or a `0600` file. Candidate
   input and credentials are never interpolated into argv.
7. Requires the result to repeat the exact commit, lock, policy ID, and policy
   revision. Build, publish, signature, and mirror results must also repeat the
   external operation ID.
8. Bounds results to 4 MiB and logs to 64 KiB, redacts common secret forms,
   validates immutable digests/URIs, and atomically caches completed results.

Operation locks are reference counted and removed after their final waiter.
The workspace cache suppresses ordinary duplicate delivery but is not accepted
as proof that an external side effect is safe. Buildx puts the operation ID in
the registry tag. Kubernetes uses a deterministic Job and this PVC journal:

```text
<workspace-root>/.wolf-release-backend-journal/<operation-sha256>/
```

A restarted coordinator resumes that Job or returns its durable result. If a
start marker exists but both Job and result are gone, the backend returns an
ambiguous-result error and does not recreate the operation. Retain journals
through the protected-release and retry windows.

## Fixed resource policy

| Step kind | CPU | Memory | Ephemeral disk | Timeout | Concurrency |
| --- | ---: | ---: | ---: | ---: | ---: |
| checkout | 1 CPU | 1 GiB | 10 GiB | 10m | 2 |
| validation | 2 CPU | 2 GiB | 10 GiB | 15m | 4 |
| build | 4 CPU | 8 GiB | 50 GiB | 2h | 2 |
| test | 2 CPU | 4 GiB | 20 GiB | 1h | 4 |
| security | 2 CPU | 4 GiB | 20 GiB | 1h | 4 |
| evidence | 2 CPU | 2 GiB | 20 GiB | 30m | 4 |
| publish | 2 CPU | 2 GiB | 10 GiB | 1h | 2 |
| integration | 4 CPU | 8 GiB | 30 GiB | 90m | 1 |
| policy | 1 CPU | 1 GiB | 2 GiB | 10m | 4 |

Every current default-pipeline action is mapped:

```text
checkout
manifest-validate
generated-parity
update-source-recheck
lock-reproducibility
license-metadata
build/*
image-manifest/*
strict-version-smoke/*
invocation-smoke/*
parser-fixtures/*
normalized-golden/*
vulnerability-scan/*
secret-scan/*
license-scan/*
sbom/*
oci-annotations/*
provenance/*
candidate-publish/*
signature/*
published-verify/*
finding-regression
aggregate-sbom
mirror-copy-verify
compose-integration
kubernetes-integration
release-manifest
release-manifest-signature
policy-evaluation
candidate-evidence-summary
```

Adding an action requires a new explicit mapping and test. There is no
configurable "run this string" escape hatch.

## Step-image protocol

Local and Kubernetes use a digest-pinned step image and default to:

```text
/usr/local/bin/wolf scanner-release-step
```

Local sends `Invocation` JSON on stdin. Kubernetes passes only `--request` and
`--result` paths on the shared PVC. The payload binds the original
`StepRequest` to an action, resources, operation ID, definition commit, lock,
and policy. The result shape is:

```json
{
  "result": {
    "output_uri": "oci://registry.example/evidence/...",
    "output_digest": "sha256:bbbb...",
    "summary": {"status": "passed"}
  },
  "binding": {
    "definition_commit": "0123456789abcdef0123456789abcdef01234567",
    "lock_digest": "sha256:aaaa...",
    "policy_id": "global",
    "policy_revision": 12
  },
  "external_operation_id": "sha256:7d1c..."
}
```

`external_operation_id` is required for build, publish, signing, and mirror
operations. It means the action used that key at the registry, signing service,
or mirror—not merely that it copied the value into JSON.

The built-in `scanner-release-step` command revalidates the Invocation,
executes signature actions through the verified customer/managed signer
adapter, and bridges other non-side-effect actions to the existing executor binary at
`/usr/local/bin/wolf-release-command-executor`. It deliberately refuses build,
publish, and mirror actions because the legacy request cannot prove sink-level
idempotency. Signing uses a durable operation journal and exact external
operation-ID acknowledgement instead of the legacy request.

Consequently, the shipped Kubernetes backend defaults to the same partial
offline/evidence capability set and adds signature actions only when its signer
profile and adapter are configured. It does not advertise the complete release
DAG. A complete deployment must route build and registry side effects through a
sink-aware backend or use the command executor boundary until a first-party
sink-aware step runtime is configured.

## Local/offline backend

```bash
export WOLF_SCANNER_RELEASE_EXECUTOR_BACKEND=local
export WOLF_SCANNER_RELEASE_CONTAINER_ENGINE=/usr/bin/podman
export WOLF_SCANNER_RELEASE_STEP_IMAGE='registry.example/wolf-step@sha256:<64-hex>'
export WOLF_SCANNER_RELEASE_STEP_PROGRAM=/usr/local/bin/wolf
export WOLF_SCANNER_RELEASE_WORKSPACE=/srv/wolf/release-workspaces
wolf scanner-release-worker --role=build
```

The fixed container argv uses no network, exact CPU/memory and writable-layer
limits, a PID limit, read-only root, all capabilities dropped,
no-new-privileges, bounded tmpfs, and only the operation workspace writable.
Use a rootless engine. A runtime that cannot enforce a mandatory limit fails.

For an engine reached through a Compose socket, map the two namespaces:

```text
WOLF_SCANNER_RELEASE_WORKSPACE=/workspace
WOLF_SCANNER_RELEASE_HOST_WORKSPACE=/absolute/host/release-workspaces
```

The backend rejects workspaces outside the mapping. Start the isolated builder:

```bash
export WOLF_SCANNER_RELEASE_STEP_IMAGE='registry.example/wolf-step@sha256:<64-hex>'
export WOLF_SCANNER_RELEASE_WORKSPACE_HOST="$PWD/scanner-release-workspaces"
docker compose --profile scanner-release-builder up -d scanner-release-builder
```

Only this builder receives the configured engine socket. The API receives no
additional release-builder socket.

## BuildKit/buildx backend

```bash
export WOLF_SCANNER_RELEASE_EXECUTOR_BACKEND=buildx
export WOLF_SCANNER_RELEASE_BUILDX_PATH=/usr/libexec/docker/cli-plugins/docker-buildx
export WOLF_SCANNER_RELEASE_REGISTRY=registry.example/wolf/quarantine
export WOLF_SCANNER_RELEASE_BUILDX_PUSH=true
export WOLF_SCANNER_RELEASE_CONTAINER_ENGINE=/usr/bin/podman
export WOLF_SCANNER_RELEASE_STEP_IMAGE='registry.example/wolf-step@sha256:<64-hex>'
wolf scanner-release-worker --role=build \
  --platform=linux/amd64 --platform=linux/arm64
```

The backend verifies the checked-out lock digest and variant/platform, then
renders fixed buildx argv. Its Kubernetes BuildKit driver receives equal
requests and limits for CPU, memory, and ephemeral storage plus `rootless=true`.
BuildKit emits provenance and SBOM metadata. The deterministic operation
builder is removed on completion or cancellation. Non-build actions route only
to the local backend's advertised partial set.

Use workload identity or a mounted `DOCKER_CONFIG`; never put registry
passwords in build arguments, candidate metadata, or argv.

## Kubernetes Job backend

Recommended Helm values:

```yaml
scannerRelease:
  builder:
    enabled: true
    backend: kubernetes-job
    replicaCount: 2
    platforms: [linux/amd64, linux/arm64]
    stepImage: registry.example/wolf-step@sha256:<64-hex>
    stepProgram: /usr/local/bin/wolf
    maxParallelSteps: 4
    maxStepAttempts: 2
```

The coordinator uses the worker service account and shared RWX workspace PVC.
Its token exists only because this role manages release Jobs. The API has no
Job role, token, or Docker socket.

Each release Job has a deterministic name and invocation annotation; equal
requests/limits for CPU, memory, and ephemeral storage; a hard deadline, zero
backoff, and TTL; non-root/read-only/seccomp/capability restrictions; no
service-account token; a bounded temporary volume; and fixed path-only argv.

## Managed release backend

`managed` is the deployable full-release topology. The coordinator runs in the
Wolf release namespace, creates bounded release Jobs there, and asks buildx to
create transient BuildKit resources in a different namespace. Fixed-validation,
quality, integration, and signing lanes run through separately configured,
digest-pinned adapter images and dedicated service accounts. Neither the API
nor the ordinary scan worker receives release credentials, a Kubernetes token,
signer configuration, or Buildx permissions.

A representative Helm configuration is:

```yaml
scannerRelease:
  builder:
    enabled: true
    backend: managed
    image: registry.example/wolf/release-coordinator@sha256:<64-hex>
    stepImage: registry.example/wolf/release-step@sha256:<64-hex>
    buildxPath: /usr/libexec/docker/cli-plugins/docker-buildx
    push: true
    platforms: [linux/amd64, linux/arm64]
    managed:
      sourceURL: https://github.example/acme/wolf-scanners.git
      primaryRegistryID: primary-production
      mirrorRegistryID: disaster-recovery
      gitAuthorizationSecret: wolf-release-git       # optional
      gitAuthorizationKey: authorization
      dockerConfigSecret: wolf-release-primary-registry
      dockerConfigKey: config.json
      adapterPath: /usr/local/bin/wolf-release-adapter
      kubernetes:
        apiServer: https://kubernetes.default.svc
        namespace: wolf
        buildxNamespace: wolf-buildkit
        workspacePVC: wolf-release-workspace
        coordinatorServiceAccountName: wolf-release-coordinator
        buildkitServiceAccountName: wolf-release-buildkit
      networkPolicy:
        dns:
          namespaceSelectorLabels:
            kubernetes.io/metadata.name: kube-system
          # Set cluster-specific CoreDNS/kube-dns labels when available.
          podSelectorLabels: {}
        destinations:
          fixed:
            - cidr: 203.0.113.10/32       # primary registry endpoint
              ports: [{protocol: TCP, port: 443}]
          quality:
            - cidr: 203.0.113.20/32       # registry/DB/remote engine range
              ports:
                - {protocol: TCP, port: 443}
                - {protocol: TCP, port: 2376}
          integration:
            - cidr: 203.0.113.30/32       # registry/remote engine range
              ports:
                - {protocol: TCP, port: 443}
                - {protocol: TCP, port: 2376}
          signer:
            - cidr: 203.0.113.40/32       # KMS/HSM/keyless endpoint
              ports: [{protocol: TCP, port: 443}]
      adapters:
        fixed:
          image: registry.example/wolf/fixed-adapter@sha256:<64-hex>
          registryCredentialSecret: wolf-fixed-registry
          serviceAccountName: wolf-release-fixed
        quality:
          image: registry.example/wolf/quality-adapter@sha256:<64-hex>
          registryCredentialSecret: wolf-quality-registry
          engineCredentialSecret: wolf-quality-engine
          serviceAccountName: wolf-release-quality
        integration:
          image: registry.example/wolf/integration-adapter@sha256:<64-hex>
          registryCredentialSecret: wolf-integration-registry
          engineCredentialSecret: wolf-integration-engine
          workloadIdentity: true
          serviceAccountName: wolf-release-integration
          serviceAccountAnnotations:
            example.identity/role: wolf-release-integration
  signing:
    enabled: true
    adapterPath: /usr/local/bin/wolf-signer-adapter
    profileSecret: wolf-signer-profile
    credentialSecret: wolf-signer-credentials
    serviceAccountName: wolf-release-signer
    journalPath: /workspace/.wolf-signing-journal
```

Build and publish the three lane images independently. The target fixes the
lane in the compiled entrypoint; it is not a runtime argument:

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --target scanner-release-fixed-adapter \
  --tag registry.example/wolf/fixed-adapter:RELEASE --push .
docker buildx build --platform linux/amd64,linux/arm64 \
  --target scanner-release-quality-adapter \
  --tag registry.example/wolf/quality-adapter:RELEASE --push .
docker buildx build --platform linux/amd64,linux/arm64 \
  --target scanner-release-integration-adapter \
  --tag registry.example/wolf/integration-adapter:RELEASE --push .
```

Resolve the pushed multi-platform manifest digests and configure only
`repository@sha256` references. All three contain the checksum-pinned ORAS
publisher. The fixed target adds Git and `scannertools`. The quality target adds
`scannertools`, Docker CLI, and checksum-pinned Trivy. The fixed target also
contains the trusted Go toolchain and prebuilt `synccontext` helper needed for
its locked source/database reconciliation. The integration target adds Docker
CLI/Compose, kind, kubectl, jq, Python, two precompiled qualification test
binaries, the four checked-in release/scanner qualification entrypoints, and
their fixed Compose fixture helper; candidate checkout code is not compiled in
that credential-bearing lane. All three run as the non-root `wolf` user with
`/usr/local/bin/wolf-release-adapter` as their entrypoint and use `/tmp` as
writable home under a read-only-root Job.

The Helm release namespace must equal `managed.kubernetes.namespace`; the
Buildx namespace must already exist and must be different. The workspace PVC
must support the access pattern required by all coordinator replicas and Jobs
(normally RWX). The registry IDs identify persisted immutable registry targets;
they are not arbitrary hostnames. The primary registry target must use an
immutable repository identity. The mounted Docker `config.json` is bounded to
one `auths` entry whose key exactly equals that primary registry host. Git
credentials, when required, are a single read-only authorization file mounted
at the fixed target `/run/wolf/git/authorization`.

No quality or integration Job receives a host Docker socket. Both lanes use a
separately administered remote Docker Engine over mTLS. Each lane uses two
mandatory, distinct Secrets—even when workload identity is also enabled. The
registry Secret contains exactly `config.json`; the engine Secret contains:

```text
engine.json
ca.pem
cert.pem
key.pem
```

The isolated registry `config.json` is target-bound authentication and is
never mounted from the engine Secret. `engine.json` has this
bounded schema:

```json
{
  "schema_version": "wolf.scanner-release-engine/v1",
  "host": "tcp://engine.example:2376",
  "quality_network": "wolf-quality-fixtures",
  "quality_network_policy_digest": "sha256:<64 lowercase hex characters>",
  "quality_targets": {
    "nuclei": "http://wolf-quality-nuclei:8080/"
  }
}
```

The two `quality_network` fields are optional only as a pair. Omitting both
runs measured scanner qualification with `--network none`. When controlled
package indexes, vulnerability services, or loopback test targets are needed,
the named network must already exist on the remote engine, use the `bridge` or
`overlay` driver, have Docker's `Internal` flag enabled, and carry these exact
labels:

```text
dev.wolf.scanner-release.quality-network=true
dev.wolf.scanner-release.policy-digest=sha256:<the engine.json policy digest>
```

The adapter inspects those properties before pulling or running qualification
images and binds the network ID and policy digest into the signed quality
evidence. An ordinary bridge, a host network, a mutable network policy, or a
network whose labels drift fails closed. Fixture services on this network are
operator-owned, credential-free, immutable test endpoints; the network's
internal flag prevents internet egress.

`quality_targets` is optional unless a selected tool requires a non-source
target. The current schema accepts only `nuclei`, only an `http` URL with an
explicit unprivileged port, and only a `wolf-quality-*` service name on the
validated internal network. User information, IP literals, TLS/public hosts,
queries, fragments, and non-root paths are rejected. Dockle uses the
deterministic `/scan/dockle-image.tar` corpus artifact through its upstream
`--input` mode and never receives the Docker socket.

The runner rejects links, extra/missing files, oversized inputs, non-TCP
endpoints, and endpoints without an explicit host and port. It supplies
`DOCKER_CONFIG` from the registry mount and `DOCKER_HOST`,
`DOCKER_TLS_VERIFY`, and `DOCKER_CERT_PATH` from the engine mount only
to compiled Docker actions. The fixed lane does not contact an engine; its
credential Secret contains only `config.json`.

That fixed-lane Secret is mandatory even when fixed workload identity is
enabled. `ProductionRunner` obtains target-bound ORAS authentication from its
bounded `config.json`; workload identity may supplement but does not implement
that registry credential contract. The same supplement-only rule applies to
both credential boundaries in the quality and integration lanes.

The coordinator, BuildKit, signer, fixed, quality, and integration service
accounts must all be distinct. Git authorization, Docker configuration, signer
profile/credential, and adapter credential Secrets must also be distinct. A
signer credential Secret may be replaced by signer workload identity. Adapter
workload identity never replaces an adapter credential Secret; only an enabled
workload-identity lane gets its service-account token. Workload-identity
annotations belong on the corresponding lane service account, never on the
API, ordinary scanner, coordinator, or another lane.

Managed mode also requires `networkPolicy.enabled=true` and at least one
operator-owned CIDR/port destination for each of `fixed`, `quality`,
`integration`, and `signer`. Every generated Job and Pod carries
`app.kubernetes.io/instance=<Helm release>` plus the stable
`wolf.security/lane=ordinary|fixed|quality|integration|signer` label. Five
release-scoped policies select those exact labels. They allow DNS only to the
configured namespace/pod selectors and then add only the listed `ipBlock` and
port pairs; no port-only all-destination rule is emitted. `ordinary` is present
for the standalone Kubernetes Job backend and defaults to DNS-only. Endpoint
hostnames are not accepted as an egress boundary: operators must supply the
smallest routed CIDRs, use `except` for excluded subnets, and update the values
when controlled endpoint ranges change.

The chart deliberately creates two non-overlapping authorization planes:

- the release namespace Role lets only the coordinator create/read/watch/delete
  Jobs and read their Pods and logs;
- the Buildx namespace Role lets only the coordinator manage Buildx
  Deployments/StatefulSets and ConfigMaps and inspect/exec into BuildKit Pods.

The Buildx Role has no Secret or Job access. The release Job Role has no
Deployment, StatefulSet, ConfigMap, or Secret access. The BuildKit and lane
service accounts have no chart-created Kubernetes API permissions.

Compose exposes the same backend in the opt-in
`scanner-release-managed` profile. Populate the
`WOLF_SCANNER_RELEASE_MANAGED_*` host paths and the exact backend variables in
`.env`, then run the fail-closed configuration gate before starting it:

```bash
bash deploy/compose/tests/managed-config.sh
docker compose --profile scanner-release-managed up -d \
  scanner-release-managed-builder
```

The Compose gate checks HTTPS endpoints, digest-pinned step/adapter images, two
distinct Kubernetes namespaces, distinct lane identities and Secrets,
executable and regular bounded credential mounts, and the Docker configuration
shape. It requires the fixed `config.json` Secret reference even when fixed
workload identity is enabled. Compose builds the coordinator from the checked-out, pinned Dockerfile;
production image promotion should pin and attest that resulting image in the
normal deployment pipeline. The backend repeats the target-bound registry
check at startup. Do not treat a bare `docker compose config` as this security
gate; Compose's schema cannot express the cross-field and mounted-file
invariants. Compose-generated Jobs use
`WOLF_SCANNER_RELEASE_K8S_INSTANCE=wolf-compose` by default; external-cluster
NetworkPolicies must match that instance and the same `wolf.security/lane`
labels, with explicit egress controls equivalent to the Helm policies.

## Qualification

```bash
go test -race ./internal/scannerreleasebackend ./internal/scannerreleaseworker
go test -race ./cmd/wolf
bash deploy/helm/wolf/tests/render-security.sh
docker compose config --quiet
bash deploy/compose/tests/managed-config-test.sh
```

Tests cover exhaustive mapping, immutable binding, redaction, duplicate
results, lock-map churn, concurrency, cancellation, local argv/limits, buildx
driver limits and cleanup, Kubernetes manifest hardening, durable replay,
ambiguous restart failure, and offline rejection of build/publish/sign/mirror.

Before production enablement, run a quarantine candidate, cancel each step
kind, kill a coordinator during a Job and BuildKit operation, verify
operation-ID resume, verify registry/signing/mirror deduplication, scan logs
with credential canaries, confirm each lane receives only its own identity, and
compare reported limits with the runtime's observed workload. Also prove that
the API and ordinary scanner service accounts cannot read release Secrets,
create release Jobs, or manage BuildKit resources.
