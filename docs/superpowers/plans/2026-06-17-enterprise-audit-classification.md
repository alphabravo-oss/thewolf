# Enterprise Audit Classification — Implementation Plan

> **For agentic workers:** Steps use checkbox (`- [ ]`). Backend gated by Go tests + the OpenAPI/CLI coverage tests; UI by `tsc` + `vite build` + screenshot. **Skip Docker rebuilds until the very end** (host disk is tight).

**Goal:** Turn the audit log from a technical request trail into a classified, security-aware audit log: a semantic **event taxonomy**, a **category**, a **severity**, plus **source IP + user agent**, and capture of **authentication events** (login success/failure, logout) that the mutation-only middleware misses today. Surface category/severity filters in the UI.

**Architecture:** A pure `Classify(method, path) → (event, category, severity)` function maps each route to a semantic event; the audit middleware calls it and also records IP/UA. Auth handlers (login / mfa-login) emit explicit events via a small `routes` audit sink (they're on the public group, so the middleware never sees them). New nullable-default columns extend `audit_log`. The query/filters extend the work already done in `2026-06-16-account-vs-admin-settings` (search/sort/pagination).

**Tech Stack:** Go 1.26 (chi middleware, sqlx), React 19 + TanStack Query.

---

## Vocabulary

**Category** (6): `authentication`, `authorization`, `configuration`, `secrets`, `data`, `system`.

**Severity** (3): `info` (routine), `warning` (sensitive / deletes), `critical` (security-defining: MFA disabled, role change, user delete, admin MFA reset).

**Event taxonomy** (examples; `dot.case`): `auth.login`, `auth.login.failed`, `auth.logout`, `auth.mfa.enabled`, `auth.mfa.disabled`, `auth.mfa.reset`, `auth.password.changed`, `apikey.created`, `apikey.revoked`, `secret.created`, `secret.deleted`, `user.created`, `user.deleted`, `user.role.changed`, `settings.updated`, `node.created/updated/deleted`, `policy.created/updated`, `scanner.image.built`, `plugin.installed`. Unmatched routes default to `<resource>.<verb>` in category `data` (or `configuration` for settings/config), severity `info` (`warning` for DELETE).

---

## File Structure

**Backend:**
- `internal/api/middleware/audit_classify.go` (CREATE) — `Classify(method, path)` + rules table + test.
- `internal/api/middleware/audit.go` — populate EventType/Category/Severity/IP/UserAgent on `AuditEntry`.
- `internal/api/server.go` — pass the new fields through `makeAuditRecorder`.
- `internal/db/migrations/025_audit_classification.sql` (CREATE) + register in `sqlite.go`/`postgres.go`.
- `internal/models/api_token.go` — extend `AuditLogEntry` with the 5 fields.
- `internal/db/store.go` — extend `AuditQuery` with `Category`, `Severity`.
- `internal/db/audit_query.go` — filter on category/severity; search also matches `event_type`.
- `internal/api/routes/audit_events.go` (CREATE) — `RecordAuthEvent(...)` sink + helper; `AuditSink` package var set by server.go.
- `internal/api/routes/auth.go` — emit `auth.login` / `auth.login.failed` in `Login`; `internal/api/routes/mfa.go` — emit in `MFALogin`.
- `internal/api/routes/tokens.go` (ListAuditLog handler) — parse `category` / `severity` params.

**Frontend:**
- `ui/src/routes/_authed.settings.tsx` (`AuditTab`) — Category + Severity filter selects; severity badge; show event + IP.
- `ui/src/lib/types.ts` — extend the audit entry shape (event_type, category, severity, ip).

---

## Phase 1 — Classifier (pure, test-first)

### Task 1: `Classify`

**Files:** Create `internal/api/middleware/audit_classify.go`, `internal/api/middleware/audit_classify_test.go`.

- [ ] **Step 1 (failing test):** assert representative mappings:
```go
{"POST","/api/v1/auth/mfa/disable","auth.mfa.disabled","authentication","critical"}
{"PUT","/api/v1/users/123/role","user.role.changed","authorization","critical"}
{"POST","/api/v1/config/secrets","secret.created","secrets","warning"}
{"DELETE","/api/v1/repos/9","repo.deleted","data","warning"}      // default path, DELETE
{"POST","/api/v1/scans","scan.created","data","info"}             // default path
```
- [ ] **Step 2:** implement. Strip `/api/v1` or `/api` prefix; a `[]rule{method, match func(p string) bool, event, cat, sev}` checked in order; else default from first segment + verb. Keep rules as data; `match` via `strings.HasSuffix` / `strings.Contains` + a `{id}`-agnostic check (e.g. role: `strings.HasPrefix(p,"/users/") && strings.HasSuffix(p,"/role")`). Verb from method (create/update/delete).
- [ ] **Step 3:** `go test ./internal/api/middleware/ -run Classify` passes. Commit.

---

## Phase 2 — Persist the new fields

### Task 2: model + migration + AuditEntry + recorder

- [ ] Add to `models.AuditLogEntry`: `EventType, Category, Severity, IP, UserAgent string` (json+db tags `event_type,category,severity,ip,user_agent`).
- [ ] `025_audit_classification.sql`: `ALTER TABLE audit_log ADD COLUMN event_type TEXT NOT NULL DEFAULT '';` ×5 (event_type, category, severity, ip, user_agent). Register the embed + exec in `sqlite.go` and `postgres.go` (mirror migration 024).
- [ ] `middleware.AuditEntry`: add `EventType, Category, Severity, IP, UserAgent`. In `Audit`: `event, cat, sev := Classify(r.Method, r.URL.Path)`; set them; `IP: clientIP(r)` (first `X-Forwarded-For`, else `RemoteAddr` host); `UserAgent: r.UserAgent()`.
- [ ] `makeAuditRecorder`: copy the 5 fields into the model entry.
- [ ] Build + existing audit tests green. Commit.

### Task 3: auth-event capture (login/logout)

**Files:** Create `internal/api/routes/audit_events.go`; modify `auth.go`, `mfa.go`, `server.go`.

- [ ] `routes.AuditSink func(models.AuditLogEntry)` package var; `server.go` sets it to the same recorder path (store.AppendAuditLog). `RecordAuthEvent(r, userID, event, severity, status)` builds an entry (category `authentication`, IP/UA from `r`) and calls the sink.
- [ ] `Login`: on bad credentials emit `auth.login.failed` (severity `warning`, status 401, userID best-effort/empty); on success (non-MFA) emit `auth.login` (info). `MFALogin`: on success emit `auth.login` (info, with `via=mfa` reflected in event or kept as `auth.login`); on bad code emit `auth.login.failed`.
- [ ] (Logout is authenticated → already captured by the middleware as `auth.logout`.)
- [ ] Test (`internal/api/`): a failed login then a successful login produce `auth.login.failed` + `auth.login` rows with category `authentication`. Commit.

---

## Phase 3 — Query + filters

### Task 4: extend AuditQuery + handler

- [ ] `db.AuditQuery`: add `Category, Severity string`. In `audit_query.go` (both stores): add `category = ?`/`severity = ?` conds; widen search to also match `event_type` (`LOWER(event_type) LIKE ?`).
- [ ] `ListAuditLog` handler: parse `category`, `severity` query params into the AuditQuery.
- [ ] db test: filter by category + severity. Commit.

---

## Phase 4 — UI

### Task 5: AuditTab classification UI

- [ ] `types.ts`: audit entry gains `event_type?`, `category?`, `severity?`, `ip?`.
- [ ] `AuditTab`: add **Category** and **Severity** `<select>` filters (reset to page 1 on change); pass `category`/`severity` to the query. Replace the generic "Action" column with **Event** (`event_type`), add a **Severity** badge (gray/amber/red for info/warning/critical), and show **IP** in the request cell or a column. Keep search/sort/pagination.
- [ ] `tsc` + `pnpm build` clean.

---

## Phase 5 — Verify + ship (single Docker rebuild)

- [ ] `go test ./...`, `tsc`, `vite build` all green.
- [ ] ONE `docker compose up -d --build`; log in; trigger a failed + successful login, disable+enable MFA, change a user role; confirm the Audit tab shows classified events with categories/severities, filters work, and IP is populated.
- [ ] Screenshot. Commit. Push on the user's go-ahead.

## Self-review
- **No injection:** category/severity/sort come from fixed vocab; only `event_type`/path/q are `LIKE`-bound parameters.
- **Auth events captured:** login success/failure now recorded despite being on the public group (Task 3).
- **Back-compat:** new columns default `''`; old rows render with empty event/category (UI falls back to method+path).
- **Gates:** OpenAPI/CLI coverage unaffected (same `/audit-log` path, new query params only).
