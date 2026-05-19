# Design: Repo dedup, branch dropdowns, findings category/severity filters

**Date:** 2026-05-19
**Status:** Approved

## Summary

Three independent UX improvements to thewolf:

1. **Add a repo only once** — adding a repo whose path already exists reuses
   the existing repo instead of creating a duplicate row.
2. **Branch dropdowns on scan triggers** — every scan-trigger surface gets a
   live-fetched branch dropdown instead of a free-text field or hard-coded
   default.
3. **Category + severity checkboxes** — findings views let the user uncheck
   severities and categories to hone in on the output they care about.

The three features are independent and can be built/reviewed in any order.

---

## Feature 1 — Add a repo only once

### Problem

`POST /api/repos` always inserts a new row. Adding the same local path or git
URL twice produces duplicate repos, which then show up separately in
collections, scan history, and the repo list.

### Design

**Backend** (`internal/api/routes/repos.go`, `CreateRepo`):

- Before inserting, call `ListReposByUser` for the current user.
- Normalize `source_path` for comparison: trim a single trailing `/` so
  `/repos/ab/pioneer` and `/repos/ab/pioneer/` are treated as the same repo.
- If an existing repo matches the normalized `source_path`, return that repo
  with HTTP `200 OK` and an added response field `deduplicated: true`.
  Do not create a new row.
- If no match, behave exactly as today (create + `201`).

Match key is `source_path` only, scoped to the requesting user. No database
migration — the check is an in-memory scan of the user's repo list.

**Frontend** (`AddRepoPanel` in `_authed.collections.$collectionId.tsx`):

- The existing `createAndLink` mutation does "create repo → link repo to
  collection". Because dedup returns the existing repo with its real `id`,
  the link step works unchanged.
- On success, if the response has `deduplicated: true`, the toast reads
  "Repo already existed — linked the existing one" instead of
  "Repo created and added".

### Out of scope

- Deduplicating remote git URLs by normalized form (e.g. `.git` suffix,
  `http` vs `https`). Only exact normalized `source_path` match is handled.
- Merging scan history of pre-existing duplicate repos.

---

## Feature 2 — Branch dropdowns on scan triggers

### Problem

Scan triggers don't let the user pick a branch:

- The **new-scan form** (`_authed.scans.index.tsx`) has a free-text `branch`
  input — easy to typo, no discovery.
- The **repo detail "Scan now"** button (`_authed.repos.$repoId.tsx`)
  hard-codes `branch: default_branch`.

### Design

**New component** `BranchSelect` (`ui-next/src/components/branch-select.tsx`):

- Props: `repoId: string`, `value: string`, `onChange: (branch: string) => void`,
  `defaultBranch?: string`.
- Fetches `GET /api/repos/{repoId}/branches` (already exists —
  `ListRepoBranches`, returns `{ branches, default_branch, current_branch }`).
- Renders a `<select>` of branches; the default branch is annotated
  "(default)".
- While the fetch is in flight, renders a select containing only
  `defaultBranch` so the surrounding form is never blocked.
- On fetch error, falls back to the same single-option select — scanning the
  default branch always remains possible.

**Wiring:**

1. `NewScanForm` (`_authed.scans.index.tsx`): replace the free-text `branch`
   input with `<BranchSelect>`. The form already tracks `branch` state and
   resets it when the repo changes — `BranchSelect` plugs into that.
2. Repo detail page (`_authed.repos.$repoId.tsx`): add a `<BranchSelect>`
   next to the "Scan now" button. `startScan` sends the selected branch
   instead of the hard-coded `default_branch`.

Backend needs no changes — `CreateScan` already accepts an optional `branch`
and falls back to `repo.DefaultBranch` when empty.

### Out of scope

- Caching the branch list. Dropdowns are always live-fetched.
- A branch picker in the Add-repo flow — it already has one (`/api/git-info`).

---

## Feature 3 — Category + severity checkboxes

### Problem

Users want to hide lint noise and focus on specific severities. Today:

- The **scan-detail page** (`_authed.scans.$scanId.index.tsx`) has a
  single-select severity dropdown — you can pick one severity or "all", but
  can't say "high + critical only".
- The **findings page** (`_authed.findings.index.tsx`) already does
  multi-select severity but has no category filter.

### Design

Findings carry a `Category` enum (`internal/models/types.go`): `sast`, `sca`,
`secrets`, `quality`, `container`, `docs`, `license`, `sbom`, `infra`, `dast`.
"Linting noise" is the `quality` and `docs` categories — no special-casing,
they are ordinary categories the user can uncheck.

**Scan-detail page** (`_authed.scans.$scanId.index.tsx`):

- Replace the single-select `filterSeverity` with a **severity checkbox row**
  (5 severities, all checked by default).
- Add a **category checkbox row** (all categories present in the result set,
  all checked by default).
- Visibility filter becomes
  `selectedSeverities.has(f.severity) && selectedCategories.has(f.category)`,
  combined with the existing tool filter and search.

**Findings page** (`_authed.findings.index.tsx` / `FindingsToolbar`):

- Severity multi-select already exists — keep it.
- Add the same **category checkbox row** to the toolbar.

**Shared behavior:**

- Only categories actually present in the current findings are rendered — no
  empty checkboxes.
- Filter state is ephemeral component `useState`, initialized to "all
  checked" on each page visit. The existing zustand saved-views persistence
  (`useFindingsView`) is left untouched; the new category filter is local
  state and does not persist.

### Out of scope

- Persisting filter selections across sessions.
- A category filter on any other page (dashboard, repo detail).

---

## Testing

- **Feature 1:** Backend test for `CreateRepo` dedup — posting the same
  `source_path` twice returns the existing repo with `deduplicated: true` and
  creates no second row. Also covers trailing-slash normalization.
- **Feature 2:** `/api/repos/{id}/branches` is already tested. Frontend:
  typecheck + manual verification that the dropdown populates and the
  selected branch reaches `CreateScan`.
- **Feature 3:** Pure frontend filter logic; typecheck + manual verification
  that unchecking `quality`/`docs` drops lint findings and severity
  checkboxes combine correctly.

## Rollout

Each feature ships as part of the normal build → `docker compose build wolf`
→ `docker compose up -d wolf` cycle. No migrations, no config changes.
