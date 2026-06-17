# Account vs. Admin Settings — Implementation Plan

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax for tracking. Backend changes are gated by Go tests + the existing OpenAPI/CLI coverage tests; UI changes are gated by `tsc`, `vite build`, and screenshots.

**Goal:** Split the current grab-bag `/settings` into two clear surfaces — a personal **Account** area (everything scoped to *you*) reached from the top-right avatar menu, and an **admin-only Settings** area (system config + a global oversight view of every user's API keys / secrets / nodes, plus the audit log).

**Architecture:** The same concepts (API keys, secrets, nodes) appear in both places at different scope: under Account you manage *your own*; under Settings an admin sees *everyone's* (visibility + revoke, never secret material). Personal section components are extracted from `settings.tsx` into a shared module so both routes reuse them. New role-gated `GET /admin/tokens` and `GET /admin/secrets` endpoints back the global view (nodes already have admin-sees-all via the fleet-visibility rule). Owner attribution is resolved client-side from the existing users list — no backend joins.

**Tech Stack:** Go 1.26 (chi, sqlx, SQLite + Postgres), React 19 + TanStack Router/Query + Tailwind, cobra CLI.

**Security invariant (load-bearing):** The admin global view shows that things *exist* + metadata, never secret material. API tokens are hash-only (plaintext shown once, never recoverable) so only metadata can ever be exposed. Secrets stay **masked** in the admin view (key type, name, owner, last-4); the admin endpoint must never decrypt another user's value.

---

## Surface map (target state)

**Account** — `/account`, opened from the avatar menu. Visible to everyone. Tabs:
- Profile (display name / email / password)
- Security (2FA)
- API Keys (mine)
- Secrets (mine)
- Nodes (mine)

**Settings** — `/settings`, sidebar gear, **admin-only** (non-admins redirected to `/account`). Tabs:
- General (global toggles)
- Users
- API Keys (all users — oversight + revoke)
- Secrets (all users — masked, existence only + delete)
- Nodes (all users — oversight + delete)
- Scanners
- Audit (moved out of the sidebar)

---

## File Structure

**Backend (create/modify):**
- `internal/db/store.go` — add `ListAllAPITokens`, `ListAllSecrets` to the `Store` interface.
- `internal/db/sqlite.go`, `internal/db/postgres.go` — implement both.
- `internal/api/routes/admin.go` (CREATE) — `AdminListTokens`, `AdminListSecrets` handlers.
- `internal/api/server.go` — wire `/admin/tokens`, `/admin/secrets` (scope+role gated).
- `internal/api/openapi/spec.go` — add the two endpoints to the catalog.
- `internal/cli/cmd_resources.go` — new `admin` group (`tokens`, `secrets`).
- `internal/cli/root.go` — register `newAdminCmd()` in `NewCommandGroups`.
- `internal/db/*_test.go`, `internal/api/admin_test.go` (CREATE) — coverage.

**Frontend (create/modify):**
- `ui/src/components/settings/sections.tsx` (CREATE) — the extracted personal + admin section components, all exported.
- `ui/src/routes/_authed.account.tsx` (CREATE) — personal Account area (tabs).
- `ui/src/routes/_authed.settings.tsx` — becomes the admin shell (admin-gated; imports sections; adds Audit; redirects non-admins).
- `ui/src/components/user-menu.tsx` — point Account / Two-factor / API keys at `/account`.
- `ui/src/components/sidebar.tsx` — remove the Audit nav item.
- `ui/src/routes/_authed.audit.tsx` / `_authed.audit.index.tsx` — redirect to `/settings?tab=audit` (keep deep links working).
- `ui/src/lib/types.ts` — `AdminApiToken` / `AdminSecret` response types (token/secret + `user_id`).

---

## Phase 1 — Backend: admin global-view endpoints

### Task 1: Store methods `ListAllAPITokens` / `ListAllSecrets`

**Files:**
- Modify: `internal/db/store.go`
- Modify: `internal/db/sqlite.go`, `internal/db/postgres.go`
- Test: `internal/db/sqlite_test.go`

- [ ] **Step 1: Add to the `Store` interface** (`internal/db/store.go`), next to the existing per-user methods:

```go
	ListAPITokensByUser(ctx context.Context, userID string) ([]models.APIToken, error)
	ListAllAPITokens(ctx context.Context) ([]models.APIToken, error) // admin oversight
```
```go
	ListSecretsByUser(ctx context.Context, userID string) ([]models.Secret, error)
	ListAllSecrets(ctx context.Context) ([]models.Secret, error) // admin oversight (masked by handler)
```

- [ ] **Step 2: Implement in both stores.** Mirror the `ByUser` methods but drop the `WHERE user_id` filter, ordered newest-first. SQLite + Postgres bodies are identical here (no `$1`):

```go
func (s *SQLiteStore) ListAllAPITokens(ctx context.Context) ([]models.APIToken, error) {
	var t []models.APIToken
	err := s.db.SelectContext(ctx, &t, `SELECT * FROM api_tokens ORDER BY created_at DESC`)
	// decode the JSON scopes column for each, exactly as ListAPITokensByUser does
	return decodeTokenScopes(t), err
}
func (s *SQLiteStore) ListAllSecrets(ctx context.Context) ([]models.Secret, error) {
	var sec []models.Secret
	err := s.db.SelectContext(ctx, &sec, `SELECT * FROM secrets ORDER BY created_at DESC`)
	return sec, err
}
```
Reuse whatever scope-decoding the existing `ListAPITokensByUser` does (factor a shared helper if it inlines the decode). Postgres versions are the same query.

- [ ] **Step 3: Test** (`internal/db/sqlite_test.go`): create two users, give each a token + secret, assert `ListAllAPITokens`/`ListAllSecrets` return both users' rows while `…ByUser` returns one. Run: `go test ./internal/db/ -run AllTokens`.

- [ ] **Step 4: Commit** `feat(db): admin list-all for api tokens + secrets`.

### Task 2: Admin handlers + routes

**Files:**
- Create: `internal/api/routes/admin.go`
- Modify: `internal/api/server.go`, `internal/api/openapi/spec.go`
- Test: `internal/api/admin_test.go`

- [ ] **Step 1: Handlers** (`internal/api/routes/admin.go`). Reuse the per-user secret masking (extract the existing mask helper from `secrets.go` if inline). Tokens carry no secret material, so return as-is (the `TokenHash` field is `json:"-"`). Attach `user_id` (already on both models) so the UI can resolve owners.

```go
// AdminListTokens lists every user's API tokens (metadata only — tokens are
// hash-only and never recoverable). Admin-gated. GET /api/admin/tokens
func AdminListTokens(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	tokens, err := h.Store.ListAllAPITokens(r.Context())
	if err != nil { response.WriteError(w, 500, "server_error", "failed to list tokens"); return }
	response.WriteJSON(w, 200, response.ListResponse{Data: tokens, Meta: response.ListMeta{Total: len(tokens)}})
}

// AdminListSecrets lists every user's secrets, MASKED (existence + metadata
// only; values are never decrypted here). Admin-gated. GET /api/admin/secrets
func AdminListSecrets(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	secs, err := h.Store.ListAllSecrets(r.Context())
	if err != nil { response.WriteError(w, 500, "server_error", "failed to list secrets"); return }
	response.WriteJSON(w, 200, response.ListResponse{Data: maskSecrets(secs), Meta: response.ListMeta{Total: len(secs)}})
}
```
(`maskSecrets` = the same masking the per-user `/config/secrets` list already applies; factor it shared if needed.)

- [ ] **Step 2: Route wiring** (`internal/api/server.go`), in the protected group, gated by BOTH `adminScope` and `adminOnly` (defense-in-depth, matching `/users`):

```go
r.Route("/admin", func(r chi.Router) {
	r.Use(adminScope)
	r.Use(adminOnly)
	r.Get("/tokens", routes.AdminListTokens)
	r.Get("/secrets", routes.AdminListSecrets)
})
```

- [ ] **Step 3: OpenAPI** (`internal/api/openapi/spec.go`) — add under a new `admin` tag:

```go
{"GET", "/admin/tokens", "admin", "List all users' API tokens (admin oversight)", "admin", "", true},
{"GET", "/admin/secrets", "admin", "List all users' secrets, masked (admin oversight)", "admin", "", true},
```

- [ ] **Step 4: Test** (`internal/api/admin_test.go`): with the admin JWT, `GET /admin/tokens` and `/admin/secrets` return 200 and include a second user's seeded rows; assert secret values are masked. The existing `TestEveryEndpointAuthScopeAndReachability` + `TestOpenAPICoversEveryRoute` must stay green. Run `go test ./internal/api/`.

- [ ] **Step 5: Commit** `feat(api): admin oversight endpoints for tokens + secrets`.

### Task 3: CLI `admin` group (keep coverage green)

**Files:**
- Modify: `internal/cli/cmd_resources.go`, `internal/cli/root.go`

- [ ] **Step 1:** Add `newAdminCmd()` to `cmd_resources.go`:

```go
func newAdminCmd() *cobra.Command {
	cmd := group("admin", "Cross-user oversight (admin)")
	cmd.AddCommand(
		listCmd("/admin/tokens", "List all users' API tokens"),
		listCmd("/admin/secrets", "List all users' secrets (masked)"),
	)
	return cmd
}
```
(`listCmd` auto-annotates, so `TestCLICoversEveryEndpoint` will see the two new endpoints covered.)

- [ ] **Step 2:** Register in `NewCommandGroups()` (`root.go`): add `newAdminCmd(),`.

- [ ] **Step 3:** Run `go test ./internal/cli/ -run TestCLICoversEveryEndpoint` → must pass (no new gaps). Commit `feat(cli): admin oversight commands`.

---

## Phase 2 — Frontend: extract sections

### Task 4: Extract section components into a shared module

**Files:**
- Create: `ui/src/components/settings/sections.tsx`
- Modify: `ui/src/routes/_authed.settings.tsx`

The 8 section components (`AccountTab`, `GeneralTab`, `SecurityTab`, `ApiKeysTab`, `SecretsTab`, `NodesTab`, `UsersTab`, `ScannersTab`) and their local helpers currently live inside `_authed.settings.tsx`. Move them verbatim into `sections.tsx` and `export` each, plus every helper they reference (`BoolToggle`, `IntInput`, `NewSecretForm`, the constants `GENERAL_KNOBS`, `API_SCOPES`, `ROLE_PRESETS`, `EXPIRY_OPTIONS`, `KEY_TYPES`, types, etc.).

- [ ] **Step 1:** Cut the section components + their helpers/constants/types from `settings.tsx` into `sections.tsx`. Rename the tab components to `*Section` (e.g. `AccountTab` → `AccountSection`) for clarity. Add the imports they need (`api`, `useMe`, `useQuery`, etc.) at the top of `sections.tsx`.
- [ ] **Step 2:** In `settings.tsx`, import what it still renders from `./sections`.
- [ ] **Step 3:** `cd ui && pnpm exec tsc --noEmit` → clean. Commit `refactor(ui): extract settings sections into a shared module`.

> No behavior change yet — this is a pure move so the next tasks can compose the sections into two routes.

---

## Phase 3 — Frontend: the two routes

### Task 5: `/account` personal area

**Files:**
- Create: `ui/src/routes/_authed.account.tsx`

- [ ] **Step 1:** New route with a `?section=` search param (default `profile`), rendering personal sections only:

```tsx
export const Route = createFileRoute("/_authed/account")({
  validateSearch: (s) => ({
    section: /^(profile|security|apikeys|secrets|nodes)$/.test(String(s.section ?? ""))
      ? (s.section as AccountSectionKey) : "profile",
  }),
  component: AccountPage,
});
```
Tabs: Profile (`AccountSection`), Security (`SecuritySection`), API Keys (`ApiKeysSection`), Secrets (`SecretsSection`), Nodes (`NodesSection`) — all from `@/components/settings/sections`. Same tab-bar markup as the current settings page.

- [ ] **Step 2:** `tsc` clean; `pnpm build`. Manual check via screenshot after deploy. Commit `feat(ui): personal /account area`.

### Task 6: `/settings` → admin-only + Audit + global tabs

**Files:**
- Modify: `ui/src/routes/_authed.settings.tsx`
- Create (in `sections.tsx`): `AdminApiKeysSection`, `AdminSecretsSection`, `AdminNodesSection`, `AuditSection`.

- [ ] **Step 1: Admin gate.** In `settings.tsx` `beforeLoad`, redirect non-admins:
```tsx
beforeLoad: async () => {
  const me = await fetchMe();           // GET /auth/me
  if (me?.role !== "admin") throw redirect({ to: "/account" });
},
```
Tabs become: General, Users, API Keys (admin), Secrets (admin), Nodes (admin), Scanners, Audit. Drop the `adminOnly` per-tab filtering (the whole route is admin now).

- [ ] **Step 2: Admin sections** in `sections.tsx`:
  - `AdminApiKeysSection` — `useQuery(["admin","tokens"], GET /admin/tokens)` + `useQuery(["users"], GET /users)` to map `user_id`→email; table: owner · name · prefix · scopes · last used · expires · **Revoke** (`DELETE /auth/tokens/{id}` — already admin-permitted). No create.
  - `AdminSecretsSection` — `GET /admin/secrets` + users map; table: owner · type · name · masked value · created · **Delete** (`DELETE /config/secrets/{id}`). No create.
  - `AdminNodesSection` — `GET /nodes` (admin already sees all) + users map; table: owner · name · host · enabled · **Delete**. No create.
  - `AuditSection` — move the audit-list query/table out of `_authed.audit.index.tsx` into this section (or import the existing list component).

- [ ] **Step 3:** Add `AdminApiToken` / `AdminSecret` types to `ui/src/lib/types.ts` (existing `ApiToken`/`MaskedSecret` + `user_id`).
- [ ] **Step 4:** `tsc` clean; `pnpm build`. Commit `feat(ui): admin Settings — global oversight + audit`.

---

## Phase 4 — Frontend: navigation + redirects

### Task 7: Avatar menu, sidebar, redirects

**Files:**
- Modify: `ui/src/components/user-menu.tsx`, `ui/src/components/sidebar.tsx`, `ui/src/routes/_authed.audit.tsx`

- [ ] **Step 1: Avatar menu** (`user-menu.tsx`): repoint items at `/account`:
  - Account → `/account?section=profile`
  - Two-factor auth → `/account?section=security`
  - API keys → `/account?section=apikeys`
  - (Sign out unchanged.)
- [ ] **Step 2: Sidebar** (`sidebar.tsx`): remove the `Audit` footer-nav entry (it now lives in Settings). Keep the admin-gated `Settings` gear (already `useIsAdmin`-gated via `footerNav`). Confirm the gear is hidden for non-admins.
- [ ] **Step 3: Redirects** — keep old deep links alive:
  - `_authed.audit.tsx` (or its index): `beforeLoad` → `redirect({ to: "/settings", search: { tab: "audit" } })`.
  - In `settings.tsx` `validateSearch`, map legacy personal tabs (`account|security|apikeys|secrets|nodes`) → `redirect({ to: "/account", search: { section } })`. (Do this in `beforeLoad` since redirects can't run in `validateSearch`.)
- [ ] **Step 4:** `tsc` clean; `pnpm build`. Commit `feat(ui): route account vs settings; fold audit into settings`.

---

## Phase 5 — Verify + ship

### Task 8: Full verification

- [ ] `go test ./...` green (incl. OpenAPI/CLI/reachability gates).
- [ ] `cd ui && pnpm exec tsc --noEmit && pnpm build` clean.
- [ ] `docker compose up -d --build`; log in as admin:
  - Avatar → Account shows Profile/Security/API Keys/Secrets/Nodes (yours).
  - Sidebar gear → Settings shows General/Users/API Keys(all)/Secrets(all, masked)/Nodes(all)/Scanners/Audit.
  - Old links: `/settings?tab=account` → `/account`; `/audit` → `/settings?tab=audit`.
- [ ] Seed a second user with a token + secret; confirm admin sees both in the global tabs and that secret values are masked.
- [ ] Confirm a **non-admin** session: no Settings gear, `/settings` redirects to `/account`, `/admin/*` returns 403.
- [ ] Screenshot both areas (light + dark).

### Task 9: Commit + push

- [ ] Squash-free: the per-task commits above. Push to `main` on the user's go-ahead.

---

## Self-review checklist
- **Security:** admin secret view masked (Task 2 Step 1 + Task 6 Step 2); admin endpoints role-gated (Task 2 Step 2); non-admin 403 verified (Task 8).
- **No dead links:** legacy `?tab=` + `/audit` redirected (Task 7 Step 3).
- **Gates stay green:** OpenAPI coverage (Task 2 Step 3), CLI coverage (Task 3 Step 3), reachability (Task 2 Step 4).
- **Type consistency:** `AdminApiToken`/`AdminSecret` defined once (Task 6 Step 3) and used by the admin sections.
- **Owner attribution:** resolved client-side from `/users` — no backend joins, no N+1.
