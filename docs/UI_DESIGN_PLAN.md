# UI design-system and admin-console plan

Status: executing (Phase 1 done on branch `ui/design-foundation`). Owner decision 2026-10-27. This elaborates workstream **U** in
[`PLAN.md`](PLAN.md); it is not a second execution plan. Check tasks off here and
reference the evidence in `PLAN.md` §U when they land.

## Why

An audit of `ui/src` found a good set of shared primitives that routes do not use
consistently:

| Finding | Evidence |
| --- | --- |
| `MetricCard` is imported by no route | `grep -rl MetricCard ui/src` returns only its own file |
| `Card` is used in about 8 places; most list pages are bare tables | `routes/_authed.*.tsx` |
| Page header primitives are skipped by several pages | scan detail, agent detail, fix pages |
| `routes/_authed.settings.tsx` is a 3,110-line file with 8 tables and 8 inline `<form>`s | `wc -l`, `grep -c "<form"` |
| Progressive disclosure is hand-written `<details>` in about 10 places, no shared component | `grep -rn "<details"` |
| Create flows are inline forms (`NewSecretForm`, `NewNodeForm`, `NewUserForm`, …), not dialogs | `routes/_authed.settings.tsx` |
| One `window.confirm` remains | `routes/_authed.findings.$findingId.tsx:60` |
| Enterprise admin is three thin routes plus a generic `RecordsPanel` catch-all | `routes/_authed.enterprise.*` |
| Theme is the Astronomer shadcn/zinc set (Inter), unrelated to the marketing site | `ui/src/styles/globals.css` |

The marketing site (`wolf-site`) has the better visual language: a cool-gray base,
a single steel-silver accent, Archivo / Archivo Narrow / Geist Mono type, hard
borders. The product adopts it, so site, console and admin read as one system.

## Design rules (apply to every screen)

1. **One global stylesheet.** Colour, type, radius, spacing, shadow and status
   tokens live in `ui/src/styles/globals.css` only. No hex colours, no one-off
   `style={{}}` colours, no per-route CSS. Components compose tokens and the
   semantic classes already in `@layer components`.
2. **Light and dark.** `:root` is light, `.dark` is dark, as today. Every token
   has both values and meets WCAG AA (4.5:1 text, 3:1 UI). The existing axe check
   in `e2e/product.spec.ts` must keep passing.
3. **Page template.** Every route renders `PageShell` → `PageHeader`
   (title, description, primary action) → optional KPI strip (`MetricCard`s) →
   filter bar → one `Card`-wrapped `DataTable` or card grid → detail in a
   `Sheet`. No route hand-rolls its header.
4. **Progressive disclosure.** Default view shows the decision-relevant 20%. The
   rest sits behind a shared `Section` (collapsible) component, a `Sheet` for
   row detail, or an "Advanced" section. Raw `<details>` elements are replaced.
5. **No bare forms.** A form never sits loose on a page. Creating a thing is a
   **`+ Add …` button** in the `PageHeader` or card header that opens a `Dialog`
   (short forms) or `Sheet` (long forms). Editing is the same dialog pre-filled.
   Settings-style toggles save per field with a toast, inside a titled card.
6. **Modals for confirmation.** Every destructive or irreversible action
   (delete, revoke, rotate, disable, run, merge) goes through `ConfirmDialog`.
   `window.confirm` / `alert` are banned (add an ESLint rule). Destructive
   confirms that remove records name the target; high-risk ones require typing
   the name.
7. **Empty, loading, error.** Every list has an `EmptyState` (with the `+ Add`
   action where one exists), a `Skeleton` loading state, and an `Alert` error
   state. Same components everywhere.
8. **Tables.** `DataTable` everywhere: sortable headers where the API sorts,
   sticky header, row actions in a trailing `DropdownMenu` (never a row of
   buttons), `StatusBadge` for state, mono for ids/digests, pagination footer.
9. **Enterprise edition gating.** Enterprise-only surfaces use one `EditionGate`
   treatment (locked card with what it does and a contact link), not ad-hoc text.

## Phases and order

Order is by weakness: shared foundation, then Settings and Enterprise admin,
then the list pages. Phases 1–3 are separate PRs (see `PLAN.md` fast path and
`AGENTS.md`: do not combine redesign with schema or license changes).

### Phase 1 — Foundation (theme + primitives)

- [x] **T1.1 Tokens.** Replace the zinc/Inter token values in `globals.css` with
  the site palette. Light: bg `#eef0f3`, panel `#f7f8fa`, line `#cdd3da`, ink
  `#111418`, muted `#525b66`, accent fill `#3c4855` on `#f3f5f7`. Dark: bg
  `#0e1114`, panel `#15191d`, line `#353c44`, ink `#eef1f4`, muted `#a7b0ba`,
  accent `#b8c4d0` on `#0e1114`. Express as the existing HSL triples so
  `@theme inline` and every shadcn component follow. Keep the status scale
  (success/warning/error/info) but retune to the cool base; re-verify AA.
  Evidence: site palette as HSL triples in `globals.css`; status/severity scales retuned, every tone >= 4.5:1 on bg/panel/muted with a 12% wash; axe clean on 18 routes x 2 themes (`e2e/visual-baseline.spec.ts`).
- [x] **T1.2 Type.** Self-host Archivo, Archivo Narrow (nav, eyebrows, table
  headers), Geist Mono (ids, digests, code) via `@fontsource`. Replace Inter /
  Hanken / JetBrains imports; remove unused font packages.
  Evidence: Archivo, Archivo Narrow, Geist Mono via `@fontsource`; Inter, Hanken, JetBrains packages removed from `package.json` and lockfile.
- [x] **T1.3 Shape.** Site language: 1px `--border` lines, small radius (`0.25rem`),
  restrained shadow, 2px ink rule under the page header. Update `.page`,
  `.panel`, `.glass-card`, `.data-table`, `.nav-item`, `.badge-*`, buttons,
  inputs, focus ring in the shared layer only.
  Evidence: 0.25rem radius, 1px lines, `--shadow-card`, 2px ink rule via `.page-header`, narrow-font table headers/eyebrows/nav sections, all in `globals.css` and `PageHeader`/`StatusBadge` class swaps.
- [x] **T1.4 `Section`** (`components/ui/section.tsx`): titled, collapsible,
  `defaultOpen`, optional summary line, `aria-expanded`, keyboard accessible.
  Replace every raw `<details>` in `routes/` and `components/`.
  Evidence: `components/ui/section.tsx` (+ `section.test.tsx`); `grep -rn "<details" ui/src` is empty.
- [x] **T1.5 `FormDialog`** (`components/ui/form-dialog.tsx`): `Dialog` or `Sheet`
  wrapper with title, description, body, footer (Cancel / primary), pending state,
  inline field errors, focus management, Esc/overlay close with dirty-guard.
  Evidence: `components/ui/form-dialog.tsx` (+ `form-dialog.test.tsx`: field error, pending lock, dirty-guard on Esc/Cancel); `FieldError` helper exported; Dialog or Sheet via `presentation`.
- [x] **T1.6 `AddButton`**: the standard `+ Add <thing>` trigger (icon + label,
  primary variant) used in `PageHeader` actions and `CardHeader`.
  Evidence: `components/ui/add-button.tsx` (ref-forwarding, works as a `DialogTrigger asChild`).
- [x] **T1.7 `EditionGate`** (consolidate the existing Enterprise-only treatment).
  Evidence: `components/ui/edition-gate.tsx` replaces the ad-hoc text in `EnterpriseChrome` and `FixerSetupBanner`; link target from `ENTERPRISE_INFO_URL` (`VITE_ENTERPRISE_INFO_URL`, placeholder default).
- [x] **T1.8 Lint guard.** ESLint `no-restricted-globals` for `confirm`/`alert`
  and `no-restricted-syntax` for hex colour literals in `src/` outside
  `globals.css`. Migrate the one `window.confirm` to `ConfirmDialog`.
  Evidence: ESLint bans `confirm`/`alert` (+ `window.*`) and hex literals in `src/` (tests exempt); suppression confirm is now `ConfirmDialog`; chart, terminal and severity colours read tokens; one documented disable in `theme-color-sync.tsx`.
- [x] **T1.9 Visual baseline.** Playwright screenshot sweep of every route in
  light and dark (reuse `e2e/scanner-api-mock.ts` fixtures) saved as the
  before/after reference; update `e2e/__screenshots__` deliberately.
  Evidence: `e2e/visual-baseline.spec.ts` (18 routes x light/dark, axe per shot); `pnpm test:e2e:baseline` refreshes `e2e/__screenshots__/baseline/`; linux product snapshots updated.

Acceptance: `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm test:e2e` pass; axe
clean in both themes; no raw hex outside `globals.css`; no `window.confirm`.

### Phase 2 — Settings and Enterprise admin

- [ ] **T2.1 Split Settings.** `_authed.settings.tsx` becomes a layout with a
  left sub-nav and one file per section under `routes/` or
  `components/settings/`: General, Account & MFA, Users, API keys, Secrets &
  credentials, Git access, Fixer engines, Scanners & updates, Nodes, Images,
  Security, Scheduling, Database & disk, License. Each section is a page of
  titled `Card`s. Rarely used options go in `Section` ("Advanced"), collapsed.
- [ ] **T2.2 Convert create flows.** `NewSecretForm`, `NewNodeForm`,
  `NewUserForm`, API-key create, credential create (including GitHub/GitLab
  token + host binding) become `+ Add …` → `FormDialog`. Edit uses the same
  dialog. Delete/revoke/rotate use `ConfirmDialog`.
- [ ] **T2.3 Settings tables** move to `DataTable` with trailing action menus,
  `StatusBadge`, mono ids, `EmptyState` with the `+ Add` action.
- [ ] **T2.4 Enterprise hub.** `/enterprise` index: cards grouped by Identity
  & access (SSO/SCIM, tenancy), Governance (compliance, reports, audit, legal
  hold), Integrations (SIEM, ticketing, catalogs), Data (residency, customer
  keys), Operations (fixer fleet, packaging, support, diagnostics). Each card:
  status, one-line purpose, entitlement state, primary action.
- [ ] **T2.5 Enterprise modules** use the page template instead of the generic
  `RecordsPanel` dump: KPI strip where meaningful, `DataTable`, `+ Add` dialogs
  for SIEM/ticketing/catalog/residency records, `ConfirmDialog` on send/delete,
  `Section` for raw payloads. Identity and tenancy follow the same layout.
- [ ] **T2.6 Fixer console** (`components/fixes/*`, `/fixes`): same template;
  engines as cards with state, git access as a card with `+ Add credential`.
- [ ] **T2.7 Tests.** Update Playwright specs for moved controls; add one
  spec per converted create flow (opens dialog, validates, submits, toasts).

Acceptance: no `<form>` rendered outside a dialog/sheet or a search/filter bar;
`settings.tsx` no longer exceeds ~300 lines; every delete path has a confirm
modal; screenshots in both themes.

### Phase 3 — List and detail pages

- [ ] **T3.1 Dashboard** (`/`): `MetricCard` strip (open critical/high, scans
  this week, coverage, MTTR), trend card, needs-attention table card,
  recent-activity card. Reconcile or delete the dead `.stat-card` path.
- [ ] **T3.2 Repositories, Findings, Vulnerabilities, Scans, Collections,
  Coverage, Audit, Agents, Fixes** lists: page template (header → KPI strip →
  filter bar → `DataTable` card), `+ Add` where the entity is user-created
  (repository, collection), row detail in `Sheet` where it avoids a navigation.
- [ ] **T3.3 Detail pages** (repo, scan, finding, vulnerability, collection,
  fix, agent): `PageHeader` with breadcrumbs and status, summary cards on top,
  `Tabs` for the body, evidence/raw data in `Section`.
- [ ] **T3.4 Sidebar and topbar** pick up the new tokens/type; Enterprise group
  links to the hub.
- [ ] **T3.5 Final sweep.** Re-run the T1.9 screenshot sweep, compare to
  baseline, fix regressions; confirm the lint guard finds no violations.

Acceptance: every route uses `PageShell`/`PageHeader`; `MetricCard` is used by
the dashboard and at least the list pages that have a summary; screenshots
consistent; full UI check suite green.

## Out of scope

Backend/API changes, new product features, scanner changes, and the marketing
site (tracked in `wolf-site`). Any behaviour change found during conversion is
recorded under its workstream in `PLAN.md`, not fixed silently here.

## Verification

Per PR: `cd ui && pnpm lint && pnpm typecheck && pnpm test && pnpm build &&
pnpm test:e2e`. Visual: the screenshot sweep (T1.9) in both themes attached to
the PR. Live check against the running console is separate and needs the
operator's approval to deploy.
