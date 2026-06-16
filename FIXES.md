# FIXES — thewolf self-scan, 2026-05-14

Source: `FINDINGS/thewolf-2026-05-14T18-14-51-903Z.json` (2154 findings across 14 scanners, scan `43f3e33b`)

## TL;DR

The 2154 raw findings collapse to **~12 real bugs + ~30 hardening tweaks + ~29 dep bumps + 4 new config files**. The rest is noise from machine-generated docs, intentional scanner-orchestration behavior (we exec docker, read user paths — that's the job), and CVE multi-counting across overlapping SCA tools.

A first pass of ~1 day of work (one toolchain bump, two `go get` commands, four lint configs, ~30 inline `#nosec` comments, a few perm-bit changes) takes the count to **~30 real items** that warrant attention.

## Headline numbers

| Cluster | Raw | After fixes | Reduction path |
|---|---:|---:|---|
| **SCA** (govulncheck/grype/osv/syft/trivy) | 1306 | ~0 | Go toolchain bump + `go-git` bump + indirect bumps + filter syft-info |
| **Style** (markdownlint/yamllint/sqlfluff) | 621 | ~30 | 4 new config files + 1 `sqlfluff fix` run |
| **Code+infra** (gosec/semgrep/staticcheck/kics/checkov/shellcheck/renovate) | 227 | ~12 real bugs + 29 dep bumps | Inline suppressions + targeted hardening |
| **Total** | **2154** | **~30 real + 12 bugs + 29 deps** | |

By severity: 1 critical, 1223 high, 169 medium, 664 low, 97 info. The critical and ~1200 of the highs are SCA findings driven by 4 upstream issues.

## Recommended commit sequence

Each row is one commit. PRs grouped by risk.

| # | Commit | Risk | Findings cleared |
|---|---|---|---|
| 1 | `chore(deps): bump Go toolchain 1.25.0 → latest 1.25.x` | low | ~160 stdlib CVEs (incl. only critical) |
| 2 | `chore(deps): bump go-git v5.17.0 → latest v5.x` | low | ~1016 |
| 3 | `chore(deps): bump x/net + circl indirect` | low | ~36 |
| 4 | `feat(scanners): drop syft severity=info from SCA stream` | low | 94 |
| 5 | `chore(lint): add markdownlint/yamllint/sqlfluff configs` | none | ~590 |
| 6 | `chore(lint): sqlfluff fix migrations` | low | residual LT01 |
| 7 | `fix(staticcheck): remove dead code, unused writes` | low | 14 real |
| 8 | `chore(security): tighten perm bits (G301/G306) + suppress G304/G204 by-design` | low | ~70 hardening + ~150 suppressions |
| 9 | `fix(api): path-containment check in browse.go` | medium | 1 real |
| 10 | `chore(deps): renovate patch+minor auto-merge config` | low | 10 deps |
| 11 | major dep bumps (one ecosystem per PR — tailwindcss 4, vite 8, ts 6, charmbracelet v2…) | high | 19 deps over time |

After commits 1–10, residual is ~30 real findings + the major dep bumps queue.

---

## Cluster A — SCA (1306 → ~0)

**1306 findings collapse to ~5 distinct upstream issues.** Govulncheck reports per-callsite, and grype/osv/trivy double-up on the same CVEs.

| Group | Count | Distinct CVEs | Tier |
|---|---:|---:|---|
| `go-git/go-git v5.17.0` (idx parser, smart-HTTP, billy symlinks) | 1016 | 6 | direct |
| Go stdlib (`go 1.25.0` in go.mod) | 160 | ~30 | toolchain |
| `cloudflare/circl v1.6.1` (secp384r1) | 30 | 2 | indirect |
| `golang.org/x/net` (HTTP/2 SETTINGS loop) | 6 | 1 | indirect |
| syft SBOM `info` (not vulns) | 94 | n/a | noise |

### Actions

```bash
# 1. Toolchain — clears ~160 stdlib findings (incl. CVE-2025-68121, the only critical)
go mod edit -go=1.25.4   # or latest 1.25.x with the GO-2025/2026 patches

# 2. Direct dep — clears ~1016 findings
go get github.com/go-git/go-git/v5@latest

# 3. Indirect bumps — often auto-resolved by step 2
go get golang.org/x/net@latest
go get github.com/cloudflare/circl@latest
go mod tidy
go build ./... && go test ./...
```

Then re-run scanners. Expected residual: ~94 syft-info entries — those are inventory artifacts that leaked into the findings stream. **Filter at the aggregator level** (drop `tool=syft AND severity=info` before writing findings rows; the SBOM file itself is the right place for that data).

Confidence: high on toolchain + go-git + x/net (standard version bumps, API-stable within minor). Medium on circl (may be auto-resolved by step 2; verify post-bump).

---

## Cluster B — Style (621 → ~30)

**No lint configs exist in the repo today** (`.markdownlint*`, `.yamllint`, `.sqlfluff`, `pyproject.toml` all absent). Adding 4 small config files removes 95% of the noise.

### markdownlint (532) — 80% in 2 planning docs

| File | Count |
|---|---:|
| `PLAN.md` | 253 |
| `docs/PLAN-findings-pipeline.md` | 169 |
| `README.md` | 84 |
| others | 26 |

Top rule: **MD013** line-length, 416 findings. Planning docs are long-form; wrapping them hurts diffs.

### yamllint (35) — concentrated in 2 files

| File | Count | Notes |
|---|---:|---|
| `.github/workflows/scanners-image.yml` | 21 | mostly `${{ }}` triggering braces/commas FPs |
| `configs/wolf.yaml` | 10 | real `colons` spacing — worth fixing |

### sqlfluff (54) — entirely in migrations, wrong dialect

All 54 findings are in `internal/db/migrations/*.sql`. Top rule **LT01** (whitespace, 41) is auto-fixable. The single **PRS** (parse error) is the smoking gun: sqlfluff is running on `ansi` dialect against SQLite SQL.

### Configs to add

**`.markdownlint.json`** (repo root):

```json
{
  "default": true,
  "MD013": false,
  "MD033": false,
  "MD024": { "siblings_only": true },
  "MD041": false
}
```

**`.markdownlintignore`** (repo root):

```
PLAN.md
docs/PLAN-*.md
CHANGELOG.md
FINDINGS/
scanners/LICENSES.md
```

**`.yamllint`** (repo root):

```yaml
extends: default
rules:
  line-length: { max: 160, level: warning }
  braces: disable
  commas: disable
  document-start: disable
  truthy: { check-keys: false }
ignore: |
  ui-next/node_modules/
  FINDINGS/
```

**`.sqlfluff`** (repo root):

```ini
[sqlfluff]
dialect = sqlite     # verify against internal/db driver — likely sqlite
exclude_rules = LT01 # after running `sqlfluff fix` once
max_line_length = 160
```

Then one-time: `sqlfluff fix internal/db/migrations/` to clear residual LT01.

Residual ~30 findings: ~10 real colons in `configs/wolf.yaml`, ~12 missing fence-language in real docs, ~8 keyword-as-identifier in SQL (review case-by-case).

---

## Cluster C — Code + Infra (227 → ~12 real bugs)

### Real bugs to fix (12)

| Where | Finding | Fix |
|---|---|---|
| `internal/auth/auth.go:62` | G115 `uint32(len(expectedHash))` may overflow | Guard `if len(expectedHash) > math.MaxUint32` — argon2 hashes are 32B so it's theoretical, but cheap to harden |
| `internal/api/routes/browse.go:35` | semgrep filepath.Clean misuse — `Clean` alone doesn't prevent `..` traversal | Add allow-list root + `strings.HasPrefix(filepath.Clean(dir), allowedRoot)` containment check (or use `os.Root`, Go 1.24+) |
| `internal/api/routes/auth.go:154` | Logout cookie missing Secure/SameSite | Add `Secure: true, SameSite: http.SameSiteLaxMode` even on expiry-set |
| staticcheck U1000 (7) | Dead code in `internal/api/routes/fixes.go:243`, `scans.go:1844`, `stubs.go:5`, 4× in `internal/loop/controller/controller_test.go` | Delete |
| staticcheck SA4006 (3) | Unused writes in `scans.go:1171,1174,1872` | Remove dead assignment |
| staticcheck SA4017 (2) | Return value ignored in `internal/scan/enricher/packages.go:95,188` | Use return value or `_ = …` |
| staticcheck SA1019 (1) | Deprecated API in `internal/scan/report/markdown.go:119` | Switch to non-deprecated |
| staticcheck S1016 (1) | `mapper.go:258` — use type conversion | Convert |

### Hardening (~70 — perm bits, error-handling, pinning)

- **G301 → 0750** (14 dirs): `cmd/wolf/main.go:101,311,315`, `internal/api/routes/scans.go:200,206`, `internal/artifacts/artifacts.go:42,67`, `internal/fix/git/git.go:33`, etc. Server-side artifacts don't need world-read.
- **G306 → 0600** (8 files): `internal/scan/report/artifacts.go:70,82,90,111,117`, `internal/scan/report/manifest.go:137`, `internal/scan/runner/runner.go:407`, `internal/api/routes/scans.go:512`.
- **G104 unchecked errors** (26): mostly `w.Write(data)` in HTTP handlers and deferred `f.Close()`. Pattern: `if _, err := w.Write(data); err != nil { log.Debug()... }`; for closes, `defer func(){ _ = f.Close() }()` with `// #nosec G307`.
- **GitHub Actions SHA pinning** (5): `.github/workflows/*.yml` — use Renovate `pinDigests` preset.
- **docker-compose hardening**: bind ports to `127.0.0.1`, add `security_opt: ["no-new-privileges:true"]`, replace literal password env with a secret ref. The docker-socket mount is intentional (wolf orchestrates docker scanners) — keep it but document.

### Suppress by design (~150)

| Category | Count | Mechanism |
|---|---:|---|
| **gosec G304** (file-path-from-arg) — we read user-supplied repo paths and artifact JSON | 19 | `// #nosec G304 -- path comes from validated repo arg / artifact dir under scanRoot` per call site |
| **gosec G204** (exec.Command with variable) — we exec docker/claude/codex | 18 | `// #nosec G204 -- command is configured tool name; args sourced from internal config` |
| **semgrep dangerous-exec-command** (10) — duplicates G204 | 10 | `// nosemgrep: go.lang.security.audit.dangerous-exec-command` |
| **semgrep xss/no-direct-write-to-responsewriter** (7+3) — JSON/CSV downloads, not HTML | 10 | `// nosemgrep` |
| **KICS apt-pin** (23) — `scanners/Dockerfile` intentionally floats versions | 23 | `disable-queries: 965a08d7-…` in `.kics.yaml`, or `# kics-scan ignore-line` per block |
| **KICS apk-pin** (2) — same rationale for main Dockerfile | 2 | same |
| **Checkov healthcheck-missing** (5) — bucket images are one-shot containers, no service to healthcheck | 5 | `# checkov:skip=CKV_DOCKER_2` per file |
| **shellcheck SC2317** (26 in `scanners/smoke-test.sh`) — `check()` called indirectly via `run()` | 26 | one line at top: `# shellcheck disable=SC2317` |

### Renovate (29 dep bumps)

- **Patch** (1, safe): `go-sqlite3 v1.14.34 → v1.14.44`
- **Minor** (9, low-risk): `lib/pq 1.11→1.12`, `zerolog 1.34→1.35`, `x/crypto`, `x/term`, `golang 1.24→1.26 alpine`, `alpine 3.20→3.23`, `@tanstack/react-form` minor, `lucide-react` minor, plus go-git (covered above).
- **Major** (19, review each): `tailwindcss 3→4`, `vite 6→8` + `@vitejs/plugin-react 4→6`, `typescript 5→6`, `bubbletea/lipgloss v1→v2` (charmbracelet redesign), `recharts 2→3`, `tailwind-merge 2→3`, `@tanstack/react-form 0→1`, `lucide-react 0→1`, `sonner 1→2`, base images `node 20→26`, `debian 12→13`, `postgres 16→18`.

Set up a Renovate config (or batched PR) that auto-merges patch+minor and holds majors for manual review.

---

## Anti-goals

Things explicitly *not* to do:

- **Don't try to fix MD013 by reflowing PLAN.md.** It's 62 KB of long-form planning prose; line-wrapping hurts diffs and we ignore the file via `.markdownlintignore` instead.
- **Don't add `#nosec G304` to everything.** The two HTTP-reachable callers (`browse.go`, `scans.go:345`) need a real containment check, not a comment.
- **Don't pin every apt package in `scanners/Dockerfile`.** Wolf builds the scanner image fresh; floating versions is the design.
- **Don't suppress all of `w.Write` G104.** The handlers SHOULD log a Debug on write failure — at minimum because of HTTP/2 stream resets in the SSE path. Worth ~30 min of mechanical edits.
- **Don't bump tailwindcss 3→4 in the same PR as the SCA cleanup.** Major UI dep bumps deserve their own PRs with manual smoke-testing.

---

## Suggested first PR

**~1 day of work, clears ~180 of 227 code+infra findings + the entire SCA cluster:**

1. `chore(deps)`: Go toolchain + go-git + x/net + circl + `go mod tidy`
2. `feat(scanners)`: drop `syft severity=info` from SCA stream (one-line filter in the aggregator)
3. `chore(lint)`: add `.markdownlint.json`, `.markdownlintignore`, `.yamllint`, `.sqlfluff` + run `sqlfluff fix` on migrations
4. `fix(staticcheck)`: delete dead code, fix unused writes, swap deprecated API
5. `chore(security)`: 0755→0750, 0644→0600, suppress G304/G204/SC2317/KICS apt-pin/Checkov healthcheck
6. `chore(deps)`: Renovate config — auto-merge patch+minor, manual-review major

After that: separate PRs for `browse.go` path containment, docker-compose hardening, and individual major dep bumps.

---

## Source reports

Detailed per-cluster reports retained at `/tmp/wolf-findings/{sca,style,code}-report.md` for the duration of this session. The triage was generated from the JSON above; re-run a fresh scan after committing fixes to confirm the residual.
