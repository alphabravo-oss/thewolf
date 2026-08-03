# Durable custom-build UI

## Operator contract

The administrative scanner-management area exposes **Custom builds** at:

```text
/scanners?tab=custom_builds
```

The workspace is a client of the first-class durable resources under
`/api/v1/scanners/custom-builds`. It does not invoke the legacy synchronous
build stream. The existing Settings → Scanners inventory remains available,
and its established rebuild buttons now open the same durable create dialog.
After acceptance, Settings retains the operation ID and a link to the
URL-backed workspace.

An accepted create is a receipt, not a completed build. The build continues on
a worker after the dialog, route, browser tab, or API connection closes.

## Create behavior

An operator can choose:

- one of Default, JVM, Rust, or CodeQL, or all four variants;
- a local Docker load or registry push;
- `linux/amd64`, `linux/arm64`, or both for a pushed multi-architecture image;
- at most one supported platform for a local load;
- an optional registry namespace for push;
- a required audit reason.

Each create, cancel, and retry receives a fresh `Idempotency-Key`. Cancel and
retry also send the detail response ETag in `If-Match`, require a reason, and
require the operator to type the exact build ID.

CodeQL is a hard local-only boundary. The UI disables direct CodeQL push and
rejects push-all before submission, with an explanation that CodeQL cannot be
redistributed. A local all-variant build remains supported. Pushing Default,
JVM, and Rust is performed as separate one-variant operations because the
backend create contract accepts one or all, not an arbitrary subset.

The browser never requests or renders a credential value or credential
reference. Push credential resolution remains server-side.

## Durable detail and failure handling

The selected build ID and list state live in `custom_build` and
`custom_build_state` URL parameters. Refresh and browser reload therefore
restore the same resource and filter.

Detail shows only the allowlisted contract:

- operation ID, state, safe status-resource path, attempt, timestamps, actor,
  reason, requested variants/platforms, destination, and reserved version;
- per-variant state, image references, digest, local-load/push outcome, and
  timestamps;
- bounded error classes and remediation selected from known error-class
  families;
- worker heartbeat/lease timing only when needed to identify a stale active
  lease.

The adapter discards user IDs, secret references, idempotency keys, request
digests, worker IDs, raw summaries, raw terminal payloads, and raw build or
variant error details. Unknown variants, platforms, and malformed JSON fields
are not reflected into the UI.

Partial operations keep successful variant digests and outcomes visible while
identifying failed variants independently. Authorization and availability
failures use fixed operator guidance rather than reflecting backend detail.

The custom-build detail resource does not persist a trace ID or
operation-correlation ID. The create response does return both in validated
`X-Wolf-*` headers. The UI captures those receipt headers, retains them in
bounded URL state across reload, and offers an exact operation Audit link.
Actor and reason remain visible. Opening a build from the general list does
not fabricate correlation when its original create receipt is unavailable.

## Logs, reconnect, and polling

The log viewer uses a fetch-based SSE reader so an explicit reconnect can send
`Last-Event-ID`. It:

- de-duplicates by numeric event sequence;
- renders at most the latest 800 lines in the browser;
- limits each admitted line to 8,192 characters;
- strips unsafe control characters;
- ignores terminal JSON payloads;
- reconnects with bounded exponential delay;
- switches its visible status to polling fallback after three consecutive
  stream failures while continuing reconnect attempts.

Operation detail remains authoritative and polls every five seconds while the
build is nonterminal. Thus a log outage does not hide cancellation, failure,
partial completion, or final per-variant outcomes. A terminal build performs
one persisted log replay after reload and then stops.

The backend remains the durable log source and applies its own 4,000-line /
4 MiB retention bounds.

## Capability and compatibility behavior

Settings retains its existing Docker-versus-Kubernetes capability boundary.
When Docker image management is unavailable, the existing cluster-managed
message and action visibility remain unchanged. When it is available, image
inventory, digest status, doctor, pull, update, credential setup, and scanner
tool status behave as before.

The UI migration does not remove the server compatibility routes. They can be
disabled independently after non-UI callers have migrated, as described in
`scanner-release-management.md`.

## Automated evidence

The client and component suites cover:

- strict transport normalization and sensitive-field exclusion;
- create validation for CodeQL push-all and multi-platform local builds;
- local CodeQL creation without namespace or secret data;
- fresh idempotency and exact ETag headers;
- partial per-variant evidence and remediation;
- reason plus typed confirmation for retry;
- loading, empty, permission/error, and polling-fallback states;
- safe SSE parsing and terminal-payload exclusion.

The Playwright suite runs the workspace at 1440 × 1000 and 390 × 844. It
covers:

- top-level navigation, axe WCAG A/AA automation, and responsive containment;
- partial all-variant detail, secret canaries, and reload survival;
- running-log disconnect/reconnect with observed `Last-Event-ID`;
- push-all rejection and a supported two-platform registry submission;
- cancel and retry with `If-Match`, idempotency, reason, and typed build ID;
- the legacy Settings local-CodeQL entry, durable receipt, and operation link;
- the unchanged scanner Settings visual baseline before the new build is
  submitted.

Run from `ui/`:

```sh
pnpm typecheck
pnpm lint
pnpm test
pnpm test:e2e:typecheck
pnpm build
pnpm test:e2e
```
