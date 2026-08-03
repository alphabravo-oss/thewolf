# Scanner management browser regression suite

This suite exercises scanner management as a browser user while keeping the
backend boundary deterministic. It is intentionally separate from the Go API
and persistence integration suites: every `/api/**` request is intercepted by
`scanner-api-mock.ts`, command requests are recorded, and an unrecognized API
request fails with `E2E_ROUTE_NOT_MOCKED`.

## Covered behavior

- Existing Settings → Scanners inventory, doctor, image update/pull, rebuild,
  and tool-version controls.
- Kubernetes-managed Scanner Settings without unsupported Docker actions.
- An ordinary API-created repository scan from submission through `201`
  navigation, partial finding display, and focus-safe whole-scan cancellation.
- `read_only`, `candidate`, `canary`, and `stable_control` capability stages.
- On-demand discovery, update selection, candidate creation, approval,
  release selection, promotion, and navigation to the created rollout.
- Keyboard-only access to the rollback action, dialog focus behavior, typed
  confirmation, reason capture, and submitted rollback payload.
- Scanner notification filtering and safe dead-letter detail, including
  payload redaction, accessible status/severity semantics, remediation links,
  audit-reason capture, and exact `If-Match` plus idempotency retry headers.
- Read-only scanner alert review at desktop and mobile widths, including
  active/resolved filters, allowlisted evidence summaries, lifecycle/reopen
  disclosure, remediation deep links, and unsupported-mutation absence.
- The Operations degraded state and its actionable links, alerts, readiness
  table, and stuck-work summary.
- Candidate manifest and scanner-lock diff evidence, including truncation
  disclosure and keyboard-focusable diff regions.
- Desktop (`1440×1000`) and mobile (`390×844`) rendering for the stable
  settings, Operations, and artifact-diff views.
- All 12 scanner-management panels in light and dark themes under Chromium,
  plus the same tagged semantic/axe/containment matrix in desktop Firefox.

The suite checks one visible `h1`, accessible names for interactive controls,
focus indicators in the guarded keyboard journey, and axe WCAG 2.0/2.1 A/AA
rules scoped to `main`. The complete 12-panel matrix includes axe's computed
`color-contrast` rule in both themes under Chromium and Firefox. Screenshots
remain pinned to Chromium dark theme.

This automation is not a claim of complete contrast qualification: computed
axe colors do not cover every composited pixel, alternate production theme,
operating-system rendering path, or user configuration. Screen-reader journeys,
browser zoom/reflow beyond the automated viewports, forced-colors and increased
contrast modes, touch assistive technology, and usability remain manual release
checks with supported assistive technologies and real production themes.

## Commands

From `ui/`:

```sh
pnpm install --frozen-lockfile
pnpm exec playwright install chromium firefox
pnpm test:e2e:typecheck
pnpm test:e2e
pnpm test:e2e:headed
```

The Playwright web server creates a production Vite build before serving it.
That deliberately tests the same lazy chunks shipped by the Go server and
avoids dependency pre-bundling differences in Vite development mode. It uses
port `43719` and refuses to attach to an unrelated process already using the
port.

To isolate functional tests from screenshots:

```sh
pnpm exec playwright test --grep-invert @visual
```

## Screenshot policy

Screenshot baselines are checked in under
`e2e/__screenshots__/<viewport>-chromium-<platform>/`. The project names include
the operating-system platform because font rasterization differs between macOS
and Linux even with self-hosted, pinned fonts. CI uses the checked-in Linux
baselines; macOS developers use the checked-in Darwin baselines.

Snapshots also pin:

- Playwright/Chromium through `pnpm-lock.yaml`;
- locale `en-US` and timezone `America/New_York`;
- dark color scheme, reduced motion, device scale factor `1`;
- fixed API timestamps and fixture data;
- animation and caret suppression;
- a maximum one-percent differing-pixel ratio.

Update snapshots only for an intentional, reviewed UI change:

```sh
pnpm test:e2e:update
git diff -- ui/e2e/__screenshots__
pnpm test:e2e
```

Linux baselines must be produced with the same Node, pnpm lockfile, and
Playwright version used by CI. Do not approve a snapshot update solely to make
CI green: inspect the old/new images and confirm the associated UI change.
Windows is not a baseline platform today; Windows contributors can run the
non-visual command or use the Linux CI job for visual qualification.

## Adding a scanner journey

1. Add the smallest required read/command route to `scanner-api-mock.ts`.
2. Keep timestamps, identifiers, digests, and list ordering fixed.
3. Record and assert security-relevant command payloads, not only the resulting
   toast or navigation.
4. Prefer role/name locators. Use exact names when one control's name is a
   substring of another.
5. Add keyboard behavior when introducing a new guarded or destructive action.
6. Run both viewport projects and inspect any new screenshots.
7. Use a backend integration suite—not this fixture—when the behavior depends
   on database concurrency, credentials, registry state, SSE replay, or worker
   execution.

Failure artifacts are written to ignored `test-results/` and
`playwright-report/` directories. CI retains traces, videos, screenshots, and
the HTML report for failed runs.
