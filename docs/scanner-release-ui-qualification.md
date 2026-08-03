# Scanner release UI qualification

## Status and scope

This document records the automated qualification currently implemented for
the administrative `/scanners` experience. It covers the top-level scanner
release panels, their bounded remediation links, scanner-management dialogs,
the Operations reliability dashboard, audit-correlation troubleshooting,
durable registry reconciliation/quarantine operations, and durable custom
scanner-image builds. It also records the bounded compatibility journeys for
Docker/Kubernetes-specific Scanner Settings and an ordinary API-created scan.

This evidence is not a WCAG certification and is not a claim that the product
has completed production accessibility review. Automated rules and Chromium
keyboard journeys cannot reproduce the behavior of a real screen reader,
browser accessibility tree, forced-colors mode, or every user configuration.

The registry and custom-build qualification consume their existing durable
APIs without changing those contracts. Existing scanner Settings behavior
remains covered by its original regression journey, including its new adapter
to durable Custom builds.

The fixed, quality, and integration adapter images are deployment-plane
dependencies, not members of the eight-image scanner/fixer release inventory.
Release Operations accepts only the bounded worker/lane component vocabulary
and never lets unknown components displace a known row. A lane row reflects
only health returned by the queried API; the UI does not infer readiness from
configuration, synthesize adapter releases, or mix adapter architecture
evidence into release details. In deployments where worker health endpoints
are not aggregated into the API, lane health remains available from the
worker's health/metrics endpoint and deployment runbook rather than being
claimed in the UI. Per-platform scanner/fixer evidence remains attached to the
immutable candidate/release manifest that owns it.

## Automated browser evidence

`ui/e2e/scanner-management.spec.ts` runs the complete suite in Chromium with:

- a 1440 × 1000 desktop viewport;
- a 390 × 844 mobile viewport;
- reduced motion enabled;
- deterministic scanner API fixtures with bounded failure and secret-shaped
  values;
- axe rules tagged WCAG 2 A/AA and WCAG 2.1 A/AA, including the automated
  `color-contrast` rule in both light and dark themes.

The tagged 12-panel semantic, axe, responsive-containment, and navigation
matrix additionally runs in desktop Firefox in both themes. Visual baselines
remain Chromium-only so engine-specific font rasterization cannot create
unreviewable snapshot churn.

The browser suite verifies:

- all 12 top-level panels load from their direct URL, retain one visible `h1`,
  expose accessible names for interactive and explicitly focusable elements,
  pass the selected axe rules in light and dark themes, and do not create
  page-level horizontal overflow;
- the horizontal scanner navigation is reachable and activatable using only
  `Tab` and `Enter`, exposes a visible focus indicator, preserves
  `aria-current`, and announces the selected panel;
- shared destructive dialogs focus the first meaningful field, trap forward
  and reverse tab movement, close with `Escape`, and return focus to the
  actual opener;
- custom legacy-import and signer create/rotate/revoke dialogs receive focus
  inside the dialog, retain accessible labels, and pass axe checks;
- wide update, candidate, release, signer, notification, comparison, and
  legacy-preview data regions are keyboard-focusable and internally
  scrollable at narrow widths;
- overview critical health, operational alerts, release-factory failures, and
  stuck queues expose direct, resource-scoped remediation links;
- read-only, candidate, canary, and stable-control capability stages disable
  actions at the expected authorization boundary;
- announced loading, bounded error, retry, empty, disabled, pending, and
  partial-data states retain independently available information;
- operational alert evidence, notification details, signer references, build
  telemetry, and audit events do not render unknown secret-shaped payload
  fields;
- build reliability shows allowlisted completed, partial, failed, cancelled,
  duration, and queue aggregates while ignoring unknown labels;
- rollout detail renders first-class synthetic corpus identity/currentness and
  fixture outcomes separately from sampled candidate/stable real-scan
  outcomes, bounded failure counts, finding-loss evidence, worker readiness,
  and p95 duration delta; internal corpus IDs and the retired mock-only
  verification-scan shape do not enter the DOM;
- exact trace and operation deep links filter audit history, malformed partial
  identifiers never reach the API, copy affordances announce success, and
  JSONL export preserves the active aggregate/event/actor/correlation scope.
- the complete registry job/quarantine journey runs at both viewports and
  covers URL-backed list/detail/filter state, exact manifest/signature/
  provenance/SBOM evidence, resumable events, audit links, dead-letter ETag
  retry, typed repair and cleanup confirmations, provisional cleanup
  eligibility, capability gating, axe checks, and responsive containment;
- registry worker summary, error detail, image detail, event payload, and
  quarantine metadata canaries never enter the DOM.
- the custom-build journey runs at both viewports and covers URL-backed
  list/detail state, reload survival, Last-Event-ID reconnect, polling
  fallback semantics, partial all-variant evidence, local CodeQL, hard
  push-all rejection, supported multi-platform push, ETag/idempotent
  cancel/retry, typed confirmations, create-response audit correlation, and
  the legacy Settings adapter;
- custom-build user/secret/idempotency/request/worker fields, raw summary,
  error detail, variant error detail, and terminal event payload canaries never
  enter the DOM.
- Docker Scanner Settings retain their inventory, doctor, pull/update, and
  durable local-build path, while Kubernetes capability mode removes unsupported
  Docker actions without an accessibility or containment regression;
- an API-created repository can start an ordinary explicit-tool scan from the
  Scans page, navigate from the `201` response, render a partial finding, and
  preserve keyboard focus and an announced result through whole-scan
  cancellation at both Chromium viewports.

The component tests additionally verify guarded confirmation, permissions,
loading/error/empty behavior, keyboard-focusable bounded evidence, aggregate
telemetry zero/absent handling, correlation identifier validation, registry
evidence equality, and provisional quarantine eligibility.
Custom-build component and client tests additionally verify strict allowlist
normalization, loading/empty/unauthorized states, pre-submit policy validation,
safe error-class remediation, bounded SSE parsing, and stream-to-polling
fallback.

## Automated coverage limitations

The axe color-contrast rule evaluates computed colors in both supported themes
in Chromium and Firefox. It does not prove contrast for every composited pixel,
image, display profile, forced-colors setting, operating-system theme override,
or future browser/theme combination.

The mobile project uses a narrow Chromium viewport and touch-capable context.
It proves responsive containment and the tested interactions, not behavior on
every physical device, mobile browser, virtual keyboard, safe-area inset, or
orientation.

The dialog checks prove browser focus behavior for representative shared and
custom dialogs. They do not prove how every assistive technology announces
descriptions, live updates, or disabled controls.

## Manual qualification still required

Before claiming production accessibility qualification, complete and record:

- VoiceOver with Safari on macOS and iOS;
- NVDA with Firefox and Chrome on Windows;
- JAWS with Edge or the enterprise-supported browser;
- reading order, landmark navigation, forms mode, table navigation, live
  announcements, dialog entry/trapping/return, copy feedback, and error
  recovery using those screen readers;
- keyboard-only operation in every supported production browser, including
  native scrolling of each wide data region;
- 200% and 400% zoom, large text, custom text spacing, reflow, portrait and
  landscape orientation, and on-screen keyboards;
- Windows High Contrast/forced-colors, increased contrast, reduced
  transparency, and both supported light and dark themes;
- Safari, Firefox, and Edge layout/interaction qualification and production
  visual-regression baselines;
- localization with long translated labels, locale-specific dates/numbers,
  and bidirectional text if those locales are supported;
- usability review with representative release operators, including alert
  prioritization, remediation clarity, destructive-action comprehension, and
  recovery from real authorization and service failures.

## Commands

From `ui/`:

```sh
pnpm typecheck
pnpm test:e2e:typecheck
pnpm lint
pnpm test
pnpm build
pnpm test:e2e
```

Keep the broad accessibility/browser completion items in the implementation
plan unchecked until the manual and multi-browser work above has evidence.
