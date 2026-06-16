# Plan — Deterministic Findings Pipeline

**Status:** draft — pending review
**Author:** drafted with Claude, 2026-05-14
**Owner:** mj
**Scope:** thewolf scan pipeline — from "raw scanner output" to "deterministic, deduplicated, fix-ready documents", with zero AI in the loop. AI integration deferred to a later phase that consumes these artifacts.

---

## 1. Context & Goals

### Vision recap

The Wolf scans a local repo path with N containerized scanners and produces a usable set of findings that:

1. **Preserves the raw output** of every scanner verbatim (audit trail, reproducibility, debug).
2. **Selects scanners** based on detected languages/stacks in the repo — instead of always running all 44.
3. **Normalizes** every finding into one canonical record.
4. **Deduplicates** findings reported by multiple tools at the same location.
5. **Categorizes** each finding into a fine-grained category (e.g. `sql-injection`, not just `sast`) via a deterministic per-rule lookup table.
6. **Filters** false-positive-prone findings using path heuristics (test files, vendored code, generated code) and an optional `.wolfignore`.
7. **Emits two human-readable documents**: a `FIX-HIGH.md` (critical/high severity, high confidence) and a `FIX-ALL.md` (everything that passes path suppression), each grouped by category and rendered with a fix-strategy template — so an engineer or downstream AI agent can act on them directly.
8. **Remains AI-free** at every stage. The knowledge required (rule → category, rule → fix strategy) is curated YAML/Go data, not inference.

### Non-goals (deferred)

- No AI fix generation in this phase. The `FIX-*.md` documents are designed to be consumable by AI later but are valuable standalone.
- No automated patching, no PR creation, no scan-fix-rescan loop.
- No additional scanners. We have 44 and that is enough.
- No UI work. The pipeline produces artifacts on disk; UI consumption is a separate effort.

### Success criteria (overall plan)

- `wolf scan --repo <path>` produces a single directory containing: raw per-tool output, a canonical `findings.json`, a `FIX-HIGH.md`, a `FIX-ALL.md`, and a `manifest.json`.
- Scans on language-mismatched repos (e.g. Python scanners on a Go repo) no longer run by default.
- Re-running a scan against an unchanged repo produces byte-identical `findings.json` (modulo timestamps).
- A repo with deliberate FP-prone fixtures (test files containing fake secrets, vendored dependencies) produces a `FIX-HIGH.md` that does not surface those.

---

## 2. Current State (from investigation, 2026-05-14)

| Capability | State | Location |
|---|---|---|
| Language/framework detection | ✅ built, **CLI does not call it** | `internal/scan/detector/detector.go` |
| Scanner selection by language | ✅ built, dormant | `internal/scan/runner/runner.go:138` (`SelectTools`) |
| Canonical `Finding` struct | ✅ mature (28 fields, normalized severity + category) | `internal/models/finding.go:5-36` |
| Per-tool parsers | ✅ per-plugin, idiomatic | `plugins/*.go` |
| Fingerprint (stable ID) | ✅ working — `SHA256(tool:rule:file)`, repo-relative paths | `internal/scan/runner/runner.go:87-98` |
| Deduplication | ✅ working — keeps higher severity | `internal/scan/runner/runner.go:100-130` |
| Per-tool stdout/stderr capture | ✅ persisted as `<tool>.log` | `~/.wolf/artifacts/<scanID>/` |
| Per-tool parsed findings JSON | ✅ persisted as `<tool>.json` | same dir |
| Markdown/SARIF/JSON report writers | ✅ exist, **not wired to disk** | `internal/scan/report/{markdown,sarif,report}.go` |
| Raw pre-parse tool output (SARIF/JSON blob) | ❌ discarded after parsing | n/a |
| Path-based suppression / `.wolfignore` | ❌ does not exist | n/a |
| Fine-grained rule categories | ❌ only 10 coarse buckets (`sast`, `sca`, ...) | `internal/models/types.go` |
| Rule → fix-strategy mapping | ❌ does not exist (the `AIFixSuggestion` field is populated by AI only) | n/a |
| HIGH vs ALL document split | ❌ one combined markdown only | n/a |

**Implication:** the foundation is strong. The plan is mostly **wiring existing components together** + **adding deterministic knowledge** + **adding suppression**.

---

## 3. Target End State — Artifact Layout

After `wolf scan --repo /path/to/myrepo` completes:

```
~/.wolf/artifacts/<scanID>/
├── manifest.json                  # scan metadata
├── findings.json                  # canonical deduped findings (machine-readable)
├── FIX-HIGH.md                    # high-priority human-readable fix doc
├── FIX-ALL.md                     # all findings that pass path suppression
├── RAW.md                         # all tool stdout/stderr concatenated by section
├── combined.sarif                 # aggregated SARIF 2.1.0 across all tools
├── raw/
│   ├── semgrep.sarif              # original tool output, verbatim
│   ├── gosec.json
│   ├── gitleaks.json
│   ├── trivy.json
│   └── ...
├── logs/
│   ├── semgrep.log                # tool stdout/stderr
│   └── ...
└── per-tool/
    ├── semgrep.findings.json      # parsed findings per tool (already exists)
    └── ...
```

### `manifest.json` schema (example)

```json
{
  "scan_id": "01HXYZ...",
  "repo_path": "/path/to/myrepo",
  "repo_commit": "a1b2c3d",
  "started_at": "2026-05-14T08:30:00Z",
  "finished_at": "2026-05-14T08:34:12Z",
  "wolf_version": "2.0.1",
  "detection": {
    "languages": ["go", "typescript"],
    "frameworks": ["react"],
    "manifests": ["go.mod", "ui-next/package.json"]
  },
  "scanners_run": ["gosec", "semgrep", "gitleaks", "trivy", "eslint", "..."],
  "scanners_skipped": [
    {"tool": "bandit", "reason": "no python files"},
    {"tool": "rubocop", "reason": "no ruby files"}
  ],
  "counts": {
    "raw_findings": 1842,
    "after_dedupe": 1311,
    "after_suppression": 287,
    "high_confidence_high_severity": 41
  }
}
```

### `findings.json` schema

Array of canonical Finding objects (already defined in `internal/models/finding.go`) plus the new fields added in Phase 2.

### `FIX-HIGH.md` structure (example)

See §8.

---

## 4. Phased Build Plan

Work is split into three phases. Each phase is independently shippable.

| Phase | Theme | Effort | Ship value |
|---|---|---|---|
| **Phase 1** | Wire what exists — detection + raw output + reports to disk | ~4–5h | Scans pick correct scanners, raw output preserved, combined reports on disk |
| **Phase 2** | Deterministic categorization + fix-strategy knowledge base + FIX-HIGH/ALL docs | ~12–15h | Engineers get prioritized, deduplicated, actionable fix docs |
| **Phase 3** | Path suppression, `.wolfignore`, FP reduction | ~3–4h | FIX-HIGH stops surfacing obvious test/vendor noise |

---

## Phase 1 — Wire Existing Pieces (~4–5h)

### Why first

None of these require new design. They unlock visible value (correct scanner selection + raw output preservation) without touching the finding model. They also produce the artifact layout that Phase 2 and 3 build on.

### Tasks

#### 1.1 Wire detector into CLI scan path

**File:** `cmd/wolf/main.go` (around line 259)
**Current:** `RunConfig.Languages` is left empty → runner falls back to all 44 scanners.
**Change:** Call `detector.Detect(repoPath)` and populate `cfg.Languages`. Add CLI flag `--all-scanners` (default false) to opt out of language-based selection. Add `--detect-only` flag for debugging.

**Sketch:**

```go
// in scan command
det, err := detector.Detect(repoPath)
if err != nil {
    return fmt.Errorf("language detection failed: %w", err)
}
if !allScanners {
    cfg.Languages = det.Languages
}
log.Printf("detected languages: %v, frameworks: %v", det.Languages, det.Frameworks)
```

**Tests:**

- Add `cmd/wolf/main_test.go` (or integration test in `internal/scan/runner/runner_integration_test.go`): scan a known Go-only fixture under `testdata/`; assert `scanners_run` includes `gosec` and does NOT include `bandit`.
- Existing detector tests already cover detection logic — do not duplicate.

**Pass gates:**

- Manual: `wolf scan --repo ./testdata/go-only` shows `Skipped: bandit (no python files)` in output and `manifest.json`.
- Manual: `wolf scan --repo . --all-scanners` runs all 44 (verifies fallback).

#### 1.2 Persist raw pre-parse tool output

**Files:** `internal/scan/runner/runner.go` + each plugin's runner harness, plus the runner config.
**Current:** Each plugin runs its tool in a container, captures stdout, and parses it in-memory. The raw bytes are discarded after parsing.
**Change:** Before parsing, write the raw bytes to `<scanDir>/raw/<tool>.<ext>` (extension by content-type: `.sarif`, `.json`, `.txt`). Hook into the existing `OnToolOutput`/`OnToolDone` callback flow.

**Approach options:**

- **Option A (preferred):** Add a `RawOutputDir` field to `RunConfig`. Plugins that already buffer their full output (most do, for parsing) write the buffer to disk in the runner after parser returns. No plugin-by-plugin change needed.
- **Option B:** Change each plugin to call `runner.SaveRaw(toolName, bytes)`. More invasive.

Default Option A.

**Sketch (in runner.go where plugin output is collected):**

```go
if cfg.RawOutputDir != "" && len(rawBytes) > 0 {
    ext := detectExt(rawBytes) // sniff: starts with '{' or '<' or 'sarif' marker
    path := filepath.Join(cfg.RawOutputDir, toolName+ext)
    _ = os.WriteFile(path, rawBytes, 0o644)
}
```

**Tests:**

- Unit: feed runner a fake plugin that returns known bytes, assert file written to `RawOutputDir/<name>.json`.
- Integration: scan a fixture, assert `raw/semgrep.sarif` exists and is valid SARIF JSON (parse round-trip).

**Pass gates:**

- After scan, `ls ~/.wolf/artifacts/<scanID>/raw/` shows one file per scanner that ran successfully.
- Each file is well-formed in its native format (jq parses it for JSON-emitting tools).

#### 1.3 Wire existing report writers to disk

**File:** `internal/api/routes/scans.go` (`executeScan`) and the CLI equivalent.
**Current:** `report.GenerateJSON/Markdown/SARIF` functions exist but are not called.
**Change:** After `Deduplicate()`, call all three and write to `<scanDir>/{findings.json, RAW.md, combined.sarif}`. Register each as a `ScanArtifact` row.

> Note: `RAW.md` here is the existing markdown report — we are intentionally co-opting that filename because Phase 2 will introduce `FIX-HIGH.md` / `FIX-ALL.md` as the curated docs. `RAW.md` remains the "everything verbatim, no opinions" doc.

**Tests:**

- Integration: scan a fixture, assert all three files exist on disk and `findings.json` is parseable as `[]models.Finding`.

**Pass gates:**

- `findings.json` round-trips through `json.Unmarshal` into `[]models.Finding`.
- `combined.sarif` validates against SARIF 2.1.0 schema (use existing project validator if present, else `jq`-based sanity check).

#### 1.4 Write `manifest.json`

**File:** `internal/scan/report/manifest.go` (new).
**Current:** No manifest concept on disk.
**Change:** Compose from detection result, scan timings, scanner pass/skip list, finding counts. Write at end of scan.

**Tests:**

- Unit: build manifest from fixture inputs, assert JSON shape matches schema in §3.

**Pass gates:**

- Manifest validates against a JSON schema (write a minimal one in `internal/scan/report/manifest.schema.json` for documentation).
- Counts match: `len(findings.json) == counts.after_dedupe`.

### Phase 1 ship gate

- Scan a multi-language fixture repo. Verify the artifact directory contains all expected files.
- All pre-existing tests still pass.
- `wolf doctor` (if applicable) reports no regressions.

---

## Phase 2 — Deterministic Knowledge Base + Fix Docs (~12–15h)

### Why this phase

Phase 1 ships *information*. Phase 2 ships *actionability*. This is the value users actually feel.

### Design — Fine-grained categories

Add a second category layer alongside the existing coarse `Category`:

```go
// internal/models/finding.go — add field
type Finding struct {
    // ... existing fields ...
    FineCategory   string `json:"fine_category" db:"fine_category"`     // e.g. "sql-injection"
    FixStrategyID  string `json:"fix_strategy_id" db:"fix_strategy_id"` // e.g. "parameterize-query"
    Confidence     string `json:"confidence" db:"confidence"`           // "high" | "medium" | "low"
    CorroboratedBy []string `json:"corroborated_by" db:"-"`            // list of tools that also flagged
}
```

`Confidence` is derived deterministically from cross-tool agreement at dedupe time:

- 3+ distinct tools at same `(file, line, fine_category)` → `high`
- 2 tools → `medium`
- 1 tool → `low`

Note: this requires the dedupe step to *merge* matching findings from multiple tools (currently it discards duplicates entirely). See task 2.3.

### Tasks

#### 2.1 Knowledge base data structure

**Files (new):**

- `internal/finding/knowledge/types.go` — Go types for entries
- `internal/finding/knowledge/data/<tool>.yaml` — one file per scanner with rule mappings
- `internal/finding/knowledge/strategies/<id>.md` — markdown templates per fix strategy
- `internal/finding/knowledge/loader.go` — embed (`//go:embed`) + lookup API

**Entry shape (yaml):**

```yaml
# data/gosec.yaml
tool: gosec
rules:
  G201:
    fine_category: sql-injection
    fix_strategy: parameterize-query
    confidence_floor: medium    # this rule rarely false-positives
    references: [CWE-89, https://owasp.org/...]
  G401:
    fine_category: weak-crypto
    fix_strategy: replace-weak-hash
  G404:
    fine_category: insecure-randomness
    fix_strategy: use-crypto-rand
```

**Strategy template shape (markdown):**

```markdown
---
id: parameterize-query
title: Use parameterized queries
applies_to: [sql-injection]
---

Replace string-concatenated SQL with placeholders bound at execution time.

**Before:**
\`\`\`go
db.Query("SELECT * FROM users WHERE id = " + userID)
\`\`\`

**After:**
\`\`\`go
db.Query("SELECT * FROM users WHERE id = ?", userID)
\`\`\`

**References:** CWE-89, OWASP A03:2021
```

**API:**

```go
package knowledge

func Lookup(tool, ruleID string) (Entry, bool)
func Strategy(id string) (Template, bool)
func AllStrategies() []Template
```

**Tests:**

- Unit: load all embedded YAML files at init; fail test if any reference a fix strategy that doesn't exist.
- Unit: each `Lookup` returns deterministic results.

**Pass gates:**

- Validator command `wolf knowledge validate` reports zero dangling references.

#### 2.2 Seed the knowledge base

This is the curation grind. I (Claude) will produce the entries; mj reviews.

**Initial coverage targets (top emitters first):**

| Tool | Approx rules covered initially | Source |
|---|---|---|
| semgrep | 50 most-emitted rules | semgrep registry, focus on `security` + `correctness` rulesets |
| gosec | all ~30 G-series rules | gosec docs |
| gitleaks | secret-type → "rotate + remove from history" strategies (~10 entries) | gitleaks default ruleset |
| trivy | top 20 CVE categories + IaC misconfigs | trivy docs |
| KICS | top 20 IaC misconfig categories | KICS docs |
| eslint | top 20 security plugin rules | eslint-plugin-security |
| bandit | all B-series rules | bandit docs |

**Fix strategies — initial set (~25 templates):**
`parameterize-query`, `escape-html-output`, `replace-weak-hash`, `use-crypto-rand`, `rotate-and-remove-secret`, `pin-dependency-version`, `move-secret-to-env`, `use-arg-list-no-shell`, `validate-redirect-target`, `set-secure-cookie-flags`, `use-prepared-statements`, `disable-debug-in-prod`, `use-https`, `add-content-security-policy`, `restrict-cors-origins`, `escape-shell-args`, `use-safer-yaml-load`, `verify-jwt-signature`, `set-resource-limits`, `non-root-container-user`, `pin-base-image-digest`, `update-vulnerable-dependency`, `remove-hardcoded-credential`, `validate-input`, `use-context-with-timeout`.

**Out-of-scope rules:** style/formatting findings (`gofmt`, `markdownlint` cosmetic rules, etc.) — these can stay in `FIX-ALL.md` with a generic "style" strategy that just says "see tool docs".

**Tests:**

- Per-tool: write a small parsed-output fixture and assert every rule in the fixture has a knowledge entry. (Coverage measurement.)

**Pass gates:**

- Coverage report: `wolf knowledge coverage` prints "X of Y observed rule IDs covered" per tool. We target ≥80% on the top-5 tools before merging.

#### 2.3 Update dedupe to merge cross-tool matches

**File:** `internal/scan/runner/runner.go:100-130`
**Current:** Dedupe key is `file:line:rule_or_title`. Duplicates are discarded.
**Change:**

- New dedupe key: `file:line:fine_category` (computed via knowledge lookup; falls back to old key when no fine category known).
- On match: keep highest-severity record but append the other tool's name to `CorroboratedBy`.
- Compute `Confidence` from `len(CorroboratedBy)+1`.

**Sketch:**

```go
type bucket struct {
    primary  models.Finding
    tools    []string
}

func DedupeMerge(findings []models.Finding) []models.Finding {
    buckets := map[string]*bucket{}
    for _, f := range findings {
        cat := f.FineCategory
        if cat == "" { cat = f.RuleID }
        key := fmt.Sprintf("%s:%d:%s", f.FilePath, f.LineStart, cat)
        b, ok := buckets[key]
        if !ok {
            buckets[key] = &bucket{primary: f, tools: []string{f.ToolName}}
            continue
        }
        b.tools = append(b.tools, f.ToolName)
        if severityRank(f.Severity) > severityRank(b.primary.Severity) {
            b.primary = f
            b.tools = uniq(append(b.tools, b.primary.ToolName))
        }
    }
    out := make([]models.Finding, 0, len(buckets))
    for _, b := range buckets {
        b.primary.CorroboratedBy = b.tools
        b.primary.Confidence = confidenceFromCount(len(b.tools))
        out = append(out, b.primary)
    }
    return out
}
```

**Tests:**

- Unit: feed in 3 findings at same location from gosec/semgrep/snyk, assert one record returned with `confidence=high`, `corroborated_by=[gosec,semgrep,snyk]`.
- Unit: feed in 1 finding, assert `confidence=low`.
- Unit: existing tests for `Deduplicate` continue to pass for backwards compat (consider keeping `Deduplicate` as a thin wrapper).

**Pass gates:**

- A multi-scanner scan on a known SQL-injection fixture produces 1 finding (not 3), with `corroborated_by` populated.

#### 2.4 Categorize findings at parse time

**Files:** `internal/scan/runner/runner.go` post-parse hook, or per-plugin.
**Change:** After each plugin returns its findings, enrich each with `FineCategory` + `FixStrategyID` via `knowledge.Lookup(toolName, ruleID)`. If lookup misses, leave fields empty and log a debug message (so we can grow coverage).

**Tests:**

- Unit: synthetic finding with `tool=gosec, rule=G201` → enriched with `fine_category=sql-injection`.

**Pass gates:**

- On a real scan, `manifest.json` reports `categorization_coverage: X%` (categorized findings / total findings). Target ≥70% in this phase.

#### 2.5 FIX-HIGH.md and FIX-ALL.md renderer

**File:** `internal/scan/report/fix_doc.go` (new).
**Inputs:** deduped/categorized findings, knowledge base.
**Output:** two markdown files.

**Rendering logic:**

1. Filter for HIGH doc: `severity in {critical, high}` AND `confidence in {high, medium}` AND `not suppressed` (Phase 3).
2. Group by `fine_category`.
3. For each category, render: strategy template (one time), then a list of locations.
4. Sort categories by total severity weight, then by count, descending.
5. Findings with no `fine_category` collected at end under "Uncategorized — see tool docs".

**Tests:**

- Golden file: known input findings → known markdown output. Update on intentional renderer changes.
- Snapshot: render `FIX-ALL.md` against a fixture scan, assert sections in expected order.

**Pass gates:**

- A finding present in `findings.json` with `severity=high, confidence=high` appears in `FIX-HIGH.md`.
- A finding with `severity=low` does not appear in `FIX-HIGH.md` but does appear in `FIX-ALL.md`.

### Phase 2 ship gate

- Scan thewolf's own repo. Verify:
  - `FIX-HIGH.md` exists and is non-empty.
  - At least 70% of findings have a `fine_category`.
  - Confidence values appear and are deterministic across two consecutive scans.

---

## Phase 3 — Path Suppression & `.wolfignore` (~3–4h)

### Tasks

#### 3.1 Built-in default suppression rules

**File:** `internal/scan/suppress/defaults.go` (new).
**Defaults:**

- `**/*_test.go`, `**/test_*.py`, `**/*.spec.{js,ts}`, `**/__tests__/**`, `**/testdata/**`, `**/fixtures/**` — suppress *secret* findings and *quality* findings by default, keep security findings.
- `**/vendor/**`, `**/node_modules/**`, `**/third_party/**` — suppress everything by default (the user doesn't own this code).
- `**/*.generated.{go,ts,js}`, files starting with `// Code generated` or `@generated` — suppress everything.

Rules expressed as:

```go
type Rule struct {
    PathGlob     string
    Categories   []string   // empty = all
    Action       string     // "suppress" or "downgrade"
}
```

#### 3.2 `.wolfignore` parser

**File:** `internal/scan/suppress/wolfignore.go` (new).
**Syntax:** gitignore-style with optional rule filter:

```
# Block all findings in generated code
**/*.generated.go

# Block only secret findings in test fixtures
**/testdata/** category=secrets,sast.hardcoded-secret

# Block a specific rule everywhere
* rule=semgrep.foo.bar
```

#### 3.3 Apply suppression in renderer (not in dedupe)

**Why not in dedupe:** Suppressed findings should still appear in `findings.json` (with `suppressed: true`) so users can audit what was hidden. They are excluded from `FIX-HIGH.md` and from the visible portion of `FIX-ALL.md` (moved to a collapsed appendix).

**Add field:**

```go
type Finding struct {
    // ...
    Suppressed       bool   `json:"suppressed"`
    SuppressedReason string `json:"suppressed_reason,omitempty"`
}
```

**Tests:**

- Unit: a finding in `vendor/foo/bar.go` is marked `suppressed=true, reason="default:vendor"`.
- Integration: a repo with a `.wolfignore` containing `**/cmd/legacy/**` produces a `FIX-HIGH.md` with no findings from that path.

**Pass gates:**

- Suppressed findings are present in `findings.json` but absent from `FIX-HIGH.md` body (may appear in `FIX-ALL.md` appendix only).
- `manifest.json.counts.after_suppression` reflects the reduction.

### Phase 3 ship gate

- Scan a fixture repo containing intentional FP-prone files (`testdata/fake_secrets.go`, `vendor/whatever`). Verify zero of those findings reach `FIX-HIGH.md`.

---

## 5. Risks & Open Questions

1. **Knowledge base maintenance burden.** Mitigated by: (a) coverage is incremental, not all-or-nothing; (b) uncategorized findings still appear in `FIX-ALL.md`; (c) `wolf knowledge coverage` makes gaps visible.
2. **Severity normalization across tools is fuzzy.** E.g. gosec "HIGH" ≠ semgrep "HIGH". For Phase 2 we accept the existing per-plugin severity mapping. Re-calibration could be a Phase 4 concern; for now it's good enough.
3. **Fingerprint stability if `FineCategory` becomes part of the dedupe key.** It shouldn't change existing fingerprints because fingerprint is `SHA256(tool:rule:file)` — independent of dedupe key. Confirm with a test that re-running a scan after knowledge-base changes does not change fingerprints.
4. **Performance.** Knowledge-base lookups are O(1) map reads; suppression is O(findings × rules) glob matches, which is fine up to a few thousand findings. Revisit only if we exceed 100k findings/scan.
5. **`.wolfignore` UX.** Choosing gitignore-style is friendly but our `category=`/`rule=` extension is non-standard. Document clearly. Consider validating files with `wolf wolfignore lint`.
6. **Backwards compat.** Adding fields to `Finding` requires a DB migration. Plan migration to add nullable columns; existing rows tolerate empty `FineCategory`.
7. **CLI vs API parity.** The investigation found that the API code path persists artifacts but the CLI does not all the way. We need a shared `executeScan` helper that both call. This is a refactor that *should* happen in Phase 1 task 1.3.

---

## 6. Test Strategy Summary

| Level | What | Where |
|---|---|---|
| Unit | Detector behavior on synthetic dir trees | exists, extend if needed |
| Unit | Knowledge lookup, validation | new `internal/finding/knowledge/*_test.go` |
| Unit | DedupeMerge with cross-tool inputs | `internal/scan/runner/runner_test.go` |
| Unit | Suppression rules + `.wolfignore` parser | `internal/scan/suppress/*_test.go` |
| Unit | Renderer golden files | `internal/scan/report/fix_doc_test.go` + `testdata/golden/` |
| Integration | End-to-end scan against `testdata/sample-repos/*` produces complete artifact dir | `internal/scan/runner/runner_integration_test.go` |
| Manual | mj reviews `FIX-HIGH.md` from a scan of thewolf itself | once per phase |

Each phase has its own ship gate (see end of each phase section). The plan as a whole is "done" when:

- Scanning thewolf produces a `FIX-HIGH.md` that mj agrees is genuinely actionable, with no test/vendor noise.
- Two consecutive scans of an unchanged repo produce byte-identical `findings.json`.
- `wolf knowledge coverage` reports ≥70% on the top-5 tools.

---

## 7. Out of Scope (this plan)

- AI fix generation (a separate document will cover this once Phase 1–3 ship).
- Web UI changes to consume the new artifacts.
- Cross-scan diff ("what's new since the last scan").
- Per-rule baselining ("accept all current findings as expected, only show new ones").
- Auto-fix loops / scan-fix-rescan.
- New scanners.

---

## 8. Example — Target `FIX-HIGH.md` Output

```markdown
# Wolf Findings — High Priority

**Scan:** 2026-05-14T08:30:00Z
**Repo:** thewolf @ `e863d43`
**Languages:** go, typescript
**Scanners run:** gosec, semgrep, gitleaks, trivy, eslint
**Findings:** 41 high-priority across 6 categories (of 287 total, 1311 deduped from 1842 raw)

---

## 1. SQL Injection — 8 findings

### Fix strategy: Use parameterized queries

Replace string-concatenated SQL with placeholders bound at execution time.

**Before:**
\`\`\`go
db.Query("SELECT * FROM users WHERE id = " + userID)
\`\`\`

**After:**
\`\`\`go
db.Query("SELECT * FROM users WHERE id = ?", userID)
\`\`\`

**References:** CWE-89, OWASP A03:2021

### Locations
- `internal/db/users.go:42` — _corroborated by gosec, semgrep_
  \`\`\`go
  db.Query("SELECT * FROM users WHERE id = " + userID)
  \`\`\`
- `internal/db/orders.go:88` — gosec
  \`\`\`go
  db.Exec("DELETE FROM orders WHERE id=" + orderID)
  \`\`\`
- ...

---

## 2. Hardcoded Secrets — 3 findings

### Fix strategy: Rotate and remove from git history
...

---

## 6. Uncategorized — 5 findings

These findings did not match any known fix strategy. See the tool's own documentation linked below.

- `internal/foo/bar.go:12` — semgrep `python.lang.security.audit.foo` — [docs](https://...)
- ...
```

---

## 9. Implementation Order (concrete checklist)

This is the order tasks will be executed if/when this plan is approved.

### Phase 1

- [ ] 1.1 — Wire detector into CLI (`cmd/wolf/main.go`)
- [ ] 1.2 — Add `RawOutputDir` to `RunConfig`; persist raw bytes (`internal/scan/runner/runner.go`)
- [ ] 1.3 — Wire report writers to disk in shared `executeScan` (refactor CLI + API to share helper)
- [ ] 1.4 — Add `manifest.json` writer (`internal/scan/report/manifest.go`)
- [ ] 1.5 — Phase 1 ship-gate test pass

### Phase 2

- [ ] 2.1 — Knowledge base scaffolding (`internal/finding/knowledge/`)
- [ ] 2.2 — Seed knowledge entries for top 5 scanners + 25 fix strategies
- [ ] 2.3 — DedupeMerge with cross-tool corroboration
- [ ] 2.4 — Categorize findings at parse time
- [ ] 2.5 — `FIX-HIGH.md` + `FIX-ALL.md` renderer
- [ ] 2.6 — DB migration for new Finding fields
- [ ] 2.7 — `wolf knowledge coverage` + `wolf knowledge validate` subcommands
- [ ] 2.8 — Phase 2 ship-gate test pass

### Phase 3

- [ ] 3.1 — Built-in suppression defaults (`internal/scan/suppress/defaults.go`)
- [ ] 3.2 — `.wolfignore` parser
- [ ] 3.3 — Apply suppression in renderer, add `Suppressed` fields
- [ ] 3.4 — Phase 3 ship-gate test pass

---

*End of plan. Awaiting review.*
