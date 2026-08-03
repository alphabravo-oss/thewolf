# Scanner release worker and scheduler

The scanner release factory is isolated from normal code-scan execution. The
`wolf scan-worker` process continues to execute user scans. The new
`wolf scanner-release-worker` process consumes scanner discovery and build
records, evaluates periodic scanner supply-chain schedules, and reconciles
durable release rollouts.

## Process roles

```text
wolf scanner-release-worker --role=scheduler
wolf scanner-release-worker --role=discovery \
  --definition-commit=0123456789abcdef \
  --discovery-manifest=/definitions/tools.yaml \
  --discovery-lock=/definitions/scanner-lock.yaml
wolf scanner-release-worker --role=proposal \
  --proposal-executor=/usr/local/bin/wolf \
  --proposal-executor-arg=scanner-proposal-executor
wolf scanner-release-worker --role=notification \
  --notification-webhook-adapter=/opt/wolf-notification-adapters/webhook
wolf scanner-release-worker --role=alert \
  --policy-scope=global \
  --alert-interval=5m
wolf scanner-release-worker --role=build --executor=/opt/wolf-release-executor/runner
wolf scanner-release-worker --role=rollout
wolf scanner-release-worker --role=all \
  --proposal-executor=/usr/local/bin/wolf \
  --proposal-executor-arg=scanner-proposal-executor \
  --executor=/opt/wolf-release-executor/runner
```

- `scheduler` evaluates the daily discovery and weekly candidate clocks. It
  does not require a container socket or executor credentials.
- `discovery` claims queued discovery runs and resolves package, release, and
  image sources over HTTPS. It does not require a Docker socket or builder
  credentials.
- `proposal` claims candidates awaiting a definition update and runs a
  shell-free JSON proposal executor. It does not inherit process environment.
- `notification` delivers the transactional notification outbox through
  deployment-owned webhook, email, and SIEM adapters.
- `alert` evaluates policy-owned scanner release operational alerts under a
  period-scoped database lease. It has database-only access.
- `build` claims compatible queued build runs and executes their persisted DAG.
- `rollout` reconciles canary and stable cohort assignments, health, pause
  boundaries, and rollback using a dedicated rollout lease.
- `all` is appropriate for a single-node administrative installation. Separate
  roles are recommended for production.
- `--once` performs one schedule/claim pass for qualification and automation.

Build workers support the compatibility-preserving command executor plus
policy-bound local, BuildKit/buildx, and Kubernetes Job backends. See
[Scanner release execution backends](scanner-release-execution-backends.md)
for action coverage, fixed resources, the step-image protocol, sink-level
idempotency, deployment examples, and qualification.

The scheduler snapshots the enabled policy revision and immutable definition
commit when it enqueues work. Daily runs create a queued discovery aggregate.
Weekly runs create an `awaiting_definition` candidate aggregate; proposal
automation later binds it to a generated lock before a build can be queued.
When a completed discovery exists for the exact definition commit, policy
revision, and scope, the weekly candidate stores that discovery ID and a
durable selection descriptor. A weekly candidate fails closed when that exact,
complete discovery snapshot does not exist; the scheduler lease is left
reclaimable so the same period can be retried after discovery succeeds.

The selection also snapshots the active freshness decision. By default,
`maximum_stable_image_age` is `168h` (seven days). An unchanged weekly run is
an audited no-op only while the newest stable release is younger than that
limit. No stable release, an expired stable release, or
`force_weekly_rebuild: true` queues a complete eight-image rebuild even when no
tool version changed. The durable selection records `force_rebuild` and a
machine-readable `rebuild_reason`, so proposal and build workers cannot
reinterpret the scheduler decision. On-demand candidate requests always enter
the proposal/build workflow and never inherit the scheduled no-op shortcut.

Both schedules use database leases and period-scoped idempotency. On-demand
and security-triggered work use caller-scoped idempotency without waiting for
the periodic clock.

## Durable discovery behavior

The discovery worker loads a validated tools manifest and deterministic scanner
lock before it accepts work. The configured definition commit must exactly
match the immutable commit recorded on every claimed run. A mismatch is a
durable failed result; the worker never silently scans different definitions.

A worker claims the oldest queued run with an opaque lease token, then:

1. Decodes the persisted complete or selected-component scope.
2. Resolves items with global and per-host concurrency bounds.
3. Applies per-item timeout, classified retry/backoff, manual holds, and
   deterministic risk classification.
4. Heartbeats the claim and immediately cancels resolver contexts when an
   operator requests cancellation.
5. Recursively redacts URLs, evidence, errors, metadata, and resolver details.
6. Atomically stores the terminal state, coverage, all status counts, definition
   and lock digests, normalized update items, and the audit event.
7. Clears lease ownership only in the same transaction that commits the
   results.

Source failures are data, not lost work. If at least one selected source was
resolved, the durable run is `completed` with `error_class=partial_coverage`,
an explicit coverage fraction, unreachable/unsupported/unknown counts, and all
item results. This preserves compatibility with existing API/UI terminal states
while retaining the engine's partial semantics. Zero source coverage is
`failed`. Operator cancellation is `cancelled`.

An expired claim is requeued until `max_attempts` is exhausted. Exhausted claims
fail as `worker_lost`; a cancellation request on an abandoned claim completes
as cancelled. A stale worker cannot heartbeat or finalize after its lease is
reclaimed. Graceful shutdown drains the active claim for
`--discovery-drain-timeout`; after that deadline the lease is left for recovery
instead of accepting a late result.

Production discovery controls are:

```text
--discovery-max-concurrency=8
--discovery-per-host-concurrency=2
--discovery-item-timeout=30s
--discovery-source-attempts=3
--discovery-poll=2s
--discovery-heartbeat=10s
--discovery-lease-duration=45s
--discovery-drain-timeout=1m
```

The lease duration must exceed twice the heartbeat interval. Resolver retries
are separate from durable claim attempts: the former retry one upstream source;
the latter recover an entire abandoned run.

## Durable proposal behavior

Proposal generation is a durable external side effect, so it uses a dedicated
claim rather than running in the API or scheduler process. The oldest
`awaiting_definition` candidate is claimed with a random lease token. Only the
worker ID and exact token may heartbeat, release, or finalize that candidate.
The worker uses a stable candidate-scoped idempotency key for all retries.

While the executor is running, the worker:

1. Heartbeats the lease and cancels the child immediately if ownership,
   candidate state, or version changes.
2. Redacts credential-bearing URLs, stderr, and returned error details.
3. Resolves the candidate's selected item IDs against its exact discovery run
   and sends only redacted update facts, immutable required gates, risk, and a
   deterministic source timestamp to the executor.
4. Accepts exactly one bounded JSON result with a canonical lowercase Git
   object ID, sha256 lock digest, valid risk JSON, and credential-free immutable
   proposal and lock references.
5. Atomically records the generated definition fields, clears lease ownership,
   appends the audit transition, and advances the candidate to `queued`.
6. Creates the deterministic build-plan DAG. A restart after candidate
   finalization but before plan creation is repaired by the worker's queued
   candidate reconciliation pass.

Expired claims are reclaimed until `proposal_max_attempts` is exhausted.
Exhaustion leaves the candidate in a durable failed state with
`proposal_error_class=worker_lost`. A stale executor result cannot finalize
after another replica has reclaimed its lease. Shutdown drains the active
executor for `--proposal-drain-timeout`; at the deadline it cancels the child
and leaves the lease for safe recovery.

Production controls are:

```text
--proposal-executor=/usr/local/bin/wolf
--proposal-executor-arg=scanner-proposal-executor
--proposal-executor-max-output=4194304
--proposal-poll=3s
--proposal-heartbeat=10s
--proposal-lease-duration=45s
--proposal-drain-timeout=1m
```

The proposal lease duration must exceed twice the heartbeat interval.
`--proposal-executor-env` is repeatable and is the only way to pass an
environment variable to the child. The named variable must exist in the worker
environment; its value is never placed in an argument or request JSON.

### Managed Git proposal generation

The shell-free JSON executor boundary remains the deployment contract. Wolf
ships `wolf scanner-proposal-executor`, which composes
`scannerproposal.Managed`, `scannerproposal.CheckoutGenerator`, the
server-resolved selected-update editor, and the bounded GitHub provider. The
durable worker—not the child and not the API caller—resolves stored discovery
item IDs, expands base/Rust compatibility groups, and supplies the immutable
risk and required-gate snapshots. The executor fails closed when that data is
absent or an update is held, stale, unsupported, or unselected.

Generation is failure-atomic with respect to the Git provider:

1. Require a full lowercase 40-character definition commit.
2. Clone a configured local object store or fetch the exact commit from a
   credential-free HTTPS repository URL into a new temporary directory. Git
   hooks and global/system configuration are disabled. HTTPS credentials are
   passed only through the child process environment as a transient Git header,
   never in the URL, request JSON, command arguments, logs, or persisted data.
3. Check out the exact requested commit in detached mode and verify `HEAD`.
   The source repository's worktree, index, and uncommitted files are never
   copied.
4. Reject symlinks, apply only server-resolved selected edits, regenerate
   `scanners/TOOLS.md`, refresh the canonical resolved scanner lock, and
   regenerate the complete embedded scanner/fixer context without a shell.
5. Run manifest validation, generated-doc checking, byte-for-byte parity
   checking, and resolved-lock checking.
6. Collect a sorted, bounded file set relative to the exact base. Added,
   modified, and deleted definition files are represented explicitly; changes
   outside `scanners/`, the embedded context, and scanner bump changelogs are
   rejected.
7. Derive tool, base-image, and toolchain changes from the base and proposed
   locks and reject any lock mutation not accounted for by the selected update
   set. Go toolchain updates bind both Linux archive checksums; grouped base
   updates rewrite all affected owned scanner/fixer Dockerfiles.
   Canonicalize risk JSON and render a deterministic pull-request body
   containing exact lock/definition/file-set digests, the policy gate plan,
   validation commands, source evidence links, and explicit pending-evidence
   placeholders.
8. Validate the complete Git proposal again before calling any provider
   method. Only then may the provider create blobs, a compare-and-swap branch,
   or a pull request.

Candidate selection is never placed in argv. The only subprocesses owned by the
managed generator use fixed argument vectors for Git and `go run
./cmd/scannertools`; the built-in proposal executor is invoked through JSON
stdin without a shell. Existing proposal branches require
their exact observed head before an update, and the GitHub provider always uses
`force=false`, so retries cannot overwrite operator changes.

The generated pull request intentionally marks build, signature, provenance,
and other durable policy evidence as pending. Proposal-generation validation is
reported separately and never claims that later image gates have passed.

## Durable build behavior

A worker claims the oldest build whose complete platform matrix is supported
by its repeated `--platform` flags. The claim has an opaque lease token which
never leaves the worker. The worker:

1. Loads the candidate's definition commit, lock digest, and immutable policy
   revision.
2. Rejects missing, disabled, or mismatched policy/lock inputs.
3. Reconstructs and validates the DAG from persisted step metadata.
4. Creates an ephemeral workspace and removes it after the claim finishes.
5. Runs only dependency-ready steps, with `--max-parallel-steps` as a hard
   bound and concurrency keys preventing unsafe publication overlap.
6. Heartbeats the build lease during execution and checks ownership before
   every result mutation.
7. Stops publishing immediately if the lease expires or transfers.
8. Cooperatively cancels the executor when an operator requests cancellation.
9. Creates a new persisted attempt only for a retryable step and never reruns a
   completed attempt.
10. Stores bounded, recursively redacted structured evidence and content
    digests before completing a step.

A claimed-but-not-started build is requeued after worker loss. A running build
is failed as `worker_lost`; it is not silently replayed. An operator or control
service creates a new build attempt when a whole-build retry is appropriate.

## Durable rollout behavior

The rollout controller is a separate control loop; neither the API process nor
the code-scan worker performs rollout work. It claims the oldest active rollout
using a database row keyed by rollout ID and an opaque lease token. Claim
heartbeat and release require the exact worker ID and token. An expired active
claim can be taken over by another replica, which appends a
`rollout.reclaimed` audit event. The new replica always re-reads persisted
rollout/cohort state and observed runtime state before acting.

Business state and controller ownership are intentionally separate:

```text
pending -> preparing -> canary -> verifying -> rolling_out -> completed
                 \          \             \               \
                  paused     rolling_back -> rolled_back    failed
```

One reconciliation pass performs at most one durable boundary:

1. Validate the immutable policy snapshot and destination/rollback releases.
2. Re-evaluate the maintenance gate. A closed required window pauses before
   assignment or promotion; rollback is never blocked by maintenance.
3. Persist `assigning`, an observation start, and a cohort deadline.
4. Invoke the idempotent runtime assignment using an operation ID derived from
   rollout, cohort, and immutable release ID.
5. Persist `observing` and point-in-time aggregate health.
6. Require every active worker to observe and verify the desired release.
7. Apply minimum samples/observation and infrastructure, parser, pull,
   signature, manifest, finding-loss, crash-loop, and duration gates.
8. Promote cohorts sequentially only after the canary verifies.
9. On a configured automatic health failure, restore cohorts in reverse order
   to the prior rollback-eligible release.

Cohort health snapshots, desired/observed releases, worker counts, deadlines,
and lifecycle timestamps survive controller restarts. Runtime mutations are
safe to retry because `scannerrollout.Runtime.Assign` receives a stable
`AssignmentRequest.OperationID`; implementations must honor that idempotency
key and context cancellation. `Runtime.Health` is read-only. The built-in
`WorkerStatusRuntime` updates and observes durable worker release status and
remains the compatibility default. It does not mutate a deployment. Managed
deployments select the concrete `compose` or `kubernetes` backend; both wrap
the same worker-status health contract, so API/UI behavior and existing worker
heartbeats are unchanged.

Pause and resume are optimistic API transitions. The heartbeat detects a state
or version change and cancels an in-flight runtime call. Paused rollouts remain
eligible for lifecycle-only reconciliation, allowing a failed deployment pause
to retry durably. Deployment-aware runtimes receive idempotent pause/resume
operations for every accepted cohort. Rollback first cancels candidate cohort
deployment state and then restores the prior exact release in reverse cohort
order. A first deployment with no prior release is failed explicitly because it
cannot be restored. Manual and automatic rollback ignore maintenance windows
but still require exact-token ownership and observed convergence.

### Synthetic verification

The verification stage uses the embedded, signed
`wolf.scanner-fixture-corpus/v1` corpus. It contains clean, known-vulnerable,
and parser-edge fixtures with pinned source digests and expected finding IDs.
Wolf verifies the corpus Ed25519 signature before worker startup and sends the
fixed corpus to a directly executed JSON adapter. The adapter receives:

- rollout, cohort, and stable assignment operation identities;
- exact release manifest and per-image digests/references;
- the corpus ID/digest and fixture expectations.

Its result must repeat every identity and report per-fixture parser status,
finding IDs, duration, and crash status plus pull, signature, and manifest
verification. Evidence is stored in worker release status under a reserved
synthetic cohort. Reuse requires the exact operation, release, current manifest,
current image-digest map, corpus digest, and an evidence timestamp at or after
assignment. A restart can therefore reuse current evidence, while stale or
rebound evidence is executed again. Only bounded aggregate parser, finding,
performance, signature, pull, manifest, crash, and infrastructure metrics feed
the existing canary thresholds. Adapter stderr is redacted in transient errors;
durable infrastructure failures contain only a generic class.

### Compose deployment backend

The Compose backend requires an absolute durable state root and a directly
executed reload/readback adapter. Before changing desired state it resolves the
published release, rejects malformed/mutable OCI repositories, constructs
exact `repository@sha256` references, pulls every image, and confirms the
Docker cache reports each digest. Desired assignment, lifecycle state, and
observed assignment are separate atomic files. Success requires adapter
readback of the exact operation, release, manifest, and image-digest map.
Restart reuses that observed state; failed reload restores the prior desired
and lifecycle files. Active scan rows are never rewritten, so overlapping
stable scans retain their original release snapshot.

The adapter receives one JSON object on stdin containing `action` (`apply`,
`paused`, `resumed`, or `cancelled`) and `assignment`. `apply` returns exactly
one bounded `DeploymentObservation`; lifecycle actions return no value.

### Kubernetes deployment backend

The Kubernetes backend authenticates with a service-account token file and CA
bundle over HTTPS. It pre-pulls exact references in bounded, per-operation
Pods and verifies both the requested image reference and runtime image digest.
It server-side-applies an assignment ConfigMap, patches every Deployment
labeled `wolf.dev/scanner-cohort=<cohort>`, and requires generation, replica,
operation, release, manifest, and image-digest readback before accepting the
assignment. Pause/resume/cancel are durable ConfigMap lifecycle states.
Rollback applies the prior release through the same pre-pull and readback path.

`InjectKubernetesJobAssignment` rejects mutable or out-of-release Job images,
then records the exact release/manifest/operation on Job and Pod annotations
and environment. The controller service account needs namespace-scoped
ConfigMap, Deployment, Pod, and Job permissions; no cluster-admin permission
is required.

The policy snapshot can fail closed on maintenance evidence with this optional
extension:

```json
{
  "maintenance": {
    "required": true,
    "window_open": false,
    "emergency_override_until": "2026-07-30T22:00:00Z"
  }
}
```

Existing snapshots without the extension retain their current behavior. An
emergency override must be a bounded timestamp persisted in the immutable
snapshot; an environment variable cannot bypass the gate.

Controller tuning:

```text
--rollout-poll=2s
--rollout-reconcile=15s
--rollout-heartbeat=10s
--rollout-lease-duration=45s
--rollout-cohort-timeout=1h
--rollout-worker-active-within=2m
--rollout-backend=worker-status

# Managed Compose:
--rollout-backend=compose
--rollout-compose-state-root=/var/lib/wolf/rollouts
--rollout-compose-adapter=/opt/wolf/bin/compose-rollout-adapter
--rollout-docker-path=docker

# Managed Kubernetes:
--rollout-backend=kubernetes
--rollout-kubernetes-api=https://kubernetes.default.svc
--rollout-kubernetes-namespace=wolf
--rollout-kubernetes-token-file=/var/run/secrets/kubernetes.io/serviceaccount/token
--rollout-kubernetes-ca-file=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt

# Required for either managed deployment backend:
--rollout-synthetic-adapter=/opt/wolf/bin/synthetic-scan-adapter
--rollout-synthetic-timeout=15m
```

The lease duration must exceed twice the heartbeat interval. The reconciliation
delay is persisted as claim availability, preventing a pending observation
from starving other rollout targets. `worker-status` intentionally remains the
default so an upgrade cannot reload existing Compose/Kubernetes workloads or
change existing UI scan flows. Selecting a managed deployment backend fails
startup unless its deployment settings and synthetic adapter are present.

## Durable notification behavior

Scanner release state changes and their notification records commit in the
same database transaction. A provider outage can therefore never roll back a
candidate, publication, registry observation, or rollout transition, and a
process crash cannot create an event without its matching notification.

Every qualifying event creates a delivered `ui:administrators` record. The
immutable policy revision associated with that event supplies optional
external destinations:

```json
{
  "notifications": {
    "destinations": [
      "webhook:security-operations",
      "email:release-approvers",
      "siem:primary"
    ]
  }
}
```

The portion after the colon is an opaque alias. Policy validation rejects URLs,
email addresses, control characters, and secret-shaped inline configuration.
Wolf stores no notification endpoint or credential. The adapter resolves the
alias using deployment-owned configuration and secrets.

Outbox payloads are capped at 8 KiB and are assembled from a fixed event
allowlist: event identity/type, aggregate identity, state boundary, sequence,
time, immutable policy revision, and a small type-specific count/status. Actor,
operator reason, raw event payload, errors, URLs, and credentials are not
copied. Error diagnostics are redacted and capped separately.

The built-in event mapping covers critical update discovery, candidate approval
readiness, gate/build failure, publication, canary start/pass/fail, rollout
pause/rollback/completion, mirror drift, and release revocation health. A
future exception-expiration scheduler can emit its own domain event into the
same outbox without changing adapter or delivery semantics.

The worker claims only external rows. Ownership requires a random lease token,
worker ID, and unexpired lease; heartbeat and finalization require the exact
tuple. An interrupted claim is reclaimed after expiry. Delivery uses the
notification ID as a stable provider idempotency key, because a provider can
accept a message immediately before a worker loses its database connection.

Retryable failures use exponential backoff with deterministic 0.75–1.25 jitter,
capped by `--notification-max-backoff`. A permanent adapter rejection or an
exhausted attempt budget enters `dead_letter`. Dead letters remain visible
until an administrator retries them with an ETag, reason, and idempotency key:

```text
wolf scanner notification list --state=dead_letter
wolf scanner notification show <notification-id>
wolf scanner notification retry <notification-id> \
  --if-match=7 \
  --reason="destination mapping repaired" \
  --idempotency-key=incident-421-retry
```

The equivalent API controls are:

```text
GET  /api/v1/scanner-supply-chain/notifications
GET  /api/v1/scanner-supply-chain/notifications/{id}
POST /api/v1/scanner-supply-chain/notifications/{id}/retry
```

The retry request requires `If-Match`, `Idempotency-Key`, and
`{"reason":"..."}`. It resets the delivery attempt counter without changing
the original event, payload, destination, or immutable policy identity. Every
claim, retry, delivery, dead letter, stale reclaim, and operator retry is also
an append-only scanner release audit event.

Each adapter is a directly executed binary, never a shell command. Wolf writes
one JSON object to standard input:

```json
{
  "schema_version": "wolf.scanner-notification-delivery/v1",
  "notification_id": "notification-id",
  "idempotency_key": "notification-id",
  "notification_type": "release_published",
  "destination_type": "webhook",
  "destination_ref": "security-operations",
  "attempt": 1,
  "payload": {
    "schema_version": "wolf.scanner-notification/v1"
  }
}
```

A successful adapter returns exactly one bounded JSON value:

```json
{"status":"delivered","provider_message_id":"optional-provider-id"}
```

Non-zero exit, timeout, or cancellation is retryable. Missing adapters,
unsupported destination types, malformed/multiple/oversized output, and any
status other than `delivered` are permanent. Child processes receive no
inherited environment except variables explicitly named with repeatable
`--notification-adapter-env`; use workload identity or secret-file variables.

Production controls are:

```text
--notification-webhook-adapter=/opt/wolf-notification-adapters/webhook
--notification-email-adapter=/opt/wolf-notification-adapters/email
--notification-siem-adapter=/opt/wolf-notification-adapters/siem
--notification-adapter-max-output=65536
--notification-poll=2s
--notification-heartbeat=10s
--notification-lease-duration=45s
--notification-delivery-timeout=30s
--notification-drain-timeout=1m
--notification-base-backoff=15s
--notification-max-backoff=30m
```

The lease duration must exceed twice the heartbeat interval. On graceful
shutdown the worker drains its active adapter call. At the drain deadline it
cancels the child and deliberately leaves the claim for lease recovery rather
than committing an uncertain result.

## Observability and readiness

Every release-factory process exposes a dependency-free Prometheus endpoint and
role-aware health endpoints on a dedicated listener:

```text
WOLF_SCANNER_RELEASE_OBSERVABILITY_ADDR=:9091

GET /metrics
GET /health
GET /ready
```

Use `--observability-address=<host:port>` to override the listener, or set it to
`off` for an installation that provides an equivalent sidecar endpoint. The
listener is enabled for continuously running workers and is not started for
`--once` executions. Bind it to a private administration network; the endpoint
contains no credentials or work identifiers, but it exposes operational state.

The main API also exposes the same process-local metric family at
`GET /api/v1/metrics`, and includes a `release_factory` snapshot in
`GET /api/v1/health` and `GET /api/v1/ready`. API replicas normally report all
release roles as `disabled`; that is healthy because work is delegated to the
dedicated role deployments.

The worker listener distinguishes:

- `disabled`: the role is intentionally not present in this process.
- `active`: all enabled roles and the database are ready.
- `database_unavailable`: the persistence dependency cannot be pinged.
- `degraded`: an enabled role is starting, stopped, degraded, or has stale or
  stuck lease work.

`/health` is liveness and always returns HTTP 200 with the diagnostic snapshot.
`/ready` returns HTTP 503 when the database is unavailable or any enabled role
is degraded. Disabled roles remain ready. A role with non-zero
`expired_lease` or `lease_lost` work reports `stale_or_stuck` until
reconciliation clears the gauge.

Metric families are intentionally bounded:

```text
wolf_scanner_release_component_runs_total{component,result}
wolf_scanner_release_component_run_duration_seconds_{sum,count}{component,result}
wolf_scanner_release_claims_total{component,result}
wolf_scanner_release_lease_events_total{component,result}
wolf_scanner_release_retries_total{component,reason}
wolf_scanner_release_results_total{component,state}
wolf_scanner_release_component_state{component,state}
wolf_scanner_release_stuck_work{component,kind}
wolf_scanner_release_database_ready
wolf_scanner_release_component_ready{component}
wolf_scanner_release_queue_depth{component,state}
```

The only component labels are `scheduler`, `discovery`, `proposal`, `build`,
`rollout`, and `notification`. Result, claim, lease, retry, state, queue, and
stuck-work labels use fixed enumerations. Candidate IDs, release IDs, periods, scanner names,
versions, hosts, errors, and lease tokens are never metric labels. Unknown
values collapse into a fixed fallback label instead of increasing cardinality.

Each replica reports only its own counters and gauges. Prometheus should scrape
every replica and aggregate by `component`; do not treat the absence of one
replica as proof that no work exists. Alert on database readiness immediately,
on component readiness after the deployment's startup grace period, and on a
non-zero stuck-work gauge for longer than the configured lease duration plus
two heartbeat intervals. Pair alerts with the operations runbook and durable
database/audit records when determining the affected work item.

## Proposal executor protocol

The proposal executor is a binary, not a shell command. Wolf writes exactly one
request to standard input:

```json
{
  "candidate_id": "candidate-id",
  "definition_commit": "0123456789abcdef0123456789abcdef01234567",
  "selection": {
    "mode": "complete"
  },
  "policy_id": "policy-id",
  "policy_revision": 4,
  "idempotency_key": "candidate-period-key/proposal"
}
```

It must return exactly one result:

```json
{
  "proposed_commit": "fedcba9876543210fedcba9876543210fedcba98",
  "proposal_url": "https://github.example/acme/scanners/pull/42",
  "lock_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "lock_uri": "oci://registry.example/wolf/locks@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "risk_summary": {
    "highest_risk": "low",
    "changes": []
  },
  "images": [
    {
      "key": "default",
      "platforms": [
        "linux/amd64",
        "linux/arm64"
      ]
    }
  ]
}
```

Unknown fields, multiple JSON values, oversized output, unsafe URLs, invalid
digests, noncanonical commits, and non-zero exits are rejected. The executor
must honor the request idempotency key and must not embed credentials in URLs.

The in-process managed adapter composes an injected deterministic definition
generator with the GitHub Git Data API provider. It checks the exact base
commit, writes complete file contents into a new tree, never force-pushes, and
only updates an existing automation branch when its head equals the expected
head. A moved or human-edited branch stops automation for review. It reuses an
existing open pull request and can set a candidate commit status. Deployments
may instead use the generic command executor to create a patch bundle for a
sovereign or offline review process.

For GitHub, provide a short-lived fine-grained token or workload identity with
only repository contents, pull-request, and optional commit-status permissions.
Do not grant administration, workflow, or force-push permission.

## Build executor protocol

The executor is a binary, not a shell command. Wolf starts it with the
ephemeral workspace as its working directory, writes one JSON request to
standard input, and reads one JSON result from standard output. Unknown fields,
multiple JSON values, oversized output, or a non-zero exit fail the step.

Example request (abridged):

```json
{
  "build_run_id": "build-id",
  "candidate_id": "candidate-id",
  "build_attempt": 1,
  "step": {
    "key": "lock-reproducibility",
    "kind": "validation",
    "depends_on": ["update-source-recheck"],
    "timeout": 120000000000,
    "retryable": false,
    "required": true
  },
  "step_attempt": 1,
  "workspace": "/workspace/wolf-scanner-release-build-id-123",
  "definition_commit": "0123456789abcdef",
  "lock_digest": "sha256:...",
  "policy_id": "policy-id",
  "policy_revision": 4,
  "platforms_json": "[{\"key\":\"core\",\"platforms\":[\"linux/amd64\"]}]"
}
```

Example result:

```json
{
  "output_uri": "oci://registry.example/wolf/evidence@sha256:...",
  "output_digest": "sha256:...",
  "summary": {
    "checks": 49,
    "status": "passed"
  },
  "retention_class": "candidate-evidence",
  "protected": false,
  "verification": {
    "lock_digest": "sha256:..."
  }
}
```

Trust-boundary steps have additional required verification:

- `checkout`: exact definition commit and lock digest.
- `lock-reproducibility`: exact lock digest.
- `policy-evaluation`: exact policy ID/revision plus normalized
  `policy_input`. The trusted worker, not the executor, calculates and
  overwrites the policy outcome and decision digest.

Example policy input:

```json
{
  "policy_input": {
    "risk": "low",
    "changes": [
      {
        "component": "semgrep",
        "kind": "patch",
        "from": "1.2.3",
        "to": "1.2.4"
      }
    ],
    "gates": [
      {
        "name": "smoke",
        "status": "passed",
        "evidence_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
      }
    ],
    "exceptions": [],
    "evidence": {
      "vulnerabilities": {
        "database_identity": "sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
      }
    },
    "maintenance_window_open": true
  },
  "verification": {
    "policy_id": "policy-id",
    "policy_revision": 4
  }
}
```

Risk and change data must match the persisted proposal when both are present.
Required gate evidence, exceptions, maintenance state, and vulnerability
database identity are schema checked and evaluated against the immutable policy
revision. The executor cannot authorize itself by returning an outcome or
decision digest.

The executor receives no inherited environment by default. The CLI allows only
`PATH`, `SSL_CERT_FILE`, and `SSL_CERT_DIR`; add a variable explicitly with
`--executor-env`. Prefer short-lived credential files or workload identity over
environment secrets. Output is redacted again before persistence, but redaction
is defense in depth and is not permission to print credentials.

## Schedule configuration

```text
WOLF_SCANNER_DEFINITION_COMMIT=<immutable Git commit>
WOLF_SCANNER_RELEASE_TIMEZONE=America/New_York
WOLF_SCANNER_RELEASE_DAILY_TIME=02:00
WOLF_SCANNER_RELEASE_WEEKLY_TIME=03:00
WOLF_SCANNER_RELEASE_WEEKLY_DAY=Sunday
WOLF_SCANNER_RELEASE_POLICY_SCOPE=global
```

Times use the organization IANA timezone. Logical period keys are local dates,
so daylight-saving gaps and repeated hours still enqueue once. Jitter is
deterministic. Catch-up windows suppress stale periods, while an expired active
lease can be recovered by another replica. A completed period is terminal.

## Deployment

Compose exposes the scheduler, discovery worker, builder, and rollout
controller under the opt-in `scanner-release` profile:

```bash
WOLF_SCANNER_DEFINITION_COMMIT="$(git rev-parse HEAD)" \
  docker compose --profile scanner-release up -d
```

The standard image contains its immutable `tools.yaml` and
`scanner-lock.yaml` under `/usr/share/wolf/scanners`. A custom definition can
be mounted and selected with `WOLF_SCANNER_DISCOVERY_MANIFEST` and
`WOLF_SCANNER_DISCOVERY_LOCK`; its configured definition commit must identify
that exact content.

Proposal automation is a separate opt-in profile because its Git credentials
and repository destination are privileged. The profile builds and runs Wolf's
`proposal-runtime` image; no host-mounted runner is required:

```bash
WOLF_SCANNER_PROPOSAL_GITHUB_OWNER=acme \
WOLF_SCANNER_PROPOSAL_GITHUB_REPOSITORY=wolf \
WOLF_SCANNER_PROPOSAL_GITHUB_TOKEN_FILE=/run/secrets/wolf-github-token \
  docker compose \
    --profile scanner-release \
    --profile scanner-release-proposal \
    up -d
```

The worker invokes `/usr/local/bin/wolf scanner-proposal-executor` with one
bounded JSON request on standard input. Configure the credential file as a
Compose secret or read-only secret mount. `WOLF_SCANNER_PROPOSAL_GITHUB_TOKEN`
exists for local development only; production deployments should use a secret
file or workload identity. The repository URL must be credential-free HTTPS.

Notification delivery is a separate opt-in trust boundary:

```bash
docker compose --profile scanner-release-notifications up -d
```

Install adapter executables under
`WOLF_SCANNER_NOTIFICATION_ADAPTERS_HOST` first. The default is
`./deploy/scanner-notification-adapters`; the directory contains the protocol
contract but no network client or secret.

The Helm chart has independent `scannerRelease.scheduler.enabled`,
`scannerRelease.discovery.enabled`, `scannerRelease.proposal.enabled`,
`scannerRelease.notification.enabled`,
`scannerRelease.rollout.enabled`, and `scannerRelease.builder.enabled`
controls. Discovery optionally mounts
`scannerRelease.discovery.existingConfigMap`;
that ConfigMap must contain the configured manifest and lock keys. PostgreSQL is
required for multi-replica production scheduling, discovery, and building. The
proposal and builder images must contain their configured executor binaries.
Notification delivery requires a dedicated image plus at least one configured
adapter path. Its pod is non-root, read-only, token-free, capability-free, and
has an explicit list of adapter egress ports when NetworkPolicy is enabled.
The chart requires `image.digest` and `postgres.digest` (or an already combined
`image@sha256` PostgreSQL reference) unless an operator explicitly enables the
development-only mutable-image escape hatches. Network-required scanner Jobs
remain egress-denied until `networkPolicy.scannerEgressCIDRs` is populated.
Use `scannerRelease.proposal.executorEnvironment` with `secretKeyRef` entries
for the smallest necessary credential set. Neither the API nor the discovery
deployment needs a Docker socket. The built-in `kubernetes-job` backend remains
appropriate for its advertised offline/evidence subset. A complete release
pipeline uses `scannerRelease.builder.backend=managed`: a digest-pinned
coordinator and step image, three digest-pinned lane adapter images, an RWX
workspace PVC, a dedicated release-Job namespace, a separate Buildx namespace,
primary/mirror registry target IDs, and configured signing. The standard
runtime image contains real Docker Buildx at
`/usr/libexec/docker/cli-plugins/docker-buildx`; no Docker socket is mounted.
Quality and integration Jobs likewise use no host socket: their dedicated
credential Secrets bind them to an operator-managed remote Docker Engine over
mTLS. Build the three lane images from the Dockerfile's
`scanner-release-{fixed,quality,integration}-adapter` targets and deploy their
resolved multi-platform manifest digests.
The fixed lane also requires its own Secret containing target-bound
`config.json`; adapter workload identity supplements but never replaces any of
the three adapter Secrets. Each generated Job is labeled with its Helm release
and `wolf.security/lane`. Managed Helm installs require explicit fixed, quality,
integration, and signer destination CIDR/port allowlists; lane-specific
NetworkPolicies add only those destinations plus selector-scoped cluster DNS.
The API and ordinary scanner service accounts remain token-free and receive no
managed-release Secrets or signer mounts. See
[Managed release backend](scanner-release-execution-backends.md#managed-release-backend)
for the complete deployment contract.

`scannerRelease.mode` controls API/UI capability exposure independently of
worker deployment. Its fail-closed default is `read_only`; staged
installations can use `read_only`, `candidate`, `canary`, then
`stable_control`. `scannerRelease.legacyBuildEndpoints` defaults to `true` so
upgrades do not break synchronous build callers. Once callers use durable
candidates, setting it to `false` retires those endpoints with `410 Gone`.
Restoring the prior mode and setting `legacyBuildEndpoints: true` is the
configuration rollback; persisted release-management state is unaffected.

## Validation and recovery

Run the deterministic and race tests:

```bash
go test -race ./internal/scannerdiscoveryworker ./internal/scannerreleaseworker ./internal/scannerreleasebackend ./internal/scannerreleasescheduler ./internal/scannerrollout
go test -race ./internal/scannerproposalworker ./internal/scannergit ./internal/scannerproposal
go test -race ./internal/scannernotification ./internal/scannernotificationworker
go test ./cmd/wolf ./internal/db
bash deploy/helm/wolf/tests/render-security.sh
bash deploy/compose/tests/managed-config-test.sh
```

The default test suite skips real deployment infrastructure. Explicit,
destructive-to-the-created-test-environment checks are gated:

```bash
WOLF_RUN_ROLLOUT_COMPOSE_E2E=1 \
  ./scripts/e2e/scanner-rollout-compose.sh

WOLF_RUN_ROLLOUT_KIND_E2E=1 \
  ./scripts/e2e/scanner-rollout-kind.sh
```

The Compose script launches exact-digest candidate and rollback containers and
exercises cache readback plus pause/resume/cancel. The Kind script creates and
deletes its own cluster, uses a namespace-scoped service account, and exercises
pre-pull, Deployment convergence, Job digest injection, lifecycle control, and
rollback. A pre-existing Kind cluster is never reused. Running neither script
is evidence of a production rollback drill.

Operational qualification should additionally:

- Run two scheduler replicas against PostgreSQL and confirm one aggregate per
  logical period.
- Start two rollout replicas, terminate the owner during canary observation,
  and verify a `rollout.reclaimed` event followed by state-derived recovery.
- Pause during assignment and verify the runtime context is cancelled before
  any cohort result is persisted under the stale rollout version.
- Force signature, manifest, pull, crash-loop, infrastructure, parser,
  expected-finding, duration, and deadline failures and verify reverse-order
  restoration of the prior release. Unit fault injection asserts logical and
  wall-clock recovery bounds, but does not replace a recorded operational
  recovery measurement.
- Kill a claimed worker before start and confirm requeue after lease expiry.
- Kill a running worker and confirm `worker_lost` failure without late results.
- Reclaim a discovery lease while a resolver is blocked and confirm no result
  is written by the stale worker.
- Run two proposal replicas and confirm exactly one external proposal call for
  a candidate.
- Terminate a proposal owner, reclaim its expired lease, and verify the late
  executor result cannot mutate the candidate.
- Finalize a proposal and stop before build-plan creation; verify queued
  candidate reconciliation creates exactly one deterministic DAG.
- Request discovery cancellation during retry backoff and confirm the run and
  all returned cancellation items are persisted without credential canaries.
- Run one complete and one selected discovery and reconcile total/status counts
  against the stored item rows.
- Request cancellation during a long executor step and confirm both step and
  build become cancelled.
- Re-run a retryable step and confirm the prior attempt and evidence remain.
- Inspect persisted evidence for credential canaries.
- Verify the builder's platform list covers every platform in the queued image
  matrix before enabling candidate automation.
- Scrape every scheduler and worker role, then stop its database dependency and
  verify `/health` reports `database_unavailable` while `/ready` returns 503.
- Expire and reclaim one schedule/worker lease; verify the bounded
  `reclaimed`, `stale_lease`, and `expired_lease` series change and the stuck
  gauge returns to zero after reconciliation.
- Make an adapter fail retryably, permanently, and after provider acceptance;
  verify delayed backoff, dead-letter visibility, stable provider idempotency,
  and ETag/idempotency-gated operator recovery.

## Durable operational alerts

The alert evaluator is a separate, opt-in worker role. Every replica derives
the same logical period from `--alert-interval` and competes for
`scanner-release-alert-evaluator/<policy-scope>`. Exactly one owner evaluates
that period. A completed period cannot be run again; an expired active lease
can be reclaimed with the same deterministic period identity.

Alert rules live in the immutable policy revision. All rules are disabled by
default, so enabling the worker alone does not change current behavior. An
enabled rule must have a validated threshold where one is required:

```json
{
  "alerts": {
    "missed_discovery": {"enabled": true, "after": "72h"},
    "stale_stable_release": {"enabled": true, "after": "168h"},
    "queue_backlog": {"enabled": true, "max_depth": 100, "max_age": "1h"},
    "lease_churn": {"enabled": true, "count": 5, "window": "15m"},
    "repeated_gate_failure": {"enabled": true, "count": 3, "window": "1h"},
    "mirror_drift": {"enabled": true},
    "rollout_failure": {"enabled": true},
    "signature_health": {"enabled": true}
  }
}
```

Durations use Go duration syntax and must be between one minute and 365 days.
Counts and queue depth are bounded at one million. The policy API rejects an
enabled duration/count rule with no usable threshold.

The evaluator covers missed discovery, stale stable releases, queue
depth/age, worker-loss and expired-lease churn, repeated gate/build failure,
mirror mismatch, latest rollout failure per target, and unhealthy signatures
on stable or revoked releases. Absence of a required baseline is `unknown`,
not healthy. It neither opens nor falsely resolves an alert.

One durable row exists per SHA-256 fingerprint of policy scope, alert kind,
and bounded resource scope. Policy revisions and evaluator replicas do not
create duplicates.
Continuing conditions update bounded evidence and trigger count without
emitting repeated lifecycle messages. Recovery emits `alert.resolved`; a
returning condition increments its generation and emits `alert.reopened`.
Opened, resolved, and reopened events atomically create administrator and
configured external notification outbox records.

Production controls are:

```text
--alert-interval=5m
--alert-heartbeat=30s
--alert-lease-duration=2m
--policy-scope=global
```

The lease duration must exceed twice the heartbeat interval. Inspect alerts
with:

```bash
wolf scanner alert list
wolf scanner alert list --severity=critical --kind=signature_health
wolf scanner alert list --state=all
wolf scanner alert show <id>
```

The API equivalents are `GET /api/v1/scanner-supply-chain/alerts` and
`GET /api/v1/scanner-supply-chain/alerts/{id}`. The overview includes
`alerts` counts and `alert_health`. Prometheus exposes
`wolf_scanner_release_alerts{severity="warning|critical"}` plus the standard
component run, lease, state, and readiness series with `component="alert"`.

For Compose, start the `scanner-release-alerts` profile. For Helm, set
`scannerRelease.alert.enabled=true`; the pod is non-root, read-only, has no
service-account token, and its NetworkPolicy permits only DNS and PostgreSQL.

See [Scanner release operations runbook](scanner-release-operations-runbook.md)
for staged enablement, alert response, backup/restore, disaster recovery,
capacity, and retention procedures.

## Durable registry reconciliation worker

`scanner-release-worker --role=registry` claims
`scanner_registry_jobs` using opaque lease tokens. Supported job kinds are:

- `reconcile`: exact manifest and OCI referrer evidence readback;
- `repair`: source verification, digest-idempotent OCI graph copy, optional
  policy-authorized re-signing, and exact destination readback;
- `cleanup`: retention- and reference-gated deletion of durable quarantine
  records.

The public job APIs are additive. The existing synchronous registry
`/reconcile` route remains available, so deploying the worker does not change
legacy UI behavior. The job detail response contains UI-ready job state and
per-image source/destination digest, signature, provenance, and SBOM evidence.
SSE events use aggregate type `registry_job`.

Production controls are:

```text
--registry-poll=2s
--registry-heartbeat=10s
--registry-lease-duration=45s
--registry-operation-timeout=30m
--registry-drain-timeout=2m
--registry-base-backoff=15s
--registry-max-backoff=30m
```

Source and destination clients are built only from configured registry
origins and opaque secret references. Bearer token hosts and upload
`Location` origins are allowlisted. A required re-sign policy without an
authorized adapter fails closed. An interrupted copy can be retried safely:
existing content-addressed blobs/manifests are verified and skipped, and the
partial destination is retained in quarantine for guarded cleanup.
