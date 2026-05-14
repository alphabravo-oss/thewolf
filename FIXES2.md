# FIXES2 — thewolf self-scan, 2026-05-14 (post-FIXES round)

Source: `FINDINGS/thewolf-2026-05-14T18-50-53-929Z.json` (141 findings, post-`FIXES.md`)

## TL;DR

The 141 remaining findings break into **3 wolf-side bugs to actually fix** + **2 lint/scanner config gaps** + **noise**. Real PR work is roughly half a day. The big-ticket item is a **plugin-side fix to KICS** so it honors `.kics.yaml` — that alone clears 27 of the 42 KICS findings.

| Cluster | Count | After fix | Action |
|---|---:|---:|---|
| KICS infra alerts | 42 | ~15 | Pass `--exclude-queries` flag in `plugins/infra/kics.go` (config file isn't auto-read) |
| Renovate dep bumps | 27 | 0–1 PR each | Separate PRs per ecosystem; auto-merge patch+minor |
| osv-scanner CVEs | 23 | 0 | Likely all in stdlib of the `/thewolf` binary; rebuild after toolchain bump should clear |
| grype CVEs | 21 | 0 | Same as osv-scanner — overlap |
| semgrep false-positives | 10 | 0 | Re-apply inline `// nosemgrep` at current line numbers (previous batch shifted) |
| yamllint `colons` | 8 | 0 | Real style fixes in `configs/wolf.yaml` |
| markdownlint MD040 | 6 | 0 | Add fence language tags in `README.md` |
| markdownlint MD032 | 1 | 0 | Fix blank-line around list in `docs/RELEASE_NOTES_2_0.md` |
| trufflehog | 2 | 0 | False positive: UUIDs in `.kics.yaml` look like DockerHub creds. Add to trufflehog exclude. |
| sqlfluff RF04 | 1 | 0 | Keyword-as-identifier in `007_ai_prompts_and_settings.sql` — rename column |

## Real wolf-code bugs to fix

### 1. KICS plugin doesn't pass `--exclude-queries` (P1)

**File**: `plugins/infra/kics.go:44-46`
**Current**:
```go
script := "kics scan -p /scan -o /tmp/kics --no-progress " +
    "--report-formats json --silent >/dev/null 2>&1; " +
    "cat /tmp/kics/results.json"
```

**Problem**: KICS does *not* auto-discover `.kics.yaml` in the scan root. The repo's `.kics.yaml` we added in the previous round (excluding apt-pin + apk-pin queries) has had no effect. 27 of the 42 KICS findings would be filtered out if those exclusions actually applied.

**Fix**: read `.kics.yaml` from the scan root and translate the `exclude-queries` list into a CLI flag, or pass `--config <path>` directly. The CLI flag is simpler:

```go
exclude := kicsLoadExcludeQueries(opts.RepoPath) // reads .kics.yaml if present
if exclude != "" {
    script = "kics scan --exclude-queries " + exclude + " -p /scan ..."
}
```

Test: write a `.kics.yaml` with one exclusion + assert that rule doesn't appear in the result.

### 2. Semgrep `// nosemgrep` comments misaligned (P1)

After the staticcheck-cleanup commit (`32e5943`) removed dead code from `internal/api/routes/scans.go` and `fixes.go`, the line numbers downstream shifted by 1–4. The batch suppression script that landed in commit `6c0d200` used line numbers from the *original* scan, so the inline `// nosemgrep` comments are now on the wrong lines.

**Files affected** (current scan, ground truth):
- `internal/api/routes/findings.go:337` — `xss/no-direct-write-to-responsewriter`
- `internal/api/routes/findings.go:440` — same
- `internal/api/routes/scans.go:1014, 1054, 1783, 1961, 1974` — same
- `internal/api/routes/fixes.go:233` — `xss/no-fprintf-to-responsewriter`
- `internal/scan/mapper/mapper.go:357` — `dangerous-exec-command`
- `internal/api/routes/auth.go:154` — `cookie-missing-secure`

**Fix**: re-run the `/tmp/apply-suppress.py` script with the new findings file. Then check that none of the comments land below a closing `}` (the previous bug). Alternatively, do an idempotent pass: search for the offending statement substring and ensure a `// nosemgrep` directive sits on the previous line.

The `cookie-missing-secure` at `auth.go:154` is the *logout* handler explicitly expiring a legacy cookie — add `Secure: true, SameSite: http.SameSiteLaxMode` to the expiring Cookie struct (low-cost hardening), then mark it `// nosemgrep`.

### 3. yamllint `colons` in `configs/wolf.yaml` (P2)

**File**: `configs/wolf.yaml:38` and 7 others
**Problem**: Style issues — too many spaces after `:`. Real fix, not noise.

```bash
docker run --rm -v "$PWD:/scan" wolf-scanners:dev sh -c 'cd /scan && yamllint configs/wolf.yaml'
```

Inspect each hit, drop the extra space. ~10 minutes of manual edits.

## Lint/scanner config gaps

### 4. trufflehog flags UUIDs in `.kics.yaml` as DockerHub creds

**Why**: trufflehog's DockerHub detector looks for hex-like strings of certain lengths. The KICS query UUIDs (`965a08d7-ef86-4f14-8792-4a3b2098937e`) match.

**Fix**: add `.kics.yaml` to the trufflehog exclude. Easiest is to extend `scanners/trufflehog-excludes.txt` (already exists):

```
.kics.yaml
```

Plus add `# trufflehog:ignore` comments next to the UUIDs.

### 5. sqlfluff RF04 — keyword as identifier

**File**: `internal/db/migrations/007_ai_prompts_and_settings.sql:2`
**Problem**: A column named `key` or similar — SQL keyword. sqlfluff's RF04 (`reserved keywords as identifiers`) flags it.

**Decision**: either rename the column (DB migration cost) or suppress via `# sqlfluff:dialect:postgres` comment. Look at the schema first; rename if the column name is genuinely `key` and the migration is recent enough.

## Dep bumps (renovate, 27)

### Patch (1, safe to merge):
- `gomod: github.com/mattn/go-sqlite3 v1.14.34 → v1.14.44`

### Minor (7, safe-ish — single-PR batch):
- `gomod: x/crypto v0.50.0 → v0.51.0`
- `gomod: zerolog v1.34.0 → v1.35.1`
- `gomod: lib/pq v1.11.2 → v1.12.3`
- `dockerfile: golang 1.24-alpine → 1.26-alpine`
- `dockerfile: alpine 3.20 → 3.23`
- `npm: lucide-react ^0.469.0 → ^0.577.0`
- `npm: @tanstack/react-form ^0.40.0 → ^0.48.0`

### Major (19, individual PRs — review each):

| Package | From → To | Risk | Notes |
|---|---|---|---|
| `dockerfile: debian` | 12-slim → 13-slim | M | 5 Dockerfiles (default + 3 buckets + main). New apt indices, possible package-name churn. |
| `npm: tailwindcss` | 3→4 | L | Config format changed; needs migration of `tailwind.config.ts` |
| `npm: vite` | 6→8 | L | Plugin API surface shifts; check `@vitejs/plugin-react` (also major) compat |
| `npm: @vitejs/plugin-react` | 4→6 | L | Bundle with vite upgrade |
| `npm: typescript` | 5→6 | L | Strictness changes; expect type-fix churn |
| `npm: recharts` | 2→3 | M | API redesign |
| `npm: tailwind-merge` | 2→3 | M | Tied to tailwind v4 upgrade |
| `npm: @tanstack/react-form` | 0→1 | M | First stable; minor breaking renames |
| `npm: lucide-react` | 0→1 | M | First stable; mostly icon-name churn |
| `npm: sonner` | 1→2 | S | Toast lib; small surface |
| `npm: @types/node` | 22→25 | S | Node 25 types; check Node baseline in CI |
| `gomod: bubbletea` | 1→2 | L | Charmbracelet v2 redesign |
| `gomod: lipgloss` | 1→2 | L | Same — bundle with bubbletea |
| `dockerfile: node` | 20→26 | M | Big jump; check eslint/ts toolchain compat |
| `docker-compose: postgres` | 16→18 | M | Major Postgres; check pg_dump/restore in any dev DB |

**Suggested order**: tailwindcss + tailwind-merge in one PR; vite + plugin-react in another; bubbletea+lipgloss as one; everything else solo.

## osv-scanner + grype residual CVEs

23 osv + 21 grype findings, mostly the same CVEs reported by both tools:

- 1 critical: **CVE-2025-68121** (stdlib crypto/tls session-resumption) — should be fixed by `go 1.25.4` directive
- Several **GO-2025-***, **GO-2026-*** in stdlib

**Hypothesis**: grype scans the *built binary* `/thewolf` and reads its embedded BuildInfo. If the binary was built with the local 1.26.2 toolchain *before* the `go.mod` `go 1.25.4` directive landed, it might still embed an older stdlib reference. Action: rebuild from clean (`go clean -cache && go build`) and re-scan.

If they persist after a clean rebuild, the local Go toolchain is missing the patches — install latest `go 1.26.x` from upstream.

## Action plan

**Single PR (~half day, clears ~70%)**:
1. Patch KICS plugin to honor `.kics.yaml` exclusions → 27 findings cleared
2. Re-run inline `// nosemgrep` script at current line numbers → 9 findings cleared
3. Fix yamllint `colons` in `configs/wolf.yaml` → 8 findings cleared
4. Add `.kics.yaml` to trufflehog excludes → 2 findings cleared
5. Add fence languages to top 6 README.md code blocks → 6 findings cleared
6. Rename or suppress the `key` column in migration 007 → 1 finding cleared
7. Clean rebuild + re-scan to flush stale stdlib CVEs from grype/osv → up to 44 findings cleared

**Follow-on**:
- Renovate patch + minor bundle → 8 deps in one PR
- Major bumps: 1 PR per logical group (tailwind, vite, charmbracelet, debian-base, postgres)

After step 7, expected residual: roughly **5–15 findings** — all real items that warrant per-case review.
