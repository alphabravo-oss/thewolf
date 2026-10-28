# UI design-system and admin-console plan

Status: executing (Phases 1 and 3 merged; Phase 2 on branch `ui/settings-enterprise`). Owner decision 2026-10-27. This elaborates workstream **U** in
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

- [x] **T2.1 Split Settings.** `_authed.settings.tsx` becomes a layout with a
  left sub-nav and one file per section under `routes/` or
  `components/settings/`: General, Account & MFA, Users, API keys, Secrets &
  credentials, Git access, Fixer engines, Scanners & updates, Nodes, Images,
  Security, Scheduling, Database & disk, License. Each section is a page of
  titled `Card`s. Rarely used options go in `Section` ("Advanced"), collapsed.
  Evidence: `routes/_authed.settings.tsx` is a 101-line layout (left sub-nav in 5 groups + mobile Select, `?tab=` deep links kept, `scanner-updates` redirects to `scanners&advanced=true`); 14 sections in `components/settings/` (`sections.tsx` registry), each a page of titled `SettingsCard`s with rare options in a collapsed `Section`. `AccountTab`/`SecurityTab`/`ApiKeysTab`/`SecretsTab`/`NodesTab` now live in `components/settings/{account,mfa,api-keys,secrets,nodes}.tsx` (imported by `/account`); `describeScannerChannel`/`isWolfBuiltImage` in `scanner-utils.ts` (+ moved `scanner-utils.test.ts`).
- [x] **T2.2 Convert create flows.** `NewSecretForm`, `NewNodeForm`,
  `NewUserForm`, API-key create, credential create (including GitHub/GitLab
  token + host binding) become `+ Add …` → `FormDialog`. Edit uses the same
  dialog. Delete/revoke/rotate use `ConfirmDialog`.
  Evidence: `NewSecretForm`, `NewNodeForm`, `NewUserForm`, API-key create, GitHub/GitLab credential create (host-bound), license install, profile email/password, MFA enroll/disable and the database DSN are `+ Add …`/action buttons opening `FormDialog` (inline field errors, pending lock, dirty guard); node edit reuses the add dialog. Delete/revoke/disable/enable/reap/role change use `ConfirmDialog`; deleting a user and revoking a used API key require typing the name (`TypedConfirmDialog`). `grep -rn "<form" ui/src/components/settings` is empty (per-field saves use Enter + Save, not forms).
- [x] **T2.3 Settings tables** move to `DataTable` with trailing action menus,
  `StatusBadge`, mono ids, `EmptyState` with the `+ Add` action.
  Evidence: Every settings list (users, API keys incl. all-users, secrets incl. all-users, nodes, git credentials, images x2, entitlements, enterprise records) is a `DataTable` via `SettingsTable`: trailing `RowActions` dropdown, `StatusBadge`, mono ids/digests, and an `EmptyState` with the `+ Add` action when empty.
- [x] **T2.4 Enterprise hub.** `/enterprise` index: cards grouped by Identity
  & access (SSO/SCIM, tenancy), Governance (compliance, reports, audit, legal
  hold), Integrations (SIEM, ticketing, catalogs), Data (residency, customer
  keys), Operations (fixer fleet, packaging, support, diagnostics). Each card:
  status, one-line purpose, entitlement state, primary action.
  Evidence: `/enterprise` (`routes/_authed.enterprise.index.tsx`, registry in `lib/enterprise-modules.ts`): KPI strip, then cards grouped Identity & access / Governance / Integrations / Data / Operations, each with `StatusBadge` entitlement state, one-line purpose, configured-count status and a primary action; locked cards use `EditionGate`. The sidebar Enterprise group is driven by `ui_routes` from the overlay and is owned by the Phase 3 PR (T3.4), so the hub link is left to that change.
- [x] **T2.5 Enterprise modules** use the page template instead of the generic
  `RecordsPanel` dump: KPI strip where meaningful, `DataTable`, `+ Add` dialogs
  for SIEM/ticketing/catalog/residency records, `ConfirmDialog` on send/delete,
  `Section` for raw payloads. Identity and tenancy follow the same layout.
  Evidence: `components/enterprise-records.tsx` is now `EnterpriseChrome` (page template, KPI strip, breadcrumb to the hub) + `RecordsPanel` (`DataTable`, `+ Add <noun>` dialog, send/delete behind `ConfirmDialog`, typed-name delete for keys/residency/workspaces/roles, raw JSON in a collapsed `Section`). SIEM, ticketing, catalogs, residency, customer keys, reports, compliance, support, diagnostics, packaging, attack paths, verification, identity and tenancy all use it. Record schemas belong to the private overlay, so the add dialog sends `name` plus an optional JSON-object field (follow-up: typed fields once the overlay publishes schemas).
- [x] **T2.6 Fixer console** (`components/fixes/*`, `/fixes`): same template;
  engines as cards with state, git access as a card with `+ Add credential`.
  Evidence: Settings → Fixer engines: engine `Card`s with `StatusBadge` state, model/effort and default, sign-in console card, CLI equivalent and prompts in `Section`s, all behind `EditionGate`. Settings → Git access (`components/fixes/git-access.tsx`): credential `DataTable` with `+ Add credential` (GitHub/GitLab token bound to a host) and delete confirm. `/fixes` still redirects to `/agents` (retired route).
- [x] **T2.7 Tests.** Update Playwright specs for moved controls; add one
  spec per converted create flow (opens dialog, validates, submits, toasts).
  Evidence: `e2e/settings-create-flows.spec.ts` (user, API key, secret, node, git credential, password, license: opens dialog, validates, posts, toasts, confirm paths), `e2e/enterprise-admin.spec.ts` (hub states, SIEM add/send/delete, Community lock); `ease`, `product`, `scheduling`, `scan-preparation`, `git-access` specs updated for moved controls; visual baseline gains 12 routes with fixtures (`e2e/__screenshots__/baseline/`, both themes, axe clean). Full suite after merging main (#34): 214 passed / 58 skipped (viewport-gated) / 3 failed — the `scan-live` spec (desktop, mobile, firefox) also fails on the base commit `f100c09`; the `product` inventory snapshots were refreshed for the new Images section.

Acceptance: no `<form>` rendered outside a dialog/sheet or a search/filter bar;
`settings.tsx` no longer exceeds ~300 lines; every delete path has a confirm
modal; screenshots in both themes.

### Phase 3 — List and detail pages

- [x] **T3.1 Dashboard** (`/`): `MetricCard` strip (open critical/high, scans
  this week, coverage, MTTR), trend card, needs-attention table card,
  recent-activity card. Reconcile or delete the dead `.stat-card` path.
  Evidence: Home leads with a `MetricCard` strip (open critical/high with week-over-week trend, scans this week from `/scans`, fresh coverage, gates failing), then priorities and coverage cards, the needs-attention table card and an admin-only recent-activity card, then the existing work/progress cards. The trend card is the existing comparison card ("Are we improving?"): `docs/dashboard.md` keeps `/findings/trends` off Home on purpose, so it is not called. MTTR is not exposed by any endpoint and is not shown. `.stat-card`, `.stat-label`, `.stat-value`, `.stat-delta` are deleted; `PostureCards` (collection detail) uses `MetricCard`.
- [x] **T3.2 Repositories, Findings, Vulnerabilities, Scans, Collections,
  Coverage, Audit, Agents, Fixes** lists: page template (header → KPI strip →
  filter bar → `DataTable` card), `+ Add` where the entity is user-created
  (repository, collection), row detail in `Sheet` where it avoids a navigation.
  Evidence: scans, repos, findings (by repo / rule / list), vulnerabilities, collections, coverage, audit and agents use `PageShell` > `PageHeader` > KPI strip (where existing counts exist) > filter bar > `Card`-wrapped `DataTable` with a trailing `RowActions` menu. Add repo, collection create/edit and New scan are `FormDialog`s (`add-repo-form.tsx` -> `AddRepoDialog`, `collection-form-dialog.tsx`, `new-scan-dialog.tsx`); bulk triage and scan cancel use `ConfirmDialog`; audit row detail is a `Sheet`. `DataTable` gained `selectedKeys` (controlled selection) and `hideHeader`. Vulnerabilities has no KPI strip (the API returns only a total). Fixes list is owned by the Phase 2 branch.
- [x] **T3.3 Detail pages** (repo, scan, finding, vulnerability, collection,
  fix, agent): `PageHeader` with breadcrumbs and status, summary cards on top,
  `Tabs` for the body, evidence/raw data in `Section`.
  Evidence: repo, scan, finding, vulnerability and collection detail use `PageShell` + `PageHeader` (new `breadcrumbs` and `status` props), a `MetricCard` summary strip, `Tabs` for the body (tab list hidden when there is one view) and raw data in `Section`. Merge (was `window.prompt`), verify and cancel go through `FormDialog`/`ConfirmDialog`. `/scans/:id/live` is a redirect with no markup. Agent and fix detail render `FixJobView` in `components/fixes/*` (Phase 2 owns it), so their page-template conversion lands with T2.6.
- [x] **T3.4 Sidebar and topbar** pick up the new tokens/type; Enterprise group
  links to the hub.
  Evidence: sidebar mark/edition chip/group labels and the offline banner use theme tokens (no gradient or raw palette colours); topbar is solid, uses the narrow face, and leaves breadcrumbs to the page header on detail routes (two fewer requests). Enterprise group links unchanged.
- [x] **T3.5 Final sweep.** Re-run the T1.9 screenshot sweep, compare to
  baseline, fix regressions; confirm the lint guard finds no violations.
  Evidence: `e2e/visual-fixtures.ts` (pinned clock) gives the sweep real rows for repos, scans, findings, vulnerabilities, collections, audit, agents and coverage; sweep adds findings-by-repo, vulnerability-detail and collection-detail; screenshots grow the viewport to the content. `pnpm test:e2e:baseline` refreshed both themes (axe clean, 21 routes x 2). `pnpm lint && typecheck && test && build && test:e2e` green (190 passed, 44 skipped).

- [x] **T3.6 Polish pass** (review of the T3.5 baseline screenshots).
  Evidence: (1) Home recent activity uses `describeAudit` (`lib/audit.ts` + `audit.test.ts`; server `event_type`, then request shape, then verb + noun; raw method/path/status in a collapsed "Raw requests" `Section`; the log carries ids, not names, so no resource names are shown). (2) Dashboard rows use equal-height cards with an "Audit log" link; "Keep work moving" and "Are we improving?" are `Section`s (new controlled `open`/`headingLevel` props, heading + named region kept) open only when there is work or progress is unknown/has new findings; KPI links reopen them. (3) `FixJobView`: `PageHeader` (breadcrumbs, status badge, actions), KPI strip, Overview/Decisions `Tabs`, log, why-not-fixed and muted in `Section`s; the `/fixes/*` routes are redirects and `fixer-console.tsx` is the Settings terminal card (no page layout), so neither needed a change; `agents` joins the topbar's page-owned breadcrumbs. (4) Vulnerabilities KPI strip: total from `meta.total`, critical/high, 2+ tools and new-since-baseline computed from the loaded page and labelled "On this page" (no MTTR, no endpoints). (5) Hub edition tile: the real `/edition` always sends `product`; the bad label came from the sweep fixture omitting it, so the fixture now sends it and `editionProduct()` derives the name from the edition when it is missing (Community: "Wolf Community - No commercial license"; Enterprise: "Wolf Enterprise"). (6) Sidebar Enterprise group gets an exact-match "Overview" link to `/enterprise` when the overlay supplies routes (no Enterprise route hard-coded). (7) `.section-page` (72rem) in `globals.css` for Settings and Account bodies. Baselines refreshed (+ `agent-detail`, `fix-detail`); axe clean in both themes.

Acceptance: every route uses `PageShell`/`PageHeader`; `MetricCard` is used by
the dashboard and at least the list pages that have a summary; screenshots
consistent; full UI check suite green.

### Phase 4 — Information architecture and dashboard

Decision (owner, 2026-10-28): remove the sidebar "Enterprise" section. Most
overlay modules are stubs (generic JSON record stores for compliance, reports,
catalogs, residency, customer keys and tenancy; attack paths and verification are
a "type a vulnerability id" form; packaging is a static catalog; support,
diagnostics and proof are a dump and a probe). The real ones (identity, the AI
fixer, SIEM/ticketing) move to where users look for them in Settings, gated with
`EditionGate` at the point of use. Stubs are hidden, with no locked-feature
catalog. Home becomes a dashboard with 7 drill-down charts (separate parallel
PR). Reports is the next Enterprise feature to make real.

- [x] **T4.1 Sidebar.** Home, Repositories, Findings, Vulnerabilities, Scans,
  Fixes (autofix on), Collections, Scanner coverage; Administration: Settings, Audit.
  Evidence: `components/layout/sidebar.tsx` drops the overlay-driven Enterprise
  group; `ui_routes` is removed from the UI `EditionInfo` type (no other reader).
- [x] **T4.2 Settings regroup.** Users & access (Users, Security, Single sign-on,
  Directory sync), Credentials, Integrations, AI fixing (Fixer engines, Git
  access), Scanning, System (Database & disk, License & support).
  Evidence: `components/settings/sections.tsx`; `identity.tsx` (provider table with
  Test sign-in, OIDC/SAML/LDAP configuration help, SCIM base URL), `integrations.tsx`
  (SIEM and ticketing tables, `+ Add destination` `FormDialog` with name/URL/optional
  Authorization, `ConfirmDialog` for Send test and delete), `license-settings.tsx`
  (What's included in plain words, install, Download diagnostics bundle, contact
  link). Old `?tab=identity|siem|ticketing|support|diagnostics` alias to the new keys.
  Roles have no UI beyond the user role selector, so there is no Roles tab.
- [x] **T4.3 Routes.** `/enterprise` and `/enterprise/*` redirect
  (`routes/_authed.enterprise.$.tsx`): identity/group-mappings → `sso`;
  siem/ticketing/integrations → `integrations`; everything else → `license`. Hub,
  identity, tenancy and splat pages, `components/enterprise-records.tsx` and
  `lib/enterprise-modules.ts` are deleted; the vulnerability attack-path and
  verification tabs are removed until they are real.
- [x] **T4.4 Tests.** `e2e/settings-enterprise.spec.ts` (SSO gated in Community and
  usable in Enterprise, SIEM add/validate/send test/delete, License & support
  entitlements and diagnostics download, `/enterprise/*` redirects);
  `enterprise-admin.spec.ts` and the `enterprise-*` baselines are deleted; the sweep
  adds `settings-sso`, `settings-sso-community`, `settings-scim`,
  `settings-integrations`, `settings-license-community`.
  Evidence: `pnpm lint && typecheck && test (67) && build && test:e2e` green (246 passed, 68 skipped viewport-gated); `pnpm test:e2e:baseline` refreshed both themes (66 shots, axe clean); `product` scanner-inventory snapshot refreshed for the regrouped sub-nav.

## Out of scope

Backend/API changes, new product features, scanner changes, and the marketing
site (tracked in `wolf-site`). Any behaviour change found during conversion is
recorded under its workstream in `PLAN.md`, not fixed silently here.

## Verification

Per PR: `cd ui && pnpm lint && pnpm typecheck && pnpm test && pnpm build &&
pnpm test:e2e`. Visual: the screenshot sweep (T1.9) in both themes attached to
the PR. Live check against the running console is separate and needs the
operator's approval to deploy.
