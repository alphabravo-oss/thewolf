# Browser regression suite

These Playwright tests exercise the product UI while keeping the backend
boundary deterministic. Every `/api/**` request is intercepted by
`scanner-api-mock.ts`. Unrecognized API requests fail with
`E2E_ROUTE_NOT_MOCKED`.

## Covered behavior

- Settings → Scanners inventory, doctor, and image pull/update
- Kubernetes-managed Scanner Settings without Docker actions
- An ordinary API-created repository scan from submission through
  navigation, partial finding display, and focus-safe cancellation
- Application shell accessibility: skip link, mobile nav drawer, reduced
  motion, and theme-color sync

## Commands

From `ui/`:

```sh
pnpm install --frozen-lockfile
pnpm exec playwright install chromium firefox
pnpm test:e2e:typecheck
pnpm test:e2e
pnpm test:e2e:headed
```
