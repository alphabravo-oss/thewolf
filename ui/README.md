# Wolf UI (v2 — Vite + TanStack)

The new Wolf UI. Replaces the Next.js-based `ui/` directory.

## Stack

- **Vite 6** — SPA build, dev-server with HMR
- **React 19**
- **TanStack Router** (file-based, type-safe routes, generated tree at build)
- **TanStack Query** (server state)
- **TanStack Form** (login/register/settings forms)
- **TanStack Table** (findings/scans/collections)
- **TanStack Virtual** (large lists)
- **Tailwind CSS 3** + custom CSS variable theme (astronomer-inspired)
- **cmdk** for the command palette
- **sonner** for toasts
- **lucide-react** for icons
- **zustand** for ephemeral UI state (palette, shortcuts, sidebar collapse)

## Layout

```text
src/
  routes/                    Route files (TanStack Router auto-discovers).
    __root.tsx               Root (devtools mount).
    _authed.tsx              Auth-guard layout — every authed route nests here.
    _authed.index.tsx        /        — dashboard
    _authed.scans.tsx        /scans   — list
    _authed.scans.$scanId.tsx
                             /scans/:scanId — detail
    _authed.scans.$scanId.live.tsx
                             /scans/:scanId/live — SSE live view
    _authed.findings.tsx     /findings — list
    _authed.findings.$findingId.tsx
                             /findings/:findingId — detail
    _authed.collections.tsx, .$collectionId.tsx
    _authed.fixes.tsx, .$fixId.tsx
    _authed.loops.tsx, .$loopId.tsx
    _authed.scanners.tsx     /scanners — container backend admin
    _authed.settings.tsx     /settings
    login.tsx, register.tsx
  components/
    app-shell.tsx            Sidebar + topbar + main.
    sidebar.tsx, topbar.tsx
    command-palette.tsx      ⌘K palette.
    shortcuts-overlay.tsx    ? cheatsheet.
    live-scan.tsx            SSE-driven per-tool grid.
    skeleton.tsx, empty-state.tsx, severity-badge.tsx, scan-status-pill.tsx
    wolf-logo.tsx
  lib/
    api.ts, types.ts         API client + Go-mirrored types.
    cn.ts, severity.ts       Helpers.
    store-ui.ts              Zustand UI state.
    sse.ts                   Reusable SSE hook.
  styles/globals.css         CSS variables + utility classes.
  main.tsx                   Entrypoint.
  vite-env.d.ts              Vite + env-var types.
```

## Dev

```shell
corepack pnpm install
corepack pnpm dev    # localhost:3000, proxies /api → :8778
```

## Build

```shell
corepack pnpm build           # → dist/
corepack pnpm typecheck       # tsc --noEmit
```

## Theme

CSS variables in `src/styles/globals.css` drive light + dark mode.
Astronomer-inspired primitives:

- `.glass-card` — backdrop-blurred panel
- `.glow-success/warning/error/info/pending` — subtle status halo
- `.log-viewer` — dark-themed monospace log box
- `.gauge-bar` / `.gauge-bar-fill` — slim progress bars
- `.shimmer` — loading-skeleton animation
- `.text-gradient` — blue→violet headline accent
- `.nav-item` — sidebar row
- `.badge-{critical,high,medium,low,info}` — severity badges
