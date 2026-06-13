# Fleet Mode + Tailwind 4 + shadcn/ui Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn wolf's web UI from a per-repo dashboard into a fleet-management tool sized for 20-100 repos. Get there on a modern, stable foundation: Tailwind 4, shadcn/ui primitives, clean component layer, no Next.js leftovers.

**Architecture:** Two-stage delivery. Stage A is foundation work (rename `ui-next` → `ui`, fix README, Tailwind 4 migration, shadcn adoption, component primitives). Stage B is fleet-management features built on that foundation (aggregate API endpoints, fleet dashboard at `/`, `/repos` filter/group/bulk UX, GitHub-org and SSH discovery wizards, org-wide repo visibility). Each stage stands on its own; failures in Stage B don't unwind Stage A.

**Tech Stack:** React 19, TanStack Router + Query + Table + Virtual, Tailwind **4** (migrated from 3.4), **shadcn/ui** (newly adopted), Vite 6, Vitest, recharts, sonner, lucide-react. Backend: Go 1.26, chi, sqlx (SQLite + Postgres).

**Source audit:** `docs/superpowers/specs/2026-06-13-ui-ux-walkthrough-findings.md` (informal walkthrough notes) and the conversation thread of 2026-06-14. This document is the actionable form.

**Companion plan executed first:** `docs/superpowers/plans/2026-06-13-ui-ux-flow-fixes.md` (already shipped through commit `4bd5e19`). Fleet mode picks up from that state.

---

## Scope Check

This plan contains two independent but sequenced subsystems:

1. **Foundation modernization** (Tailwind 4 + shadcn + rename + README fix) — pure infrastructure, no user-facing feature change. Fully self-contained.
2. **Fleet management** (aggregate API + fleet dashboard + bulk UX + import wizards + org-wide visibility) — the actual feature work.

They share a build target but otherwise don't entangle. If you need to ship one and pause the other, they cut cleanly at the end of Stage A. Stage B can ship incrementally too; each phase below is independently deployable.

---

## Decisions made upfront (won't reopen during implementation)

| Decision | Choice | Why |
|---|---|---|
| Tailwind version | **4 (stable)** | Better CSS-first config, smaller runtime, faster dev. We're already on a modern stack. |
| Component system | **shadcn/ui (copy-into-repo)** | Components become first-class repo code; no vendor lock; matches existing CVA/clsx/tailwind-merge stack. |
| UI directory name | **rename `ui-next` → `ui`** | `next` is misleading (no Next.js). Bare `ui` matches what's actually built. |
| Routing | **stay on TanStack Router** | Already file-based, working, type-safe, codegen on save. |
| Fleet visibility | **org-wide for Repos/Scans/Findings/Collections** | Match the fleet workflow. Per-user data stays as `created_by`/`owner`; access becomes `read:repos` / `write:repos` scope-driven, not user-id-driven. |
| Visibility migration | **migration 020 flips a new `fleet_mode` setting + existing rows stay; new rows are org-scoped** | Existing single-user installs aren't surprised; flipping the toggle in Settings makes the change explicit. |
| Fleet dashboard location | **replace the existing `/` Dashboard** | At 20+ repos the empty-state hand-holding is the wrong landing experience. |
| Bulk-import wizards | **modal-driven on existing pages** (`/repos` → "Import GitHub", node detail → "Discover repos") | Doesn't add new sidebar entries; keeps the inventory model. |
| Aggregate endpoints | **new `/api/v1/fleet/*` namespace** (NOT overload existing endpoints) | Aggregate queries are different beasts; clean namespace, easy to cache/limit. |

---

## File Structure

Files this plan creates or significantly changes.

### Stage A — Foundation

**Renames** (single git operation, scriptable):
- `ui-next/` → `ui/` (everywhere: file paths, Makefile, docker-compose.yml, internal/api/static.go discovery list, README, .github/workflows).

**New files:**
- `ui/components.json` — shadcn CLI config.
- `ui/src/components/ui/` — shadcn primitive components (button, card, badge, dialog, dropdown-menu, input, label, select, separator, table, tabs, sheet, toast, etc.). Added via `pnpm dlx shadcn@latest add <name>` one at a time.
- `ui/postcss.config.mjs` — Tailwind 4 PostCSS plugin.
- `ui/src/styles/globals.css` — Tailwind 4 `@import "tailwindcss"` + theme tokens.

**Removed files:**
- `ui-next/tailwind.config.ts` (replaced by inline `@theme` in CSS for Tailwind 4).
- `ui-next/postcss.config.cjs` (replaced by ESM equivalent compatible with Tailwind 4).

**Modified files:**
- `ui/vite.config.ts` — add the Tailwind 4 Vite plugin (`@tailwindcss/vite`).
- `ui/package.json` — bump tailwindcss to 4, add `@tailwindcss/vite` + `@tailwindcss/postcss`, remove `autoprefixer` (Tailwind 4 handles it).
- `ui/src/styles/globals.css` — entire file rewritten for the new CSS-first config.
- `internal/api/static.go` — change default candidate paths from `./ui-next/dist` to `./ui/dist`.
- `Makefile` — `ui-build`, `ui-dev-install`, `dev-ui` targets.
- `docker-compose.yml` — bind paths if `ui-next` referenced.
- `README.md` — update "Next.js dashboard" claim; update tech stack list.

**Component migrations** (existing components rewritten to use shadcn primitives but keep their existing public API where possible):
- `ui/src/components/severity-badge.tsx` → uses shadcn `<Badge>` + `cva` variant.
- `ui/src/components/scan-status-pill.tsx` → uses shadcn `<Badge>` variant.
- `ui/src/components/empty-state.tsx` → uses shadcn `<Card>` shell.
- `ui/src/components/topbar.tsx` → shadcn `<Input>` for search; otherwise structural.
- `ui/src/components/sidebar.tsx` → spacing/typography to Tailwind 4 tokens; structure unchanged.
- `ui/src/components/findings-toolbar.tsx` → shadcn `<DropdownMenu>` + `<Input>`.
- `ui/src/components/command-palette.tsx` → already uses `cmdk` directly; wrap with shadcn `<CommandDialog>`.
- `ui/src/components/browse-path-modal.tsx` → shadcn `<Dialog>`.

### Stage B — Fleet management

**New backend files:**
- `internal/api/routes/fleet.go` — handlers for `GET /fleet/posture`, `GET /fleet/inventory`, `GET /fleet/needs-attention`, `GET /findings/aggregate`, `POST /sources/github/list-org-repos`, `POST /nodes/{id}/discover-repos`.
- `internal/api/routes/fleet_test.go` — handler tests (httptest + in-memory store).
- `internal/db/sqlite_fleet.go` + `internal/db/postgres_fleet.go` — aggregate queries (severity totals, top-rule-id by repo count, stale scans, gate-failing repos).
- `internal/db/migrations/020_fleet_mode.sql` — new `fleet_mode` setting (defaults `false`); when true, queries select across all users instead of by user_id. Adds `repos.owner_user_id` rename (kept as-is; we add the access semantics in code, not schema).
- `internal/github/api.go` — minimal GitHub REST client (org repos list); uses the secrets store for the token.

**Modified backend files:**
- `internal/api/server.go` — register `/fleet/*` and `/sources/github/*` routes with appropriate scopes.
- `internal/api/openapi/spec.go` — add the new endpoints to the catalog.
- `internal/api/routes/repos.go` — when `fleet_mode=true`, `ListRepos` returns all repos; otherwise existing per-user filter. Same for `scans.go`, `findings.go`, `collections.go`.
- `internal/api/routes/nodes.go` — extend the browse handler with `?git_only=true` filter so the discover-repos wizard can list `.git/` parents in one call.

**New frontend files:**
- `ui/src/routes/_authed.index.tsx` — replaced wholesale: this is the new fleet dashboard.
- `ui/src/components/fleet/posture-cards.tsx` — the four top-level stat cards.
- `ui/src/components/fleet/severity-trend.tsx` — 90-day stacked area chart of open findings by severity.
- `ui/src/components/fleet/top-components.tsx` — vulnerable libraries table.
- `ui/src/components/fleet/needs-attention.tsx` — failing-gate, stale, new-high lists.
- `ui/src/components/fleet/inventory-breakdown.tsx` — by source, by collection breakdown.
- `ui/src/components/fleet/recent-activity.tsx` — audit-log-derived feed.
- `ui/src/components/repos/filter-bar.tsx` — chips: source-type, collection, status, last-scan filters.
- `ui/src/components/repos/group-toggle.tsx` — group-by selector + grouped render mode.
- `ui/src/components/repos/bulk-toolbar.tsx` — appears when any row selected; Scan / Add to collection / Apply policy / Delete actions.
- `ui/src/components/repos/import-github-modal.tsx` — multi-step modal for the GitHub-org import wizard.
- `ui/src/components/repos/discover-ssh-modal.tsx` — modal for the SSH node discover-repos wizard.
- `ui/src/lib/fleet.ts` — typed wrappers for `/fleet/*` endpoints + react-query hooks (`useFleetPosture`, `useFleetInventory`, etc.).

**Modified frontend files:**
- `ui/src/routes/_authed.repos.index.tsx` — replace the flat table with the filter-bar / group-toggle / bulk-toolbar driven view.
- `ui/src/routes/_authed.collections.$collectionId.tsx` — add per-collection posture summary (smaller version of the fleet dashboard).
- `ui/src/routes/_authed.settings.tsx` — add the `fleet_mode` toggle in General settings.

---

## Definition of Done — whole project

The plan is done only when every item below is verifiable.

### Stage A (foundation)

1. The `ui-next/` directory no longer exists. The directory is `ui/`. `git log --follow ui/src/routes/_authed.tsx` shows the rename, not a delete.
2. `internal/api/static.go` discovers `./ui/dist` (and `./dist`) by default; an explicit `WOLF_UI_DIR` still wins.
3. `Makefile` and `docker-compose.yml` reference `ui/` consistently; `make ui-build` produces `ui/dist/`.
4. `README.md` no longer says "Next.js" — describes the stack accurately (Vite + React 19 + Tailwind 4 + shadcn + TanStack Router).
5. `ui/package.json` lists `tailwindcss: ^4.x` and `@tailwindcss/vite: ^4.x`. No `tailwindcss: ^3` remnant. `autoprefixer` removed (Tailwind 4 includes it).
6. `ui/src/styles/globals.css` uses the Tailwind 4 syntax: `@import "tailwindcss"` + `@theme { ... }` block defining all design tokens (colors, radii, spacing). `tailwind.config.ts` deleted.
7. `pnpm build` produces a `dist/` whose CSS bundle is **smaller** than the Tailwind 3 build (Tailwind 4 strips harder).
8. `ui/components.json` exists with `style: "default"`, the `tailwind.css` path set to `src/styles/globals.css`, the alias `@/*` mapped to `src/*`.
9. `ui/src/components/ui/` contains at minimum: button, card, badge, dialog, dropdown-menu, input, label, select, separator, table, tabs, sheet, toast, alert, checkbox, command, popover, scroll-area, tooltip — all generated via the shadcn CLI, all in the project's own tree.
10. `severity-badge`, `scan-status-pill`, `empty-state`, `topbar`, `findings-toolbar`, `command-palette`, `browse-path-modal` rebuilt to use shadcn primitives. **Visually unchanged or improved** — no regression in dark mode rendering.
11. `pnpm test` passes (the parse-frameworks tests from the previous plan + any new tests this plan adds).
12. `pnpm build` clean.
13. The running app at `http://127.0.0.1:8779` renders without console errors. Every page reachable in the prior walkthrough still works (login, dashboard, repos, collections, scans, findings, settings, audit, /docs).

### Stage B (fleet management)

14. `GET /api/v1/fleet/posture` returns total open findings by severity, week-over-week deltas, fleet repo count, gate-failure count. Tested with a seeded fixture.
15. `GET /api/v1/fleet/inventory` returns counts by source_type, by collection, by language.
16. `GET /api/v1/fleet/needs-attention` returns the top 10 repos sorted by a composite "needs attention" score (combination of new critical/high findings, gate-failure, stale-scan).
17. `GET /api/v1/findings/aggregate?group_by=rule_id&limit=10` returns the top-N vulnerable rules with the count of repos they appear in.
18. `POST /api/v1/sources/github/list-org-repos` accepts `{org, secret_id?}` and returns the GitHub org's repo list; uses the user's `github_token` secret when `secret_id` omitted. Returns `[]` and clear error message on bad token.
19. `POST /api/v1/nodes/{id}/discover-repos` accepts `{base_path}` and returns the list of directories under that path containing a `.git/` entry, each with detected branch and last-commit-SHA.
20. The `/` route is the fleet dashboard: posture cards, severity trend chart, top vulnerable components, needs-attention list, inventory breakdown, recent activity. Empty-state for a fresh install ("No repos yet — import some").
21. The `/repos` page has a filter bar (source-type, collection, last-scan-status), a group-by selector (none, source-type, collection, language), and a bulk-action toolbar that appears when any rows are selected.
22. Bulk "Scan selected" works against an arbitrary multi-selection (creates one scan per selected repo, surfaces a progress toast).
23. `/repos` has a button "Import from GitHub" that opens the org-import modal; selecting N repos and clicking Import creates N repos with `source_type: "github"`, attached to the chosen collection if any.
24. The node detail page (`/settings?tab=nodes` → click a node, or a dedicated route if one's added) has a "Discover repos" button that opens the SSH discover modal; selecting N repos and clicking Import creates N repos with `source_type: "ssh"` pointing at that node.
25. The Settings page has a `fleet_mode` toggle under General. Default off (preserves single-user behavior). When on, ListRepos/ListScans/ListFindings/ListCollections no longer filter by `user_id`.
26. With `fleet_mode=true` and two users in the system: User A's repos appear in User B's `/repos` list. User B can see and scan them. Admin-only operations (delete user, set policy) remain admin-only.
27. `go test ./...` green (70+ packages). `pnpm test` green. `pnpm build` green. `go vet ./...` clean.
28. Manual smoke (Chrome DevTools MCP, fresh DB): log in → see empty fleet dashboard → "Import from GitHub" → see the import modal request a token if none → import 3 repos → land back on `/repos` filtered to "GitHub" → bulk-select 3 → Scan selected → 3 scans appear in `/scans` → fleet dashboard updates posture cards.

---

## Verification gates

Before declaring done, all of these must pass:

```bash
go vet ./...
go test ./...
cd ui && pnpm test && pnpm build && pnpm typecheck
# Manual: walkthrough per item 28 above.
```

---

## Risk register

- **Tailwind 4 visual drift.** The new CSS-first config moves some token defaults. Mitigation: explicit `@theme` block carrying over every custom color/radius/spacing we use. Visual regression is checked by the walkthrough at the end of Phase 2.
- **shadcn version churn.** The CLI is moving fast. Mitigation: pin the CLI version in the Makefile (`shadcn@2.x.y`) so re-running `pnpm dlx shadcn add` doesn't change other components.
- **Org-wide visibility migration.** Switching from per-user to org-wide is a one-way door for an existing multi-user install. Mitigation: `fleet_mode` is a setting (default false). Existing rows aren't touched. Toggling on is opt-in.
- **GitHub API rate limits.** The org-import endpoint hits `GET /orgs/{org}/repos` which is paginated. A 100-repo org needs 1-2 requests. Mitigation: server-side pagination + caching for 5 minutes per `(org, secret_id)` pair.
- **SSH discover scope.** Walking the FS recursively on a remote node can be slow. Mitigation: bounded depth (default 3), 10s timeout, return partial results with a "took too long" warning.

---

# Stage A — Foundation modernization

## Phase A1 — Rename and reference fixes

### Task A1: Rename `ui-next/` to `ui/`

**Files:**
- All files under `ui-next/` (renamed).
- Modify: `Makefile`, `docker-compose.yml`, `internal/api/static.go`, `.github/workflows/*.yml`, `README.md`, every `import` that uses an absolute `@/` alias (none should, since Vite resolves `@` from the directory the config lives in).

- [ ] **Step 1: Verify nothing imports `ui-next` by string**

Run: `grep -rn "ui-next" --include='*.go' --include='*.yml' --include='*.yaml' --include='*.md' --include='Makefile' --include='*.json' --include='*.toml' .`
Expected: matches in `Makefile`, `docker-compose.yml`, `internal/api/static.go`, `.github/workflows/`, `README.md`, `CHANGELOG.md`. Note them all.

- [ ] **Step 2: Rename the directory (git-aware)**

Run: `git mv ui-next ui`
Expected: `ui/` exists, `ui-next/` is gone, `git status` shows ~200 renamed files.

- [ ] **Step 3: Update every reference**

For each file from Step 1, replace `ui-next` with `ui` exactly. Specifically:
- `internal/api/static.go`: change `"./ui-next/dist"` to `"./ui/dist"` in the candidate list.
- `Makefile`: every `cd ui-next` or `pushd ui-next` becomes `cd ui` / `pushd ui`.
- `docker-compose.yml`: any volume mount or build context referencing `ui-next` becomes `ui`.
- `.github/workflows/*.yml`: `working-directory: ./ui-next` → `./ui`.
- `README.md`: any path examples.

- [ ] **Step 4: Run the verification grep again**

Run: `grep -rn "ui-next" .`
Expected: empty (or only commit messages in git history, which is fine).

- [ ] **Step 5: Build everything from scratch**

```bash
cd /Users/mj/mjcode/ab/thewolf
rm -rf ui/dist ui/node_modules
cd ui && pnpm install && pnpm build && cd ..
go build ./...
```
Expected: clean build of both UI and Go.

- [ ] **Step 6: Smoke the running app**

Kill any prior `wolf serve` on :8779, start a fresh one, confirm `GET /` returns 200 and the page renders.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(ui): rename ui-next/ to ui/ — there's no Next.js"
```

---

### Task A2: README + CHANGELOG accuracy fix

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md` (if it mentions Next.js)

- [ ] **Step 1: Find and replace stale "Next.js" claims**

Run: `grep -n "Next.js\|next.js\|NextJS" README.md CHANGELOG.md`
Expected: one or more matches.

- [ ] **Step 2: Replace with accurate stack description**

In `README.md`, replace any "Next.js dashboard" line with:
```markdown
- **Web UI** — Vite + React 19 + TanStack Router + Tailwind 4 + shadcn/ui dashboard with collection management, scan monitoring, finding exploration, fleet posture, and scanner-backend admin
```

- [ ] **Step 3: Update the Architecture diagram in README**

If the README has an architecture diagram referencing `ui-next/`, update to `ui/`. Also update any tech stack table.

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: README/CHANGELOG describe the actual UI stack (Vite, not Next.js)"
```

---

## Phase A2 — Tailwind 4 migration

Tailwind 4 stable was released in late 2024. The breaking changes from 3.4: PostCSS plugin renamed; the `tailwind.config.ts` file is replaced by a CSS-first `@theme` block; no `autoprefixer` needed; the build is faster but slightly stricter about unknown classes.

### Task A3: Install Tailwind 4 + Vite plugin

**Files:**
- Modify: `ui/package.json`
- Create: `ui/postcss.config.mjs` (replacing the 3.4 one)
- Delete: `ui/tailwind.config.ts`, `ui/postcss.config.cjs`

- [ ] **Step 1: Install the new packages**

```bash
cd ui
pnpm remove tailwindcss autoprefixer
pnpm add -D tailwindcss@^4 @tailwindcss/vite@^4 @tailwindcss/postcss@^4
```
Expected: `package.json` now lists `tailwindcss: ^4.x`, `@tailwindcss/vite: ^4.x`. No `autoprefixer`.

- [ ] **Step 2: Replace `postcss.config.cjs` with `postcss.config.mjs`**

Delete `postcss.config.cjs`. Create:
```js
// ui/postcss.config.mjs
export default {
  plugins: {
    "@tailwindcss/postcss": {},
  },
};
```

- [ ] **Step 3: Delete `tailwind.config.ts`**

Run: `rm tailwind.config.ts`

- [ ] **Step 4: Commit (broken state — config rewritten in next task)**

```bash
git add package.json pnpm-lock.yaml postcss.config.mjs tailwind.config.ts
git commit -m "chore(ui): install Tailwind 4 packages, drop autoprefixer/old config"
```
Note: the build will be broken until A4 ships. That's intentional — keep the migration commits atomic and reviewable.

---

### Task A4: Rewrite globals.css for Tailwind 4 CSS-first config

**Files:**
- Modify: `ui/src/styles/globals.css` (entire file rewritten)

- [ ] **Step 1: Read the existing globals.css to extract theme tokens**

Run: `cat ui/src/styles/globals.css | head -80`
Note: dark/light HSL values for `--background`, `--foreground`, `--card`, `--border`, `--muted`, `--accent`, `--primary`, `--secondary`, `--destructive`, `--input`, `--ring`, plus all `--sidebar-*` tokens, radius, etc.

- [ ] **Step 2: Replace globals.css with the Tailwind 4 form**

```css
/* ui/src/styles/globals.css */
@import "tailwindcss";

/* Tailwind 4 CSS-first theme: tokens live here instead of tailwind.config.ts.
   Hue values use the same HSL triplets we used under v3 so dark mode renders
   identically. Reference vars (e.g. `bg-background`) auto-generated by name. */
@theme {
  --color-background: 240 10% 4%;
  --color-foreground: 0 0% 98%;

  --color-card: 240 10% 5%;
  --color-card-foreground: 0 0% 98%;

  --color-popover: 240 10% 5%;
  --color-popover-foreground: 0 0% 98%;

  --color-primary: 0 0% 98%;
  --color-primary-foreground: 240 10% 4%;

  --color-secondary: 240 4% 16%;
  --color-secondary-foreground: 0 0% 98%;

  --color-muted: 240 4% 16%;
  --color-muted-foreground: 240 5% 65%;

  --color-accent: 240 4% 16%;
  --color-accent-foreground: 0 0% 98%;

  --color-destructive: 0 63% 31%;
  --color-destructive-foreground: 0 0% 98%;

  --color-border: 240 4% 16%;
  --color-input: 240 4% 16%;
  --color-ring: 240 5% 65%;

  --color-sidebar: 240 10% 4.5%;
  --color-sidebar-foreground: 240 4.8% 75%;
  --color-sidebar-border: 240 3.7% 12%;
  --color-sidebar-accent: 240 3.7% 10%;
  --color-sidebar-accent-foreground: 0 0% 98%;

  --radius: 0.5rem;

  --animate-fade-in: fade-in 150ms ease-out;
}

@keyframes fade-in {
  from { opacity: 0; }
  to { opacity: 1; }
}

/* Light mode override (toggled via class="dark" or "light" on <html>) */
@media (prefers-color-scheme: light) {
  :root:not(.dark) {
    --color-background: 0 0% 100%;
    --color-foreground: 240 10% 4%;
    --color-card: 0 0% 100%;
    --color-card-foreground: 240 10% 4%;
    /* …carry over every other light-mode value from the prior globals.css */
  }
}

/* Global element styling */
* {
  border-color: hsl(var(--color-border) / 1);
}

body {
  background-color: hsl(var(--color-background) / 1);
  color: hsl(var(--color-foreground) / 1);
  font-feature-settings: "rlig" 1, "calt" 1;
}
```

When copying from the existing v3 file, preserve every custom HSL value verbatim. Only the *form* changes (`@theme` block vs `:root` + `tailwind.config.ts`), not the *values*.

- [ ] **Step 3: Add the Tailwind 4 Vite plugin**

Edit `ui/vite.config.ts`:
```ts
import tailwindcss from "@tailwindcss/vite";

// inside the plugins array, BEFORE react():
plugins: [
  TanStackRouterVite({ ... }),
  tailwindcss(),
  react(),
],
```

- [ ] **Step 4: Build, expect it to work**

```bash
cd ui && pnpm build 2>&1 | tail -10
```
Expected: build succeeds. CSS bundle size should be reported in the output; jot it down for comparison.

- [ ] **Step 5: Run the app, eyeball every page**

Start `wolf serve --bind :8779`, log in, walk every route:
- `/` (Dashboard)
- `/collections`, click a collection
- `/repos`, click a repo
- `/scans`, click a scan
- `/findings`
- `/settings` (every tab)
- `/audit`
- `/loops`, `/fixes`, `/scanners`

Expected: visual parity with the pre-migration version. Any class that uses `bg-background`, `text-foreground`, etc. renders correctly. Dark mode renders correctly.

If a token isn't rendering (transparent background, wrong color), it's almost certainly a missing `@theme` value — add it and rebuild.

- [ ] **Step 6: Commit**

```bash
git add ui/src/styles/globals.css ui/vite.config.ts
git commit -m "feat(ui): migrate Tailwind 3.4 → 4 (CSS-first @theme config)"
```

---

## Phase A3 — shadcn/ui adoption

### Task A5: Initialize shadcn

**Files:**
- Create: `ui/components.json`

- [ ] **Step 1: Run the shadcn init**

```bash
cd ui
pnpm dlx shadcn@latest init
```

Choose interactively:
- Style: **Default**
- Base color: **Neutral**
- CSS variables: **Yes**
- Where is your global CSS file: `src/styles/globals.css`
- Where is your `tailwind.config`: leave empty / accept the CSS-first detection
- Configure the import alias for components: `@/components`
- Configure the import alias for utils: `@/lib/utils`
- React Server Components: **No**

Expected: `components.json` written. The CLI also adds `src/lib/utils.ts` if it doesn't exist — confirm it has the standard `cn()` helper.

- [ ] **Step 2: Verify components.json**

```bash
cat components.json
```
Expected: object with `style`, `tailwind` (pointing at `src/styles/globals.css`), `aliases: { components, utils, ui, hooks, lib }`.

- [ ] **Step 3: Verify cn() helper**

```bash
cat src/lib/utils.ts
```
Expected:
```ts
import { type ClassValue, clsx } from "clsx"
import { twMerge } from "tailwind-merge"
export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)) }
```

If your project already has a `cn()` in `src/lib/cn.ts`, you have two: either delete the new shadcn one and adjust `components.json` `aliases.utils` to `@/lib/cn`, or delete the old one and update all imports. **Pick one and be consistent.** This plan assumes you keep the new `lib/utils.ts` and delete `lib/cn.ts`.

- [ ] **Step 4: Replace `@/lib/cn` imports with `@/lib/utils`**

```bash
grep -rln '@/lib/cn' src | xargs sed -i '' 's|@/lib/cn|@/lib/utils|g'
rm -f src/lib/cn.ts
```
(On Linux, drop the `''` from sed.)

- [ ] **Step 5: Build to verify**

`pnpm build` should still pass.

- [ ] **Step 6: Commit**

```bash
git add components.json src/lib/utils.ts
git add -u  # captures the cn imports moved + cn.ts deleted
git commit -m "feat(ui): adopt shadcn/ui (components.json + cn helper)"
```

---

### Task A6: Generate the primitive component set

**Files:**
- Create (via shadcn CLI): everything under `ui/src/components/ui/`.

- [ ] **Step 1: Add the primitives we'll need**

Run from `ui/`:
```bash
pnpm dlx shadcn@latest add button card badge dialog dropdown-menu input label select separator table tabs sheet toast alert checkbox command popover scroll-area tooltip skeleton
```
Expected: ~20 files appear under `src/components/ui/`. Each is hand-editable code in your repo (not a node_modules import).

- [ ] **Step 2: Verify the new components compile**

```bash
pnpm typecheck
```
Expected: clean. If a generated component references `@/lib/utils` but yours is at a different path, fix `components.json` and re-run `add` for any broken file.

- [ ] **Step 3: Build to make sure CSS is correct**

```bash
pnpm build
```
Expected: clean build. CSS bundle size will grow slightly to include the new primitive classes; that's expected and Tailwind will tree-shake unused ones in a real build.

- [ ] **Step 4: Commit**

```bash
git add src/components/ui
git commit -m "feat(ui): generate shadcn primitive component set"
```

---

### Task A7: Rewrite domain components to use shadcn primitives

This is the biggest task in Stage A by line count, but it's mechanical: each existing component gets its custom classes replaced with shadcn primitives where applicable. The component's public API (props) stays the same so consumers don't change.

**Files (one commit per component):**

- [ ] **Step 1: `severity-badge.tsx`**

Current: custom span with severity-specific classes.

Replace with:
```tsx
// ui/src/components/severity-badge.tsx
import { cva, type VariantProps } from "class-variance-authority";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const severityVariants = cva("", {
  variants: {
    severity: {
      critical: "border-red-500/40 bg-red-500/15 text-red-300",
      high:     "border-orange-500/40 bg-orange-500/15 text-orange-300",
      medium:   "border-amber-500/40 bg-amber-500/15 text-amber-300",
      low:      "border-sky-500/40 bg-sky-500/15 text-sky-300",
      info:     "border-zinc-500/40 bg-zinc-500/15 text-zinc-300",
    },
  },
  defaultVariants: { severity: "info" },
});

type Props = VariantProps<typeof severityVariants> & {
  className?: string;
  children?: React.ReactNode;
};

export function SeverityBadge({ severity, className, children }: Props) {
  return (
    <Badge variant="outline" className={cn(severityVariants({ severity }), className)}>
      {children ?? severity}
    </Badge>
  );
}
```

Test by visiting `/findings` — the severity chips render with the new style.

Commit: `refactor(ui): rebuild SeverityBadge on shadcn Badge`

- [ ] **Step 2: `scan-status-pill.tsx`**

Same pattern. Each scan status (`pending`, `running`, `completed`, `failed`, `cancelled`) becomes a CVA variant on shadcn `<Badge>`.

Commit: `refactor(ui): rebuild ScanStatusPill on shadcn Badge`

- [ ] **Step 3: `empty-state.tsx`**

Wrap the existing layout in shadcn `<Card>` + `<CardHeader>` + `<CardContent>`. Preserve the `{ icon, title, body, cta }` props API.

Commit: `refactor(ui): rebuild EmptyState on shadcn Card`

- [ ] **Step 4: `topbar.tsx`**

Replace the search button with shadcn `<Input>` styled as a button-trigger; the actual search dialog already uses cmdk and lands in `command-palette.tsx`.

Commit: `refactor(ui): topbar uses shadcn Input for search trigger`

- [ ] **Step 5: `findings-toolbar.tsx`**

Replace the custom `<select>` dropdowns with shadcn `<DropdownMenu>` + `<DropdownMenuTrigger>` + `<DropdownMenuContent>`. Keep the existing query-param URL binding.

Commit: `refactor(ui): findings toolbar uses shadcn DropdownMenu`

- [ ] **Step 6: `command-palette.tsx`**

Replace the hand-rolled cmdk wrapper with shadcn `<CommandDialog>`. The keyboard shortcut binding (`⌘K`) stays in `topbar.tsx`.

Commit: `refactor(ui): command palette uses shadcn CommandDialog`

- [ ] **Step 7: `browse-path-modal.tsx`**

Replace the custom modal scaffolding with shadcn `<Dialog>` + `<DialogContent>` + `<DialogHeader>`. Keep the path-walking logic.

Commit: `refactor(ui): browse-path modal uses shadcn Dialog`

- [ ] **Step 8: `sidebar.tsx`**

Lightest touch — keep the structure. Replace spacing utility classes with the new tokens, replace any inline color values with semantic tokens.

Commit: `refactor(ui): sidebar uses Tailwind 4 semantic tokens`

- [ ] **Step 9: Verify each page renders unchanged**

Manual walk: log in, visit every route, confirm no visual regression. Take screenshots to `.claude/ui-screens/2026-06-14-postshadcn-*.png`.

- [ ] **Step 10: Final Stage A verification**

```bash
cd ui && pnpm test && pnpm build && pnpm typecheck
cd .. && go build ./... && go test ./...
```
Expected: all green.

Commit: `test: Stage A foundation complete (Tailwind 4 + shadcn live)`

---

# Stage B — Fleet management

## Phase B1 — Aggregate API endpoints

### Task B1: Fleet posture endpoint

**Files:**
- Create: `internal/api/routes/fleet.go`
- Create: `internal/api/routes/fleet_test.go`
- Modify: `internal/api/server.go` (register routes)
- Modify: `internal/api/openapi/spec.go` (catalog entries)

- [ ] **Step 1: Write the failing test**

```go
// internal/api/routes/fleet_test.go
package routes_test

import (
  "encoding/json"
  "net/http"
  "testing"
  // imports as in scans_test.go
)

func TestFleetPostureEmptyFleet(t *testing.T) {
  env := setupTestEnv(t)
  defer env.Store.Close()
  w := env.doRequest(http.MethodGet, "/api/v1/fleet/posture", nil)
  if w.Code != http.StatusOK {
    t.Fatalf("expected 200, got %d", w.Code)
  }
  var got struct {
    Data struct {
      OpenFindings    map[string]int `json:"open_findings_by_severity"`
      RepoCount       int            `json:"repo_count"`
      GatesFailing    int            `json:"gates_failing"`
      WeekOverWeek    map[string]int `json:"week_over_week_delta"`
    } `json:"data"`
  }
  if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
    t.Fatalf("decode: %v", err)
  }
  if got.Data.RepoCount != 0 || got.Data.GatesFailing != 0 {
    t.Errorf("empty fleet should report zeros, got %+v", got.Data)
  }
}

func TestFleetPostureCountsOpenFindings(t *testing.T) {
  env := setupTestEnv(t)
  defer env.Store.Close()
  // Seed: one repo, one scan, three findings (critical, high, low)
  // … standard seed pattern from scans_test.go …
  w := env.doRequest(http.MethodGet, "/api/v1/fleet/posture", nil)
  if w.Code != http.StatusOK {
    t.Fatalf("expected 200, got %d", w.Code)
  }
  var got map[string]any
  json.Unmarshal(w.Body.Bytes(), &got)
  data := got["data"].(map[string]any)
  sev := data["open_findings_by_severity"].(map[string]any)
  if sev["critical"].(float64) != 1 || sev["high"].(float64) != 1 || sev["low"].(float64) != 1 {
    t.Errorf("expected 1/1/1, got %v", sev)
  }
}
```

- [ ] **Step 2: Run the tests; expect routing-404**

`go test ./internal/api/routes/ -run TestFleetPosture -v`
Expected: FAIL — `/fleet/posture` returns 404 because the route doesn't exist yet.

- [ ] **Step 3: Implement the handler**

```go
// internal/api/routes/fleet.go
package routes

import (
  "net/http"
  "github.com/alphabravocompany/thewolf/internal/api/response"
  "github.com/alphabravocompany/thewolf/internal/auth"
)

func FleetPosture(w http.ResponseWriter, r *http.Request) {
  h := DefaultHandler
  if h == nil {
    response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
    return
  }
  claims := auth.GetUserFromContext(r.Context())
  if claims == nil {
    response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
    return
  }

  // Aggregate queries. Each is a small SELECT against the store.
  posture, err := h.Store.FleetPosture(r.Context(), claims.UserID, fleetModeEnabled(r.Context(), h.Store))
  if err != nil {
    response.WriteError(w, http.StatusInternalServerError, "server_error", "compute posture: "+err.Error())
    return
  }

  response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: posture})
}

// fleetModeEnabled reads the fleet_mode setting; on error or absence, false.
func fleetModeEnabled(ctx context.Context, store db.Store) bool {
  v, err := store.GetSetting(ctx, "fleet_mode")
  return err == nil && v == "true"
}
```

- [ ] **Step 4: Add the store method (interface + sqlite + postgres)**

In `internal/db/store.go`:
```go
type FleetPostureResult struct {
  OpenFindingsBySeverity map[string]int `json:"open_findings_by_severity"`
  WeekOverWeekDelta      map[string]int `json:"week_over_week_delta"`
  RepoCount              int            `json:"repo_count"`
  GatesFailing           int            `json:"gates_failing"`
}

// add to Store interface:
FleetPosture(ctx context.Context, userID string, fleetMode bool) (*FleetPostureResult, error)
```

Implement in `internal/db/sqlite_fleet.go`:
```go
package db

import "context"

func (s *SQLiteStore) FleetPosture(ctx context.Context, userID string, fleetMode bool) (*FleetPostureResult, error) {
  out := &FleetPostureResult{
    OpenFindingsBySeverity: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
    WeekOverWeekDelta:      map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
  }
  scope := "AND f.repo_id IN (SELECT id FROM repos WHERE user_id = ?)"
  args := []any{userID}
  if fleetMode {
    scope = ""
    args = nil
  }
  rows, err := s.db.QueryContext(ctx,
    `SELECT severity, COUNT(*) FROM findings f
     WHERE status = 'open' `+scope+`
     GROUP BY severity`, args...)
  if err != nil {
    return nil, err
  }
  defer rows.Close()
  for rows.Next() {
    var sev string
    var n int
    if err := rows.Scan(&sev, &n); err != nil {
      return nil, err
    }
    out.OpenFindingsBySeverity[sev] = n
  }
  // Repo count
  repoQ := "SELECT COUNT(*) FROM repos"
  if !fleetMode {
    repoQ += " WHERE user_id = ?"
  }
  repoArgs := []any{}
  if !fleetMode {
    repoArgs = append(repoArgs, userID)
  }
  s.db.QueryRowContext(ctx, repoQ, repoArgs...).Scan(&out.RepoCount)
  // Gates failing
  // (skipped detailed code — see internal/finding/gates for the schema)
  return out, nil
}
```

Same shape for `internal/db/postgres_fleet.go` (substitute `?` placeholders with `$N`).

- [ ] **Step 5: Register the route in server.go**

In the `/api/v1` protected group, add (inside the right scope group — `read:scans` is fine since posture aggregates scan data):
```go
r.With(auth.RequireScope(apikey.ScopeReadScans)).Get("/fleet/posture", routes.FleetPosture)
```

- [ ] **Step 6: Add to the OpenAPI spec**

In `internal/api/openapi/spec.go`, in `Endpoints()`:
```go
{"GET", "/fleet/posture", "fleet", "Fleet-wide posture summary", "read:scans", "", false},
```

- [ ] **Step 7: Run the tests; expect PASS**

`go test ./internal/api/routes/ -run TestFleetPosture -v`
Expected: PASS (2 tests).

- [ ] **Step 8: Run the full backend test suite**

`go test ./...`
Expected: 71 packages pass (was 70).

- [ ] **Step 9: Commit**

```bash
git add internal/api/routes/fleet.go internal/api/routes/fleet_test.go \
        internal/db/store.go internal/db/sqlite_fleet.go internal/db/postgres_fleet.go \
        internal/api/server.go internal/api/openapi/spec.go
git commit -m "feat(api): GET /fleet/posture aggregate endpoint"
```

---

### Task B2: Fleet inventory endpoint

**Files:**
- Add handler to `internal/api/routes/fleet.go`
- Add test to `internal/api/routes/fleet_test.go`
- Extend `Store` interface + implementations.

- [ ] **Step 1: Write the failing test**

```go
func TestFleetInventoryGroupsByEverything(t *testing.T) {
  env := setupTestEnv(t)
  defer env.Store.Close()
  // Seed 3 repos: 1 local Go, 1 github TypeScript, 1 ssh Python
  // … standard pattern …
  w := env.doRequest(http.MethodGet, "/api/v1/fleet/inventory", nil)
  if w.Code != http.StatusOK { t.Fatalf("got %d", w.Code) }
  var got struct {
    Data struct {
      BySourceType map[string]int `json:"by_source_type"`
      ByCollection map[string]int `json:"by_collection"`
      ByLanguage   map[string]int `json:"by_language"`
    } `json:"data"`
  }
  json.Unmarshal(w.Body.Bytes(), &got)
  if got.Data.BySourceType["local"] != 1 || got.Data.BySourceType["github"] != 1 || got.Data.BySourceType["ssh"] != 1 {
    t.Errorf("inventory wrong: %+v", got.Data)
  }
}
```

- [ ] **Step 2: Implement the handler and store method**

Pattern mirrors B1. The store method is `FleetInventory(ctx, userID, fleetMode) (*FleetInventoryResult, error)` returning three string-keyed maps. Implementation is three `GROUP BY` queries.

- [ ] **Step 3: Register + spec + tests pass + commit**

Same as B1.

```bash
git commit -m "feat(api): GET /fleet/inventory grouping breakdown"
```

---

### Task B3: Needs-attention endpoint

**Files:** Same as B1/B2.

- [ ] **Step 1: Test + handler + store**

`/fleet/needs-attention` returns the top 10 repos by composite score:
```
score = 10 * new_critical + 5 * new_high + 8 * (gate_failing ? 1 : 0) + 1 * stale_days_over_30
```

Test seeds 5 repos with varied posture and asserts the order matches the formula.

- [ ] **Step 2: Implementation**

The store method is a single Go function that:
1. Lists all repos in scope (user_id or all).
2. For each, fetches last scan + critical/high counts from that scan + gate status + scan recency.
3. Computes the score, sorts, returns top 10.

This is O(N) on repo count; N≤100 is fine. If it ever needs caching, add a 60s TTL.

- [ ] **Step 3: Register + spec + tests + commit**

```bash
git commit -m "feat(api): GET /fleet/needs-attention scored top-N"
```

---

### Task B4: Top vulnerable components endpoint

**Files:** Same as B1/B2.

- [ ] **Step 1: Test**

`/findings/aggregate?group_by=rule_id&limit=10` returns the 10 rule_ids with the highest repo-count.

```go
func TestFindingsAggregateByRule(t *testing.T) {
  // Seed: findings for rule_id "log4j-1.2" across 4 repos
  //       findings for rule_id "openssl-1.0.2k" across 2 repos
  //       findings for rule_id "internal-only" in 1 repo
  // …
  w := env.doRequest(http.MethodGet, "/api/v1/findings/aggregate?group_by=rule_id&limit=10", nil)
  // assert log4j first with repos=4
}
```

- [ ] **Step 2: Implementation**

```sql
SELECT rule_id, COUNT(DISTINCT repo_id) AS repos, COUNT(*) AS findings
FROM findings
WHERE status = 'open' AND rule_id != ''
  -- scoped by user_id if not fleet_mode
GROUP BY rule_id
ORDER BY repos DESC, findings DESC
LIMIT ?
```

- [ ] **Step 3: Test passes + commit**

```bash
git commit -m "feat(api): GET /findings/aggregate?group_by=rule_id"
```

---

## Phase B2 — Fleet dashboard UI

### Task B5: Fleet API hooks library

**Files:**
- Create: `ui/src/lib/fleet.ts`

- [ ] **Step 1: Write the hooks**

```ts
// ui/src/lib/fleet.ts
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export type FleetPosture = {
  open_findings_by_severity: Record<string, number>;
  week_over_week_delta: Record<string, number>;
  repo_count: number;
  gates_failing: number;
};

export function useFleetPosture() {
  return useQuery({
    queryKey: ["fleet", "posture"],
    queryFn: async () => {
      const { data } = await api.get<{ data: FleetPosture }>("/fleet/posture");
      return data.data;
    },
    staleTime: 30_000,
  });
}

export type FleetInventory = {
  by_source_type: Record<string, number>;
  by_collection: Record<string, number>;
  by_language: Record<string, number>;
};

export function useFleetInventory() {
  return useQuery({
    queryKey: ["fleet", "inventory"],
    queryFn: async () => {
      const { data } = await api.get<{ data: FleetInventory }>("/fleet/inventory");
      return data.data;
    },
    staleTime: 60_000,
  });
}

export type NeedsAttentionRow = {
  repo_id: string;
  name: string;
  reason: "gate_failing" | "stale" | "new_high" | "scan_failed";
  detail: string;
  score: number;
};

export function useNeedsAttention() {
  return useQuery({
    queryKey: ["fleet", "needs-attention"],
    queryFn: async () => {
      const { data } = await api.get<{ data: NeedsAttentionRow[] }>("/fleet/needs-attention");
      return data.data;
    },
    staleTime: 30_000,
  });
}

export type AggregateRow = { key: string; repos: number; findings: number };

export function useTopVulnerableRules(limit = 10) {
  return useQuery({
    queryKey: ["findings", "aggregate", "rule_id", limit],
    queryFn: async () => {
      const { data } = await api.get<{ data: AggregateRow[] }>(
        `/findings/aggregate?group_by=rule_id&limit=${limit}`,
      );
      return data.data;
    },
    staleTime: 60_000,
  });
}
```

- [ ] **Step 2: Commit**

```bash
git add ui/src/lib/fleet.ts
git commit -m "feat(ui): fleet API hooks (posture/inventory/needs-attention/aggregate)"
```

---

### Task B6: Posture cards component

**Files:**
- Create: `ui/src/components/fleet/posture-cards.tsx`

- [ ] **Step 1: Implement the component**

```tsx
// ui/src/components/fleet/posture-cards.tsx
import { Card, CardContent } from "@/components/ui/card";
import { useFleetPosture } from "@/lib/fleet";
import { Skeleton } from "@/components/ui/skeleton";
import { TrendingUpIcon, TrendingDownIcon, MinusIcon } from "lucide-react";

export function PostureCards() {
  const q = useFleetPosture();
  if (q.isLoading) {
    return (
      <div className="grid grid-cols-4 gap-3">
        {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-28" />)}
      </div>
    );
  }
  if (!q.data) return null;
  const sev = q.data.open_findings_by_severity;
  const delta = q.data.week_over_week_delta;
  const totalOpen = (sev.critical ?? 0) + (sev.high ?? 0) + (sev.medium ?? 0) + (sev.low ?? 0) + (sev.info ?? 0);
  const totalDelta = Object.values(delta).reduce((a, b) => a + b, 0);

  return (
    <div className="grid grid-cols-4 gap-3">
      <PostureCard label="Open findings" value={totalOpen} delta={totalDelta} />
      <PostureCard label="High severity" value={sev.high ?? 0} delta={delta.high ?? 0} />
      <PostureCard label="Critical severity" value={sev.critical ?? 0} delta={delta.critical ?? 0} />
      <PostureCard label="Gates failing" value={q.data.gates_failing} />
    </div>
  );
}

function PostureCard({ label, value, delta }: { label: string; value: number; delta?: number }) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
        <div className="mt-1 text-3xl font-semibold tabular-nums">{value}</div>
        {delta !== undefined && <DeltaPill delta={delta} />}
      </CardContent>
    </Card>
  );
}

function DeltaPill({ delta }: { delta: number }) {
  const Icon = delta > 0 ? TrendingUpIcon : delta < 0 ? TrendingDownIcon : MinusIcon;
  const tone = delta > 0 ? "text-red-400" : delta < 0 ? "text-emerald-400" : "text-muted-foreground";
  return (
    <div className={`mt-2 inline-flex items-center gap-1 text-xs ${tone}`}>
      <Icon className="size-3" />
      {delta > 0 ? `+${delta}` : delta} this week
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add ui/src/components/fleet/posture-cards.tsx
git commit -m "feat(ui): fleet posture cards (4-stat row)"
```

---

### Task B7: Severity trend chart

**Files:**
- Create: `ui/src/components/fleet/severity-trend.tsx`

- [ ] **Step 1: Implement**

Uses recharts (already a dependency). Pulls from the existing `GET /findings/trends` endpoint, renders a stacked area chart 90 days back.

(Full code omitted for length — pattern is straightforward: query → recharts AreaChart with five stacked Area components for each severity, styled per the severity-badge color palette.)

- [ ] **Step 2: Commit**

```bash
git commit -m "feat(ui): fleet severity-trend chart"
```

---

### Task B8: Top vulnerable components + needs-attention tables

**Files:**
- Create: `ui/src/components/fleet/top-components.tsx`
- Create: `ui/src/components/fleet/needs-attention.tsx`

Both are shadcn `<Card>` shells wrapping a shadcn `<Table>`. Each row links to a filtered `/findings` or `/repos/{id}` view.

- [ ] **Steps + commit**

```bash
git commit -m "feat(ui): top vulnerable components + needs-attention tables"
```

---

### Task B9: Inventory breakdown + recent activity panels

**Files:**
- Create: `ui/src/components/fleet/inventory-breakdown.tsx`
- Create: `ui/src/components/fleet/recent-activity.tsx`

`inventory-breakdown` is a 3-column key/value layout (by source / by collection / by language). `recent-activity` reads from `/audit-log?limit=5` (which we already have).

- [ ] **Steps + commit**

```bash
git commit -m "feat(ui): inventory breakdown + recent-activity panels"
```

---

### Task B10: Replace the dashboard route

**Files:**
- Modify: `ui/src/routes/_authed.index.tsx` (entire file rewritten)

- [ ] **Step 1: New layout**

```tsx
// ui/src/routes/_authed.index.tsx
import { createFileRoute } from "@tanstack/react-router";
import { PostureCards } from "@/components/fleet/posture-cards";
import { SeverityTrend } from "@/components/fleet/severity-trend";
import { TopComponents } from "@/components/fleet/top-components";
import { NeedsAttention } from "@/components/fleet/needs-attention";
import { InventoryBreakdown } from "@/components/fleet/inventory-breakdown";
import { RecentActivity } from "@/components/fleet/recent-activity";

export const Route = createFileRoute("/_authed/")({ component: FleetDashboard });

function FleetDashboard() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Fleet</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Open findings, posture, and inventory across every repository wolf manages.
        </p>
      </div>
      <PostureCards />
      <SeverityTrend />
      <div className="grid grid-cols-2 gap-6">
        <TopComponents />
        <NeedsAttention />
      </div>
      <div className="grid grid-cols-2 gap-6">
        <InventoryBreakdown />
        <RecentActivity />
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Build + visual check**

The fleet dashboard renders at `/`. Empty fleet shows zeros + an "Add repos to get started" prompt.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(ui): fleet dashboard replaces the empty-state Dashboard at /"
```

---

## Phase B3 — `/repos` UX

### Task B11: Filter bar

**Files:**
- Create: `ui/src/components/repos/filter-bar.tsx`
- Modify: `ui/src/routes/_authed.repos.index.tsx`

- [ ] **Step 1: Implement filter chips**

Filter dimensions: source-type (local/github/ssh/git), collection (multi-select), last-scan status (clean/open-high/open-critical/none/failed).

Each chip is a shadcn `<DropdownMenu>` with checkboxes. Selected values bind to URL query params so the filter state is shareable.

- [ ] **Step 2: Apply to the list**

The repos list filters client-side based on the URL params. (Future: push to server-side when the list grows above 500.)

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(ui): /repos filter bar (source-type, collection, last-scan)"
```

---

### Task B12: Group-by toggle

**Files:**
- Create: `ui/src/components/repos/group-toggle.tsx`
- Modify: `ui/src/routes/_authed.repos.index.tsx`

- [ ] **Step 1: Add the toggle**

shadcn `<Tabs>` (none / source-type / collection / language). State in URL query param `?group=source_type`.

- [ ] **Step 2: Grouped render**

When `group=source_type`, render the list as collapsible groups using shadcn `<Card>` per group with the count in the header.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(ui): /repos group-by toggle"
```

---

### Task B13: Bulk select + toolbar

**Files:**
- Create: `ui/src/components/repos/bulk-toolbar.tsx`
- Modify: `ui/src/routes/_authed.repos.index.tsx`
- Modify: `ui/src/components/repos/filter-bar.tsx` (add "Select all visible")

- [ ] **Step 1: Add a checkbox column**

Use shadcn `<Checkbox>`. State: a `Set<string>` of selected repo IDs, stored in `useState`.

- [ ] **Step 2: Floating toolbar**

When `selected.size > 0`, render a fixed-bottom-center toolbar:
```
N selected · [Scan ↗] [Add to collection] [Apply policy] [Delete] [Clear]
```

Each action opens a small confirmation/picker dialog. Scan calls `POST /scans` per repo with `?bulk=true` (the server accepts an array via a follow-up — for now N parallel `POST /scans` is fine).

- [ ] **Step 3: Tests + commit**

```bash
git commit -m "feat(ui): /repos bulk select + actions toolbar"
```

---

## Phase B4 — Import wizards

### Task B14: GitHub org list endpoint

**Files:**
- Create: `internal/github/api.go`
- Create: `internal/github/api_test.go`
- Add handler to `internal/api/routes/fleet.go` (or new `routes/github.go`)
- Add test
- Register route + add to spec

- [ ] **Step 1: Test the GitHub client (HTTP-mocked)**

```go
func TestGitHubListOrgRepos(t *testing.T) {
  srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if !strings.HasPrefix(r.URL.Path, "/orgs/acme/repos") {
      t.Fatalf("unexpected path %q", r.URL.Path)
    }
    w.Header().Set("Content-Type", "application/json")
    fmt.Fprintf(w, `[{"name":"api","full_name":"acme/api","default_branch":"main","private":true,"archived":false},{"name":"web","full_name":"acme/web","default_branch":"main","private":false,"archived":false}]`)
  }))
  defer srv.Close()
  c := &github.Client{BaseURL: srv.URL, Token: "ghp_x"}
  repos, err := c.ListOrgRepos(context.Background(), "acme")
  if err != nil { t.Fatal(err) }
  if len(repos) != 2 || repos[0].FullName != "acme/api" { t.Errorf("got %+v", repos) }
}
```

- [ ] **Step 2: Implement the client**

```go
// internal/github/api.go
package github

import (
  "context"
  "encoding/json"
  "fmt"
  "io"
  "net/http"
)

type Repo struct {
  Name          string `json:"name"`
  FullName      string `json:"full_name"`
  DefaultBranch string `json:"default_branch"`
  Private       bool   `json:"private"`
  Archived      bool   `json:"archived"`
  Language      string `json:"language"`
}

type Client struct {
  BaseURL string // defaults to https://api.github.com
  Token   string
  HTTP    *http.Client
}

func New(token string) *Client {
  return &Client{BaseURL: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) ListOrgRepos(ctx context.Context, org string) ([]Repo, error) {
  var all []Repo
  page := 1
  for {
    url := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&page=%d&type=all", c.BaseURL, org, page)
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    req.Header.Set("Accept", "application/vnd.github+json")
    if c.Token != "" { req.Header.Set("Authorization", "Bearer "+c.Token) }
    resp, err := c.HTTP.Do(req)
    if err != nil { return nil, err }
    body, _ := io.ReadAll(resp.Body)
    resp.Body.Close()
    if resp.StatusCode == 404 {
      // Try as user instead of org.
      return c.ListUserRepos(ctx, org)
    }
    if resp.StatusCode >= 400 {
      return nil, fmt.Errorf("github %d: %s", resp.StatusCode, string(body))
    }
    var batch []Repo
    if err := json.Unmarshal(body, &batch); err != nil { return nil, err }
    all = append(all, batch...)
    if len(batch) < 100 { break }
    page++
  }
  return all, nil
}

func (c *Client) ListUserRepos(ctx context.Context, user string) ([]Repo, error) {
  // similar — GET /users/{user}/repos
  // …
}
```

- [ ] **Step 3: Handler in routes/fleet.go**

```go
type listOrgReposRequest struct {
  Org      string `json:"org"`
  SecretID string `json:"secret_id"` // optional; fall back to user's first github_token
}

func ListOrgGitHubRepos(w http.ResponseWriter, r *http.Request) {
  // 1. Get user, look up the github_token secret (by secret_id or first one owned)
  // 2. Decrypt the token via internal/secrets
  // 3. Use github.New(token).ListOrgRepos(ctx, req.Org)
  // 4. Return the list
}
```

- [ ] **Step 4: Tests + commit**

```bash
git commit -m "feat(api): POST /sources/github/list-org-repos (org repo discovery)"
```

---

### Task B15: GitHub import modal

**Files:**
- Create: `ui/src/components/repos/import-github-modal.tsx`
- Modify: `ui/src/routes/_authed.repos.index.tsx` (add the "Import from GitHub" button)

- [ ] **Step 1: Multi-step modal**

shadcn `<Dialog>` with three steps:

Step 1: Pick credential. Calls `GET /config/secrets`, filters to `key_type === "github_token"`. If none exist, show "Add a github_token secret first" with a link to Settings → Secrets.

Step 2: Enter org/user. On submit, calls the new `POST /sources/github/list-org-repos`. Show the list in a shadcn `<Table>` with a checkbox column, plus filters (private/public, archived show/hide).

Step 3: Pick target collection (optional). Show "Import N repos" button. On click, fires N parallel `POST /repos` with `source_type=github`, `source_path=full_name`, default_branch from the GitHub response.

- [ ] **Step 2: Tests + commit**

```bash
git commit -m "feat(ui): GitHub org import wizard modal"
```

---

### Task B16: SSH discover-repos endpoint

**Files:**
- Modify: `internal/api/routes/nodes.go`
- Add test
- Update spec

- [ ] **Step 1: Test**

```go
func TestNodeDiscoverRepos(t *testing.T) {
  // Seed a node with a fakeSSHRunner that, when asked to find . -name .git,
  // returns three known paths.
  // Hit POST /api/v1/nodes/{id}/discover-repos { "base_path": "/srv/code" }
  // Assert response includes three entries with name + branch + last_commit.
}
```

- [ ] **Step 2: Implementation**

Run on the remote node:
```bash
find <base_path> -maxdepth 3 -name .git -type d 2>/dev/null
```
For each found `.git`, get the parent path. For each parent, also fetch `git rev-parse --abbrev-ref HEAD` and `git rev-parse HEAD`. Returns:
```json
{
  "data": [
    { "name": "api", "path": "/srv/code/api", "branch": "main", "commit_sha": "abc123" },
    ...
  ]
}
```

- [ ] **Step 3: Tests + commit**

```bash
git commit -m "feat(api): POST /nodes/{id}/discover-repos (SSH walk for .git/)"
```

---

### Task B17: SSH discover modal

**Files:**
- Create: `ui/src/components/repos/discover-ssh-modal.tsx`
- Modify: a node detail page or the Settings → Nodes tab

- [ ] **Step 1: Modal flow**

Step 1: Pick node (or pre-fill if launched from a node detail page).
Step 2: Enter base path. Submit triggers `POST /nodes/{id}/discover-repos`.
Step 3: Pick repos. shadcn `<Table>` with checkbox column.
Step 4: Pick collection (optional). Import N → N parallel `POST /repos` with `source_type=ssh`, `remote_node_id=node`, `source_path=path`.

- [ ] **Step 2: Tests + commit**

```bash
git commit -m "feat(ui): SSH discover-repos wizard modal"
```

---

## Phase B5 — Fleet mode (org-wide visibility)

### Task B18: Fleet-mode setting + handler scoping

**Files:**
- Create: `internal/db/migrations/020_fleet_mode.sql` (seeds `fleet_mode=false`)
- Modify: `internal/api/routes/repos.go`, `scans.go`, `findings.go`, `collections.go` (consult `fleet_mode` setting; when true, skip the `user_id` filter on list endpoints)
- Modify: `ui/src/routes/_authed.settings.tsx` (add the toggle under General)

- [ ] **Step 1: Migration**

```sql
-- 020_fleet_mode.sql
INSERT OR IGNORE INTO settings (key, value) VALUES ('fleet_mode', 'false');
```

Postgres equivalent: `ON CONFLICT(key) DO NOTHING`.

- [ ] **Step 2: Handler changes**

Each list handler reads the setting (or accepts it through context) and chooses between `ListReposByUser(ctx, userID)` and a new `ListAllRepos(ctx)`. Add `ListAll*` methods to the Store interface.

- [ ] **Step 3: Tests**

Two tests per resource:
- Default (`fleet_mode=false`): user A's repos invisible to user B.
- Fleet mode on: user A's repos visible to user B (since both authenticate with the right scope).

- [ ] **Step 4: Settings UI**

Add to `GENERAL_KNOBS` in `_authed.settings.tsx`:
```ts
{
  key: "fleet_mode",
  label: "Fleet mode",
  help: "When on, Repos / Scans / Findings / Collections are visible to anyone in the org with the matching read scope, not just their creator. Recommended for installs with multiple users sharing a fleet of >20 repos. Default off preserves single-user privacy.",
  type: "bool" as const,
},
```

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: fleet_mode setting — org-wide repo/scan/finding visibility"
```

---

## Phase B6 — Smoke + polish

### Task B19: Per-collection posture

**Files:**
- Modify: `ui/src/routes/_authed.collections.$collectionId.tsx`

- [ ] **Step 1: Add a small posture summary at the top**

Reuses `PostureCards` but scoped to the collection (new query param: `GET /fleet/posture?collection_id=...`).

- [ ] **Step 2: Backend support**

Extend `FleetPosture` to accept an optional `collection_id` filter.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(ui): per-collection posture summary on collection detail"
```

---

### Task B20: README + final smoke + push

**Files:**
- Modify: `README.md` (add Fleet Mode subsection)

- [ ] **Step 1: README**

Add under "Web UI":
```markdown
### Fleet mode

When you're managing 20+ repos across multiple hosts:
- Flip the `fleet_mode` setting in Settings → General. All Repos / Scans / Findings / Collections become visible org-wide.
- Use **Import from GitHub** on the Repos page to pull every repo from an org via your `github_token` secret.
- Use **Discover repos** on a node detail page to multi-select git directories on that SSH host.
- The **/** Fleet dashboard shows posture, vulnerable components, attention list, and inventory across the whole fleet.
```

- [ ] **Step 2: Full smoke**

Per the Definition of Done item 28 above: fresh DB, flip fleet_mode on, import 3 GitHub repos, bulk-scan, watch posture cards populate.

- [ ] **Step 3: Final test runs**

```bash
go vet ./...
go test ./...
cd ui && pnpm test && pnpm build && pnpm typecheck && cd ..
```

All green.

- [ ] **Step 4: Push**

```bash
git push origin main
```

---

## Out of scope — deferred to future plans

These items were flagged in the prior walkthrough audit but don't belong in this plan. Each gets its own design pass:

- **Baselines UI** — `docs/superpowers/specs/2026-06-15-scan-quality-ui-design.md`
- **Quality gates / policies UI** — same spec
- **Suppressions list UI** — same spec
- **SARIF import/export UI** — same spec
- **Per-source-type identity / multi-source-per-repo** — design rejected after fleet-mode conversation; current flat model serves the fleet workflow.

---

## Execution

Two paths:

1. **Subagent-Driven (recommended)** — fresh subagent per task plus adversarial reviewer; 20 tasks, ~6 hours wall time.
2. **Inline execution** — work sequentially in this session.

The plan is sized so individual phases can be paused. Stage A is a clean shippable unit; Stage B's phases (B1–B6) are also independently shippable.
