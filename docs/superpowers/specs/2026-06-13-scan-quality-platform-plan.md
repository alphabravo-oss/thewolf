# Design & Plan: Scan Quality Platform, Baselines, Suppression, and CI-Ready Gates

- **Date:** 2026-06-13
- **Branch:** `main`
- **Status:** Proposed
- **Author:** Codex planning session

---

## 1. Goal

Make Wolf dramatically better at scanning repositories by turning scanner output
into a stable, explainable, policy-aware security workflow.

Wolf should not just run many tools. It should help an engineer answer:

1. What changed since the last good scan?
2. Which findings are new, existing, fixed, resurfaced, suppressed, or accepted risk?
3. Which scanners ran, which scanners skipped, and why?
4. Which findings are likely real and worth fixing first?
5. Which results are actionable enough to block a merge, release, or deployment?
6. Which findings can be exported to SARIF or imported from external scanners?
7. Can the same scan-quality workflow work for local paths, GitHub clones, remote
   SSH development nodes, and eventually CI?

The desired end state is a scan-quality platform with:

- Stable finding identity and fingerprints.
- Deterministic normalization across all scanner plugins.
- Durable suppressions with audit trails and expiration.
- Baseline and diff scans for "new findings only" workflows.
- Quality gates with global, collection, repo, and branch policies.
- Scanner planning and explainability.
- Scan manifests and artifacts that make results reproducible.
- SARIF import/export as a first-class integration surface.
- Performance controls suitable for monorepos and expensive scanners.
- UI, API, and CLI flows that expose the same underlying model.

---

## 2. Current State

### 2.1 Strong Existing Foundation

Wolf already has several pieces that should be preserved and extended:

| Capability | Current State |
|---|---|
| Scanner integrations | Modular plugins under `plugins/*`. |
| Container execution | Centralized runner with scanner images and bucket routing. |
| Toolchain management | `scanners/tools.yaml`, image strategy validation, version checks, and smoke tests exist or are planned in the scanner toolchain work. |
| Finding model | `internal/models/finding.go` has a canonical `Finding` struct with scanner, category, severity, path, line, snippet, CWE, rule, scoring, and SARIF data. |
| Enrichment hooks | Runner applies deterministic knowledge such as fine category and fix strategy IDs. |
| Deduplication | Runner deduplicates findings, including cross-tool matches when fine categories are known. |
| Language/framework detection | `internal/scan/detector` detects many languages, frameworks, manifests, and repo signals. |
| Default suppressions | `internal/scan/suppress` has extensive default path suppressions for vendor, generated, cache, fixture, workflow, lockfile, and report paths. |
| Report writers | JSON, Markdown, and SARIF report generation exist in some form. |
| Remote scan planning | A separate remote SSH node plan exists for remote development checkout scanning. |

### 2.2 Main Gaps

| Gap | Impact |
|---|---|
| Fingerprints are not stable enough | Findings can churn when line numbers, snippets, or tool names change. |
| Fine category, confidence, corroboration, and suppression state are not fully durable | UI/API cannot reliably support triage lifecycle. |
| No first-class baseline model | Engineers cannot focus on new findings without manually comparing scans. |
| No durable suppression workflow | Ignored issues cannot be managed with reason, owner, expiration, and audit history. |
| No quality gate policy engine | Wolf cannot answer whether a scan should pass, warn, or fail. |
| Scanner selection is not sufficiently explainable | Users need to know why a scanner ran or skipped. |
| Scan provenance is incomplete | Local, GitHub, SSH, and future CI scans need the same source manifest. |
| Performance controls are too coarse | Monorepos and expensive scanners need resource-aware scheduling and partial scan support. |
| SARIF is not productized end to end | Import/export should be reliable enough for GitHub code scanning and CI later. |

---

## 3. Design Principles

1. **Normalize before rendering.** All UI, CLI, reports, gates, and exports should
   consume normalized findings, not scanner-specific raw output.
2. **Preserve raw evidence.** Raw scanner outputs, logs, command metadata, image
   versions, and parser diagnostics should be retained for reproducibility.
3. **Use stable identities.** Finding identity should survive line shifts, small code
   movement, scanner version changes, and cross-tool corroboration where possible.
4. **Keep scans immutable.** A completed scan is evidence. Suppressions, baselines,
   and triage state reference findings without rewriting the historical scan record.
5. **Make policy explicit.** Gates should be configured, explainable, and auditable.
6. **Prefer deterministic logic.** Categorization, suppression, gates, and fingerprints
   should be deterministic. AI can consume artifacts later but should not be required
   for scan correctness.
7. **One model for all source types.** Local, GitHub, Git clone, SSH remote, and future
   CI scans should share the same result, baseline, suppression, and policy model.
8. **No secret leakage.** Secret findings, SSH credentials, tokens, and private paths
   require redaction, access control, and careful artifact handling.
9. **Operator control over freshness.** Scanner updates and external network calls
   should be visible and deliberate, never hidden inside normal scan execution.

---

## 4. Proposed Architecture

### 4.1 Package Layout

Add or evolve these packages:

| Package | Responsibility |
|---|---|
| `internal/finding/identity` | Stable fingerprints, location fingerprints, semantic anchors, and backfill helpers. |
| `internal/finding/normalize` | Per-tool normalized fields, severity calibration, category mapping, evidence shaping. |
| `internal/finding/baseline` | Baseline creation, selection, comparison, and lifecycle transitions. |
| `internal/finding/diff` | New/existing/fixed/resurfaced classification between scans. |
| `internal/finding/suppress` | Durable suppression matching, `.wolfignore` parsing, precedence, and audit logic. |
| `internal/finding/gates` | Quality policy evaluation and pass/warn/fail decisions. |
| `internal/finding/sarifio` | SARIF import, export, validation, and normalized mapping. |
| `internal/scan/planner` | Scanner selection plan with run/skip reasons and resource class metadata. |
| `internal/scan/resources` | Tool timeouts, concurrency, memory classes, cache policy, and scheduling. |
| `internal/scan/artifacts` | Manifest, raw evidence, per-tool records, artifact retention, and redaction. |
| `internal/api/routes/baselines` | Baseline and comparison API endpoints. |
| `internal/api/routes/policies` | Quality gate policy endpoints. |

Existing packages can be reused where they already fit. The point is to separate
identity, normalization, suppression, baseline, and policy responsibilities instead
of letting the runner accumulate all behavior.

### 4.2 Data Model

Extend the persisted finding and scan records with durable fields. Names can be
adapted to existing conventions, but the concepts should remain.

#### Findings

Add or backfill:

| Field | Purpose |
|---|---|
| `stable_fingerprint` | Primary cross-scan identity for baseline/diff workflows. |
| `location_fingerprint` | Path and location-oriented identity for exact matches. |
| `semantic_fingerprint` | Rule/category plus semantic anchor for line-shift tolerance. |
| `identity_version` | Version of fingerprint algorithm used. |
| `fine_category` | Specific category such as `sql-injection`, `hardcoded-secret`, or `container-root-user`. |
| `fix_strategy_id` | Deterministic remediation template key. |
| `confidence` | Normalized confidence such as `low`, `medium`, `high`, `confirmed`. |
| `corroborated_by_json` | Other tools or rules that support the same finding. |
| `suppressed` | Whether the finding is hidden by durable or default suppression. |
| `suppression_id` | Durable suppression record that matched, if any. |
| `suppressed_reason` | User-visible reason summary. |
| `baseline_state` | `new`, `existing`, `fixed`, `resurfaced`, `suppressed`, `accepted_risk`, or `ignored_by_policy`. |
| `introduced_in_scan_id` | Scan where the finding first appeared in the current baseline lineage. |
| `resolved_in_scan_id` | Scan where the finding disappeared, if applicable. |
| `source_kind` | `local_path`, `git_clone`, `github`, `ssh_path`, or future `ci`. |
| `source_ref` | Branch, commit, remote path, PR, or snapshot reference. |

#### New Tables

| Table | Purpose |
|---|---|
| `scan_baselines` | Named repo/branch baselines and their source scan. |
| `scan_comparisons` | Cached comparison summaries between two scans. |
| `finding_suppressions` | Durable suppressions with scope, reason, owner, expiration, and audit fields. |
| `finding_suppression_audit` | Create/update/delete/match events for suppressions. |
| `quality_policies` | Gate policies at global, collection, repo, and branch scope. |
| `quality_gate_results` | Evaluation result for each scan and policy. |
| `scan_artifacts` | Manifest, raw output, logs, reports, and retention metadata. |
| `scanner_run_records` | One record per scanner execution with command, image, version, exit code, duration, and parser status. |
| `sarif_imports` | Imported SARIF metadata, source, hash, and mapping status. |

### 4.3 Scan Manifest

Every scan should produce and persist a manifest:

```json
{
  "scan_id": "01HZ...",
  "source": {
    "kind": "ssh_path",
    "repo_id": 12,
    "repo_path": "/home/dev/project",
    "branch": "feature/auth-hardening",
    "commit_sha": "abc123",
    "dirty_state": "clean",
    "snapshot_strategy": "rsync-to-wolf-cache",
    "remote_node_id": 4,
    "remote_host_fingerprint": "SHA256:..."
  },
  "detection": {
    "languages": ["go", "typescript"],
    "frameworks": ["gin", "react"],
    "manifests": ["go.mod", "ui-next/package.json"]
  },
  "scanner_plan": {
    "run": [{"tool": "gosec", "reason": "go files detected"}],
    "skip": [{"tool": "bandit", "reason": "no python files detected"}]
  },
  "counts": {
    "raw": 120,
    "normalized": 118,
    "deduplicated": 84,
    "suppressed": 21,
    "new": 6,
    "gate_failures": 2
  }
}
```

---

## 5. Stable Finding Identity

### 5.1 Why This Matters

Baselines and suppressions only work if findings can be recognized across scans.
Line numbers alone are too fragile. Tool-specific IDs alone are too narrow.
Different scanners can describe the same issue with different rule IDs.

Wolf should compute multiple fingerprints and use them deliberately:

| Fingerprint | Inputs | Use |
|---|---|---|
| `location_fingerprint` | normalized path, line range, rule ID, tool family | Exact same-location matching. |
| `semantic_fingerprint` | normalized path, fine category, symbol/function/module, snippet hash, dependency identity | Line-shift tolerant matching. |
| `stable_fingerprint` | best available semantic identity plus fallback strategy | Primary baseline and suppression identity. |
| `evidence_fingerprint` | scanner output hash and parser-normalized evidence | Debugging and duplicate detection. |

### 5.2 Identity Rules By Finding Type

| Finding Type | Stable Identity Strategy |
|---|---|
| SAST | repo-relative path, fine category, rule family, function/symbol, normalized snippet hash. |
| Secrets | detector/rule, path, redacted secret HMAC, nearby context hash. Never store raw secret values. |
| SCA | package URL or ecosystem/name/version, advisory ID, manifest path, lockfile path. |
| Container/IaC | resource kind/name, config path, normalized property path, rule family. |
| License | package identity, license ID, manifest path, policy rule. |
| Malware/supply chain | package identity, advisory/source, artifact hash, manifest path. |

### 5.3 Tasks

1. Add `internal/finding/identity` with a versioned fingerprint API.
2. Define `IdentityInput` separate from `models.Finding` so parsers can provide
   richer signals without bloating scanner-specific code.
3. Normalize paths consistently:
   - repo-relative
   - slash-separated
   - no temp directories
   - no remote host prefixes
   - no container mount roots
4. Add semantic anchors:
   - function name
   - method name
   - package/module name
   - dependency package URL
   - IaC resource name
5. Add redacted secret identity:
   - store keyed HMAC of secret value, not the secret
   - rotate HMAC key through application secret config
   - support "cannot compute secret HMAC" fallback when tool redacts before Wolf sees value
6. Add migration/backfill for historical findings.
7. Add `identity_version` so future algorithm changes are explicit.
8. Update deduplication to prefer stable identity, then semantic identity, then
   location identity, then current fallback.

### 5.4 Definition Of Done

- Existing findings receive backfilled fingerprints.
- New scans compute all applicable fingerprints.
- Deduplication uses the new identity hierarchy.
- Fingerprint logic is deterministic and unit-tested.
- Secret fingerprints never contain raw secret material.
- Two unchanged scans of the same repo produce the same fingerprints.
- Moving a finding down by several lines keeps the semantic fingerprint stable when
  the symbol and snippet context remain equivalent.

### 5.5 Tests

- Unit tests for path normalization on local, GitHub clone, and SSH snapshot paths.
- Golden tests for SAST, SCA, secrets, IaC, and license findings.
- Regression test where a finding moves lines but remains in the same function.
- Regression test where a dependency finding moves from one lockfile line to another.
- Security test confirming raw secret values are not stored in DB fields or artifacts.

---

## 6. Normalization And Calibration

### 6.1 Target Behavior

Every scanner plugin should emit findings that Wolf can compare and rank. That means
normalizing at least:

- Severity.
- Confidence.
- Category and fine category.
- CWE or equivalent taxonomy.
- Rule family.
- Affected path and line.
- Evidence snippet with redaction.
- Fix strategy.
- Whether the result is security, quality, dependency, license, or operational noise.

### 6.2 Severity Model

Use a common normalized severity:

| Severity | Meaning |
|---|---|
| `critical` | Likely exploitable, data exposure, active malware, critical CVE, secret leakage. |
| `high` | Real security impact with plausible exploit path or high-risk dependency. |
| `medium` | Meaningful risk but requires conditions, local access, or weaker exploitability. |
| `low` | Defense-in-depth, weak signal, or minor policy issue. |
| `info` | Informational, inventory, style, or metadata. |

Scanner native severity should remain available in raw metadata.

### 6.3 Confidence Model

Use normalized confidence:

| Confidence | Meaning |
|---|---|
| `confirmed` | Confirmed by multiple tools, exploitability signal, reachable dependency, or deterministic secret match. |
| `high` | Strong scanner signal with good rule precision. |
| `medium` | Useful but may need review. |
| `low` | Weak heuristic, noisy rule, or insufficient context. |

### 6.4 Tasks

1. Create a normalization registry keyed by tool and rule.
2. Expand deterministic knowledge mappings:
   - rule ID to fine category
   - rule ID to fix strategy ID
   - rule ID to default confidence
   - rule ID to CWE/OWASP where applicable
3. Add parser contract tests for every scanner plugin:
   - emits normalized severity
   - emits rule ID
   - emits path when path exists
   - preserves native scanner fields in metadata
4. Add cross-tool corroboration:
   - merge findings with compatible identity
   - record `corroborated_by_json`
   - raise confidence when independent tools agree
5. Add severity calibration for SCA:
   - CVSS
   - EPSS if available
   - known exploited flag if available
   - reachability or manifest scope if available
   - fix available
6. Add severity calibration for secrets:
   - verified secret or high-confidence pattern escalates
   - test fixtures and examples use suppression rules rather than severity downgrades
7. Add parser diagnostics:
   - parsed count
   - discarded count
   - reason for discarded records
   - unknown severity/rule counters

### 6.5 Definition Of Done

- Every scanner plugin has a documented normalization contract.
- Unknown rule IDs are allowed but visible in diagnostics.
- Findings have durable fine category and confidence fields.
- Corroborated findings show which tools agreed.
- Severity changes are deterministic and explainable.
- Parser errors do not silently produce empty success.

### 6.6 Tests

- Golden fixture per scanner plugin.
- Unit tests for severity mapping.
- Unit tests for confidence escalation.
- Integration test where Semgrep and Gosec report the same issue and Wolf merges
  them with corroboration.
- Regression test that unknown scanner severity maps to a safe default and emits a diagnostic.

---

## 7. Durable Suppressions

### 7.1 Target Behavior

Wolf should support suppressing findings without losing evidence. Suppression should
be explicit, scoped, auditable, and reversible.

Suppression sources:

1. Built-in default suppressions for vendor/generated/cache/report paths.
2. Repository `.wolfignore`.
3. Server-side durable suppressions from UI/API/CLI.
4. Policy-level ignores for defined categories or severities.

### 7.2 Suppression Scopes

| Scope | Example |
|---|---|
| Fingerprint | Suppress this exact finding. |
| Rule | Suppress `semgrep.go.lang.security.audit.crypto.weak-random` for one repo. |
| Fine category | Suppress `missing-license` for a repo or branch. |
| Path glob | Suppress `**/testdata/**` for secrets. |
| Package/advisory | Suppress `pkg:npm/example@1.2.3` with advisory `GHSA-...`. |
| Branch | Suppress only on `feature/demo`. |
| Expiring acceptance | Suppress until a date with owner and reason. |

### 7.3 Required Suppression Fields

- ID.
- Scope type.
- Scope value.
- Repo/collection/global scope.
- Optional branch.
- Reason.
- Created by.
- Created at.
- Expiration date.
- Status: active, expired, revoked.
- Audit metadata.

### 7.4 Tasks

1. Implement `finding_suppressions` and `finding_suppression_audit` migrations.
2. Add suppression matching service.
3. Add `.wolfignore` parser with explicit grammar:
   - path globs
   - category selectors
   - rule selectors
   - comments
   - optional expiration comments
4. Define precedence:
   - security-critical hard blocks can override broad suppressions when policy says so
   - exact durable suppressions override default display
   - expired suppressions do not match
   - default suppressions mark noise but remain visible in audit/debug views
5. Add API endpoints:
   - create suppression
   - list suppressions
   - revoke suppression
   - preview suppression impact
   - explain why a finding is suppressed
6. Add CLI commands:
   - `wolf suppress add`
   - `wolf suppress list`
   - `wolf suppress revoke`
   - `wolf suppress preview`
7. Add UI workflow:
   - suppress finding
   - require reason
   - optional expiration
   - show matching scope
   - show audit history
8. Add redaction rules so suppressed secrets do not leak in reason text, logs, or exports.

### 7.5 Definition Of Done

- Suppressions are durable and auditable.
- Suppression matching is deterministic and explainable.
- Expired suppressions stop matching automatically.
- Users can preview impact before applying broad suppressions.
- Built-in default suppressions and `.wolfignore` are visible in scan diagnostics.
- Suppressed findings can still be exported when requested with an explicit flag.

### 7.6 Tests

- Unit tests for each suppression scope.
- `.wolfignore` parser golden tests.
- API tests for create/list/revoke/preview.
- UI tests for suppression modal and validation.
- Security tests for secret redaction in suppression reasons and audit events.
- Regression test that broad path suppressions cannot hide hard-blocked secret findings when policy forbids it.

---

## 8. Baselines And Diff Scans

### 8.1 Target Behavior

Engineers should be able to scan a branch and see what is new relative to a trusted
baseline. Wolf should classify findings as:

| State | Meaning |
|---|---|
| `new` | Present in current scan but not in selected baseline. |
| `existing` | Present in both current scan and baseline. |
| `fixed` | Present in baseline but absent in current scan. |
| `resurfaced` | Previously fixed or suppressed finding appears again. |
| `suppressed` | Present but hidden by active suppression. |
| `accepted_risk` | Present and intentionally accepted for a period. |
| `ignored_by_policy` | Present but excluded from gates by policy. |

### 8.2 Baseline Types

| Baseline Type | Use |
|---|---|
| Last successful scan | Default for local branch iteration. |
| Default branch latest | Compare feature branch to `main`. |
| Named baseline | Release or audit checkpoint. |
| Imported SARIF baseline | Compare Wolf scan against external scanner state. |
| Manual snapshot | Operator-selected scan ID. |

### 8.3 Tasks

1. Add `scan_baselines` table.
2. Add `scan_comparisons` table for cached comparison summaries.
3. Implement baseline selection:
   - explicit scan ID
   - repo default branch latest successful scan
   - branch latest successful scan
   - named baseline
4. Implement diff classification:
   - stable fingerprint exact match
   - semantic match
   - location fallback
   - fixed detection
   - resurfaced detection
5. Add API endpoints:
   - create baseline from scan
   - list baselines
   - compare scan to baseline
   - set repo default baseline strategy
6. Add CLI:
   - `wolf baseline create`
   - `wolf baseline list`
   - `wolf scan --baseline <name|scan-id|default-branch|last-successful>`
   - `wolf compare --scan <id> --baseline <id>`
7. Add UI:
   - baseline selector on scan detail
   - diff summary
   - findings grouped by state
   - fixed findings view
8. Add artifact output:
   - `diff.json`
   - `diff.md`
   - optional `new-findings.sarif`

### 8.4 Definition Of Done

- A scan can be compared to a selected baseline.
- New, existing, fixed, and resurfaced counts are persisted and visible.
- Gate policies can evaluate only new unsuppressed findings.
- Baselines are immutable references to scans.
- Comparison logic handles missing baseline gracefully.
- Diff artifacts can be downloaded from API/UI and produced by CLI.

### 8.5 Tests

- Unit tests for state classification.
- Golden tests with two fixture scan outputs.
- Integration test where a finding is introduced, fixed, and reintroduced.
- Integration test comparing feature branch scan to default branch baseline.
- API tests for baseline create/list/compare.
- UI tests for baseline selector and diff filters.

---

## 9. Quality Gates And Policies

### 9.1 Target Behavior

Wolf should produce a clear pass, warn, or fail decision for each scan based on
configurable policy.

Default recommended policy:

- Fail on new unsuppressed critical findings.
- Fail on new unsuppressed high findings in security categories.
- Fail on secrets unless explicitly accepted by a privileged user with expiration.
- Fail on known exploited dependency vulnerabilities when a fix is available.
- Warn on medium security findings.
- Warn on license violations unless policy says to fail.
- Ignore or warn on low-confidence quality findings by default.

### 9.2 Policy Scopes

Policy should resolve from most specific to least specific:

1. Branch.
2. Repo.
3. Collection/team.
4. Global default.

Each policy should declare whether it inherits from parent policy or replaces it.

### 9.3 Example Policy

```yaml
name: default-security-gate
scope: global
mode: enforce
rules:
  - id: fail-new-critical
    when:
      baseline_state: new
      severity: critical
      suppressed: false
    action: fail
  - id: fail-secrets
    when:
      fine_category: hardcoded-secret
      suppressed: false
    action: fail
  - id: warn-new-medium
    when:
      baseline_state: new
      severity: medium
      confidence_min: medium
    action: warn
```

### 9.4 Tasks

1. Add `quality_policies` and `quality_gate_results` migrations.
2. Implement policy parser and evaluator.
3. Support policy conditions:
   - severity
   - confidence
   - category
   - fine category
   - baseline state
   - scanner/tool
   - path glob
   - package ecosystem
   - fix available
   - known exploited
   - suppressed
4. Support actions:
   - pass
   - warn
   - fail
   - require review
   - ignore
5. Add API endpoints:
   - list policies
   - create/update policy
   - evaluate policy for scan
   - explain gate result
6. Add CLI:
   - `wolf gate eval --scan <id>`
   - `wolf gate explain --scan <id>`
7. Add UI:
   - gate status badge
   - failed rule list
   - matching findings
   - policy editor for admins
8. Design future CI surface:
   - exit code 0 for pass/warn
   - exit code 2 for fail
   - machine-readable `gate-result.json`

### 9.5 Definition Of Done

- Every completed scan has a gate result when a policy applies.
- Gate result explains which rules matched and which findings caused failure.
- Policy evaluation can be run without rescanning.
- Policy changes do not mutate historical findings.
- CLI exit codes are documented and deterministic.

### 9.6 Tests

- Unit tests for every condition operator.
- Golden tests for policy examples.
- Integration tests for global/repo/branch policy precedence.
- API tests for policy CRUD and evaluation.
- CLI tests for exit codes.
- Regression test that suppressed findings do not fail gates unless policy explicitly includes them.

---

## 10. Scanner Planning And Explainability

### 10.1 Target Behavior

Before running a scan, Wolf should build a scanner plan. The plan should say:

- Which scanners will run.
- Which scanners will skip.
- Why each decision was made.
- What each scanner needs.
- Whether the required image is available.
- Estimated resource class and timeout.
- Whether network access is needed.

### 10.2 Tool Manifest Enhancements

Extend `scanners/tools.yaml` with capability metadata:

```yaml
tools:
  semgrep:
    languages: [go, javascript, typescript, python, ruby, java]
    ecosystems: []
    trigger_files: []
    output_formats: [sarif, json]
    resource_class: medium
    default_timeout: 10m
    network_required: false
    default_enabled: true
    skip_if_no_matching_files: true
    mutually_exclusive_with: []
```

### 10.3 Tasks

1. Add planner package that consumes:
   - detected languages/frameworks
   - repo manifests
   - user-selected tools
   - disabled tools
   - tool capability manifest
   - image availability
   - policy requirements
2. Add run/skip reason taxonomy:
   - language detected
   - manifest detected
   - explicitly selected
   - explicitly disabled
   - missing image
   - unsupported platform
   - no matching files
   - timeout budget exceeded
3. Persist scanner plan in scan manifest.
4. Show scanner plan in UI before and after scan.
5. Add CLI:
   - `wolf scan --plan-only`
   - `wolf scanners explain --repo <path>`
6. Add tests that keep tool manifest and planner behavior aligned.

### 10.4 Definition Of Done

- Users can see why every scanner ran or skipped.
- Planner is deterministic for the same repo and config.
- Planner output is persisted with completed scans.
- Scanner skip reasons are available in API/UI/CLI.
- Adding a scanner without capability metadata fails validation.

### 10.5 Tests

- Unit tests for planner decisions.
- Golden tests for Go, Node, Python, Terraform, Docker, and polyglot fixtures.
- Validation test that every registered plugin has manifest capability metadata.
- Integration test for explicit tool selection overriding auto-detection.

---

## 11. Performance And Resource Controls

### 11.1 Target Behavior

Wolf should scan large repositories without unnecessary work, runaway resource use,
or opaque timeouts.

### 11.2 Controls

| Control | Purpose |
|---|---|
| Resource classes | Group scanners into light, medium, heavy, network, and exclusive. |
| Per-tool timeouts | Prevent one tool from hanging the scan. |
| Concurrency by class | Avoid running too many heavy scanners together. |
| Cache mounts | Reuse safe scanner caches across scans. |
| Changed-path mode | Run targeted scans for branch iteration when possible. |
| Module graph | Detect monorepo packages and scan affected modules. |
| Early cancellation | Stop remaining scanners when policy fail-fast is enabled. |
| Progress records | Show real-time scanner status and duration. |

### 11.3 Tasks

1. Add resource classes to tool manifest.
2. Add scheduler that respects:
   - global concurrency
   - heavy scanner limit
   - memory limit
   - network scanner limit
   - exclusive scanner lock
3. Add per-tool timeout config with policy defaults.
4. Add scanner progress events.
5. Add scan cancellation API and CLI handling.
6. Add changed-path input:
   - local git diff
   - GitHub compare API later
   - SSH remote git diff from remote plan
7. Add module detection for common monorepo layouts:
   - Go modules
   - npm/yarn/pnpm workspaces
   - Python pyproject packages
   - Terraform modules
8. Add performance metrics:
   - duration by scanner
   - findings per scanner
   - parser duration
   - artifact size
   - cache hit/miss where available

### 11.4 Definition Of Done

- Scanner concurrency is resource-aware.
- Every scanner has a timeout.
- Users can cancel a running scan.
- Scan detail shows progress and duration per scanner.
- Large monorepo fixture scan runs within an agreed budget.
- Changed-path mode is available for supported scanner types and safely falls back
  to full scan when unsupported.

### 11.5 Tests

- Unit tests for scheduler limits.
- Integration test with fake slow scanners and timeout behavior.
- Integration test for cancellation.
- Performance fixture for monorepo scan time.
- Regression test that cache paths do not leak repo secrets between scans.

---

## 12. Scan Artifacts And Provenance

### 12.1 Target Behavior

Every scan should leave enough evidence to debug, reproduce, export, and audit the
result without rerunning tools.

### 12.2 Artifact Layout

```text
~/.wolf/artifacts/<scan_id>/
  manifest.json
  findings.normalized.json
  findings.raw.json
  diff.json
  gate-result.json
  combined.sarif
  reports/
    summary.md
    fix-high.md
    fix-all.md
  scanners/
    semgrep/
      command.json
      stdout.log
      stderr.log
      raw.sarif
      parsed.json
      diagnostics.json
```

### 12.3 Tasks

1. Define artifact schema and retention policy.
2. Persist `scan_artifacts` rows with path, type, size, checksum, and redaction level.
3. Store per-scanner command metadata:
   - tool name
   - image
   - image digest if available
   - version
   - command args with secret redaction
   - exit code
   - duration
4. Store raw scanner output when safe.
5. Add artifact redaction levels:
   - public summary
   - internal report
   - raw evidence
   - secret-sensitive
6. Add download endpoints with authorization checks.
7. Add cleanup job for retention policy.

### 12.4 Definition Of Done

- Every scan has a manifest.
- Every scanner run has a record.
- Artifacts are checksummed.
- Secret-sensitive artifacts are access-controlled and redacted where appropriate.
- Retention cleanup does not delete DB scan history.

### 12.5 Tests

- Integration test that scan artifacts are created.
- Unit test for command redaction.
- API test for artifact access control.
- Retention cleanup test.
- Golden test for manifest schema.

---

## 13. SARIF Import And Export

### 13.1 Target Behavior

Wolf should export normalized findings as SARIF and import SARIF from external tools
without losing important metadata.

### 13.2 Export Requirements

- SARIF 2.1.0.
- Stable rule IDs.
- Tool metadata includes Wolf version and source scanner.
- Result fingerprints use Wolf stable fingerprints.
- Suppression state represented when requested.
- Baseline state represented through properties.
- Redacted secret evidence.
- Compatible with GitHub code scanning upload expectations where practical.

### 13.3 Import Requirements

- Validate SARIF schema.
- Map SARIF result to Wolf finding.
- Preserve original SARIF in `sarif_data`.
- Generate Wolf fingerprints.
- Track import source and checksum.
- Support imported findings in baselines and gates.

### 13.4 Tasks

1. Create `internal/finding/sarifio`.
2. Add SARIF schema validation.
3. Implement export from normalized findings.
4. Implement import to normalized findings.
5. Add CLI:
   - `wolf sarif export --scan <id>`
   - `wolf sarif import --repo <id> --file results.sarif`
6. Add API endpoints for SARIF import/export.
7. Add GitHub code scanning compatibility checks.

### 13.5 Definition Of Done

- Wolf can export a completed scan to valid SARIF.
- Wolf can import valid SARIF and show findings in normal views.
- SARIF round trip preserves stable identity where possible.
- Invalid SARIF fails with actionable errors.

### 13.6 Tests

- SARIF schema validation tests.
- Export golden files.
- Import fixtures from Semgrep, CodeQL, Trivy, and Gitleaks.
- Round-trip test.
- GitHub code scanning dry-run validation where possible.

---

## 14. Remote, GitHub, And Future CI Scan Quality

### 14.1 Target Behavior

Local, GitHub, and SSH scans should differ only in how source code is prepared. The
finding pipeline, baselines, suppressions, gates, and artifacts should be identical.

### 14.2 Source Provenance Fields

Persist for every scan:

- Source kind.
- Repo ID.
- Branch.
- Commit SHA.
- Dirty state.
- Snapshot strategy.
- Remote node ID when applicable.
- Remote host fingerprint when applicable.
- Git URL or provider repository ID when applicable.
- Pull request or compare ref when applicable later.
- Workspace path inside Wolf cache.

### 14.3 Tasks

1. Align this plan with the remote SSH nodes plan.
2. Add source manifest to all scan paths.
3. For local scans:
   - record git root
   - branch
   - commit
   - dirty state
4. For GitHub/git clone scans:
   - record clone URL without credentials
   - branch/ref
   - commit
   - clone depth
5. For SSH scans:
   - record remote node ID
   - host key fingerprint
   - remote path
   - branch
   - commit
   - dirty state
   - snapshot command
6. Add validation that baseline comparisons only compare compatible source identities
   unless explicitly forced.
7. Reserve CI source kind:
   - provider
   - workflow run ID
   - commit SHA
   - PR number
   - actor
   - uploaded artifact reference

### 14.4 Definition Of Done

- Every scan has source provenance.
- Baselines cannot accidentally compare unrelated repositories.
- SSH scans and local scans produce compatible findings when scanning the same commit.
- Provenance is visible in UI/API/CLI.
- Credentials are never stored in provenance fields.

### 14.5 Tests

- Unit tests for source identity compatibility.
- Integration test comparing local and SSH snapshot of same repo.
- API tests for scan provenance.
- Security tests for credential redaction.

---

## 15. UI And Workflow

### 15.1 Scan Detail View

Add or enhance:

- Gate result at top.
- Baseline selector.
- New/existing/fixed/suppressed tabs.
- Scanner plan and run records.
- Artifact downloads.
- Suppression action.
- Finding explanation panel.
- Raw evidence access for authorized users.

### 15.2 Findings View

Support filters:

- Severity.
- Confidence.
- Category.
- Fine category.
- Scanner.
- Baseline state.
- Suppressed state.
- Path.
- Package ecosystem.
- Fix available.
- Known exploited.

### 15.3 Settings

Add admin settings for:

- Quality policies.
- Default baseline strategy.
- Suppression expiration requirements.
- Artifact retention.
- Scanner resource limits.
- SARIF import/export options.

### 15.4 Tasks

1. Create API DTOs for scan summary, finding list, diff summary, and gate result.
2. Update UI data fetching to use normalized endpoints.
3. Add filters and saved views.
4. Add suppression modal.
5. Add policy editor or YAML editor with validation.
6. Add scanner run diagnostics panel.
7. Add artifact download controls.

### 15.5 Definition Of Done

- A user can start from a scan and understand what failed, why, and what changed.
- A user can suppress a finding with reason and expiration.
- An admin can edit or validate policy.
- Scanner run failures are visible without checking server logs.
- UI does not expose raw secrets or credentials.

### 15.6 Tests

- Component tests for finding filters.
- API integration tests for scan summary.
- End-to-end UI tests for baseline comparison and suppression.
- Accessibility checks for critical workflows.
- Redaction tests in rendered finding details.

---

## 16. CLI And API

### 16.1 CLI Commands

Add or extend:

```text
wolf scan --repo <path> --baseline <strategy> --gate <policy> --format json
wolf scan --plan-only --repo <path>
wolf compare --scan <id> --baseline <id>
wolf baseline create --scan <id> --name <name>
wolf baseline list --repo <id>
wolf suppress add --finding <id> --reason <text> --expires <date>
wolf suppress list --repo <id>
wolf gate eval --scan <id>
wolf gate explain --scan <id>
wolf sarif export --scan <id>
wolf sarif import --repo <id> --file <path>
```

### 16.2 API Endpoints

Add or extend:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/scans/{id}/manifest` | Scan manifest. |
| `GET` | `/api/scans/{id}/findings` | Filtered normalized findings. |
| `GET` | `/api/scans/{id}/diff` | Baseline comparison. |
| `POST` | `/api/scans/{id}/compare` | Compare to selected baseline. |
| `GET` | `/api/scans/{id}/gate` | Gate result. |
| `POST` | `/api/baselines` | Create baseline. |
| `GET` | `/api/repos/{id}/baselines` | List repo baselines. |
| `POST` | `/api/suppressions` | Create suppression. |
| `POST` | `/api/suppressions/preview` | Preview broad suppression. |
| `DELETE` | `/api/suppressions/{id}` | Revoke suppression. |
| `GET` | `/api/policies` | List policies. |
| `PUT` | `/api/policies/{id}` | Update policy. |
| `POST` | `/api/sarif/import` | Import SARIF. |
| `GET` | `/api/scans/{id}/sarif` | Export SARIF. |

### 16.3 Definition Of Done

- CLI and API expose the same core workflows.
- JSON output is stable enough for automation.
- API errors are actionable.
- Authorization is enforced on suppressions, policies, raw artifacts, and SARIF import.

### 16.4 Tests

- CLI smoke tests.
- API route tests.
- Authorization tests.
- JSON schema tests for automation outputs.

---

## 17. Migration And Rollout

### 17.1 Migration Strategy

1. Add nullable columns and new tables.
2. Backfill fingerprints and normalized fields for existing findings.
3. Enable new identity for new scans.
4. Keep old fingerprint field for compatibility.
5. Add dual-read compatibility while UI/API migrate.
6. Turn on baseline support behind a feature flag.
7. Turn on gates in warn-only mode by default.
8. Move to enforcement only after policy configuration is reviewed.

### 17.2 Backfill Considerations

- Historical findings may lack raw metadata.
- Some old findings will only support fallback identity.
- Backfill should record `identity_version`.
- Backfill should be resumable.
- Backfill should not block server startup for large databases.

### 17.3 Operational Settings

Add settings for:

- Default baseline strategy.
- Artifact retention days.
- Raw artifact retention days.
- Gate mode: disabled, warn, enforce.
- Suppression expiration required: yes/no.
- Secret artifact access role.
- Max scanner concurrency.
- Heavy scanner concurrency.

### 17.4 Definition Of Done

- Fresh SQLite setup works.
- Existing SQLite databases migrate.
- Postgres setup works when configured.
- Backfill is resumable.
- Feature flags allow staged rollout.
- No historical scan data is lost.

### 17.5 Tests

- SQLite migration test from current schema.
- Postgres migration test.
- Fresh install test.
- Backfill idempotency test.
- Rollback or downgrade guidance documented if down migrations are not supported.

---

## 18. Security And Privacy Requirements

### 18.1 Requirements

- Never store raw secret values in fingerprints, logs, audit records, or reports.
- Redact credentials from commands, clone URLs, SSH configs, and environment variables.
- Require authorization for:
  - raw artifacts
  - policy changes
  - suppression creation
  - SARIF import
  - gate override
- Audit security-sensitive actions:
  - suppression create/revoke
  - policy update
  - gate override
  - raw artifact download
  - SARIF import
- Avoid running scanners with network access unless required and explicitly declared.
- Preserve container isolation defaults.
- Ensure SSH scan provenance does not expose private paths to unauthorized users.

### 18.2 Tests

- Secret redaction unit tests.
- Authorization integration tests.
- Audit event tests.
- SARIF import malicious payload tests.
- Artifact path traversal tests.
- Policy tampering tests.
- Scanner command redaction tests.

---

## 19. Phased Implementation Plan

### Phase 0: Contract Audit

**Goal:** Document the exact current scan pipeline before changing behavior.

Tasks:

1. Audit all scanner plugins and parser outputs.
2. List required normalized fields per scanner.
3. List current DB schema and migration path.
4. Identify UI/API consumers of findings.
5. Identify report writer assumptions.
6. Create fixture repositories for major ecosystems.

Definition of done:

- Contract document exists.
- Fixture inventory exists.
- Known parser gaps are tracked.
- No code behavior changes required in this phase.

Validation:

- `go test ./...`
- Fixture scan smoke test against current behavior.

### Phase 1: Identity And Normalization

**Goal:** Make findings stable and comparable.

Tasks:

1. Add identity package.
2. Add migrations for fingerprint and normalized fields.
3. Update runner dedupe to use identity hierarchy.
4. Add parser diagnostics.
5. Backfill existing findings.
6. Add golden tests.

Definition of done:

- Stable fingerprints exist for new scans.
- Historical findings are backfilled where possible.
- Deduplication still works and improves cross-tool matching.

Validation:

- Unit/golden tests for fingerprints.
- Two identical scans produce matching fingerprints.
- Moved-line fixture keeps semantic identity stable.

### Phase 2: Suppressions

**Goal:** Make false-positive and accepted-risk handling durable and auditable.

Tasks:

1. Add suppression tables.
2. Add matching service.
3. Add API/CLI support.
4. Add `.wolfignore`.
5. Add UI suppression flow.
6. Add audit events.

Definition of done:

- Findings can be suppressed and unsuppressed.
- Suppressions require reason.
- Expiration works.
- Suppression explanation is visible.

Validation:

- API tests.
- UI workflow test.
- Security redaction tests.

### Phase 3: Baselines And Diff

**Goal:** Show what changed between scans.

Tasks:

1. Add baseline tables.
2. Add comparison engine.
3. Add baseline selection in scan requests.
4. Add API/CLI/UI diff views.
5. Add diff artifacts.

Definition of done:

- Scans can compare against last successful, default branch, named baseline, or scan ID.
- Findings are classified as new/existing/fixed/resurfaced.
- Diff summary is persisted.

Validation:

- Multi-scan fixture tests.
- API tests.
- UI tests for diff filtering.

### Phase 4: Gates And Policies

**Goal:** Convert findings into pass/warn/fail decisions.

Tasks:

1. Add policy model.
2. Add evaluator.
3. Add default policy.
4. Add gate result persistence.
5. Add UI/API/CLI explain flows.
6. Add future CI-compatible exit codes.

Definition of done:

- Every scan can be evaluated by policy.
- Gate result explains matched rules and findings.
- Policies support scope precedence.

Validation:

- Policy golden tests.
- CLI exit code tests.
- API authorization tests.

### Phase 5: Scanner Planning And Resources

**Goal:** Make scanner execution explainable and efficient.

Tasks:

1. Extend scanner manifest.
2. Add planner.
3. Add scheduler/resource limits.
4. Add progress events.
5. Add cancellation.
6. Add `--plan-only`.

Definition of done:

- Run/skip reasons are visible.
- Heavy scanners are resource-limited.
- Users can cancel scans.
- Planner output is persisted.

Validation:

- Planner golden tests.
- Timeout and cancellation tests.
- Monorepo performance test.

### Phase 6: Artifacts And SARIF

**Goal:** Make outputs reproducible and interoperable.

Tasks:

1. Add artifact schema.
2. Persist scanner run records.
3. Add SARIF import/export.
4. Add artifact retention.
5. Add raw evidence authorization.

Definition of done:

- Manifest, findings, diff, gate result, and SARIF are downloadable.
- Raw evidence is retained according to policy.
- SARIF import/export is validated.

Validation:

- Artifact golden tests.
- SARIF round-trip tests.
- Access control tests.

### Phase 7: Source Provenance And Remote Compatibility

**Goal:** Apply the same quality model to local, GitHub, SSH, and future CI scans.

Tasks:

1. Add source manifest to scan records.
2. Integrate with remote SSH scan target model.
3. Validate source compatibility for baselines.
4. Add provenance UI/API/CLI display.
5. Reserve CI source kind without implementing CI execution.

Definition of done:

- Local, GitHub, and SSH scans produce comparable manifests.
- Baselines reject incompatible source identities by default.
- Credentials are redacted from provenance.

Validation:

- Local vs SSH same-commit comparison.
- Source compatibility tests.
- Credential redaction tests.

---

## 20. End-To-End Validation Matrix

| Scenario | Expected Result |
|---|---|
| Clean Go repo scanned twice | Same stable fingerprints, zero new findings on second scan. |
| Finding line shifts | Semantic fingerprint remains stable when context is equivalent. |
| New secret introduced | Gate fails, secret is redacted, finding is new. |
| Secret in test fixture | Default or repo suppression applies only when policy allows. |
| Vulnerable dependency fixed | Finding appears as fixed in diff. |
| Vulnerability reintroduced | Finding appears as resurfaced. |
| Broad suppression created | Preview shows affected findings before save. |
| Suppression expires | Finding reappears and can fail gate. |
| Scanner image missing | Scanner skipped or fails with clear run record depending on policy. |
| Unsupported language | Irrelevant scanners show skip reasons. |
| Large monorepo | Scheduler respects heavy scanner and timeout limits. |
| SARIF export | Valid SARIF 2.1.0 with redacted evidence. |
| SARIF import | Imported findings participate in baselines and gates. |
| SSH scan same commit as local | Comparable findings and compatible source identity. |

---

## 21. Required Test Suites

### Unit Tests

- Fingerprint generation.
- Path normalization.
- Secret HMAC identity.
- Severity mapping.
- Confidence mapping.
- Suppression matching.
- Baseline classification.
- Policy evaluation.
- Scanner planner decisions.
- Scheduler limits.
- SARIF mapping.
- Redaction helpers.

### Integration Tests

- Full scan fixture through normalization, suppression, baseline, and gate.
- SQLite migrations.
- Postgres migrations.
- API baseline workflows.
- API suppression workflows.
- API policy workflows.
- SARIF import/export.
- Artifact creation and retention.
- Scan cancellation.

### Golden Tests

- Per-scanner parser outputs.
- Normalized findings JSON.
- Scan manifest JSON.
- Diff JSON.
- Gate result JSON.
- SARIF export.
- Markdown reports.

### UI Tests

- Scan detail gate summary.
- Baseline selector.
- Finding filters.
- Suppression modal.
- Policy editor validation.
- Scanner diagnostics.
- Artifact downloads.

### Security Tests

- Secret redaction in DB, logs, reports, and SARIF.
- Authorization for raw artifacts.
- Authorization for policy updates.
- Authorization for suppression changes.
- Audit events for sensitive actions.
- Malicious SARIF import.
- Artifact path traversal.
- Credential redaction for Git and SSH sources.

### Performance Tests

- Large monorepo scan budget.
- Heavy scanner concurrency.
- Changed-path scan.
- Parser throughput.
- Artifact size limits.
- Database query performance for finding filters.

---

## 22. Command Validation Checklist

Run these during implementation as applicable:

```text
go test ./...
make scanners-validate
make scanners-upstream-check
make scanners-smoke
cd ui-next && npm run typecheck
cd ui-next && npm run build
```

Additional targeted checks:

```text
wolf scan --repo testdata/fixtures/go-vuln --baseline last-successful --format json
wolf scan --repo testdata/fixtures/polyglot --plan-only
wolf compare --scan <scan-id> --baseline <baseline-id>
wolf gate eval --scan <scan-id>
wolf sarif export --scan <scan-id>
wolf sarif import --repo <repo-id> --file testdata/sarif/semgrep.sarif
```

Database validation:

```text
WOLF_DB_DRIVER=sqlite go test ./internal/db/... ./internal/migrations/...
WOLF_DB_DRIVER=postgres go test ./internal/db/... ./internal/migrations/...
```

---

## 23. Documentation Deliverables

Add or update:

1. `docs/findings.md` with finding lifecycle and field definitions.
2. `docs/baselines.md` with baseline strategies and examples.
3. `docs/suppressions.md` with `.wolfignore`, server-side suppressions, and audit rules.
4. `docs/quality-gates.md` with policy examples and CLI exit codes.
5. `docs/scanner-planning.md` with run/skip reason taxonomy.
6. `docs/sarif.md` with import/export behavior.
7. `docs/artifacts.md` with retention and redaction rules.
8. `docs/remote-scan-provenance.md` with local/GitHub/SSH source identity.
9. API documentation for new endpoints.
10. Admin guide for rollout and policy enforcement.

---

## 24. Risks And Mitigations

| Risk | Mitigation |
|---|---|
| Fingerprint churn creates bad baselines | Version identity algorithms, add golden tests, keep fallback matching, expose confidence in matches. |
| Broad suppressions hide real issues | Require preview, reason, expiration, audit, and policy hard-block overrides. |
| Policies are too complex | Start with a small condition set and validated YAML examples. |
| SARIF mapping loses metadata | Preserve original SARIF in `sarif_data` and add round-trip tests. |
| Performance worsens with more processing | Add scheduler, indexes, query tests, and artifact retention limits. |
| UI becomes noisy | Default to new unsuppressed findings and gate failures, with drill-downs for diagnostics. |
| Secret evidence leaks | Centralize redaction, test DB/log/report/SARIF outputs, restrict raw artifacts. |
| Remote scan paths break identity | Normalize source paths to repo-relative paths and record source provenance separately. |

---

## 25. Open Questions

1. Should quality gates default to warn-only for all users until explicitly enforced?
2. Should suppressions require expiration by default for critical/high findings?
3. Which roles can create suppressions and override gates?
4. Should raw scanner output retention be shorter than normalized artifact retention?
5. Should imported SARIF be trusted for gate decisions by default or marked external?
6. What is the first CI target after this work: GitHub Actions, generic CLI, or SARIF upload?
7. Should baseline names be repo-scoped only or collection-scoped as well?
8. Should changed-path scans ever be allowed to pass gates, or only produce advisory results?

---

## 26. Suggested Implementation Order

Recommended order:

1. Identity and normalization.
2. Durable suppressions.
3. Baselines and diff.
4. Quality gates.
5. Scanner planning and resources.
6. Artifacts and SARIF.
7. Remote/GitHub provenance hardening.
8. UI polish and admin settings.

This order keeps the dependency chain clean. Stable identity enables suppressions
and baselines. Baselines enable useful gates. Gates and planner output make the
product ready for CI later without implementing CI in this phase.

---

## 27. Final Definition Of Done

The project is complete when:

1. Wolf can scan a repo and produce normalized findings with stable fingerprints.
2. Wolf can compare a scan to a baseline and classify new, existing, fixed, and
   resurfaced findings.
3. Wolf can apply durable suppressions with reason, owner, expiration, and audit.
4. Wolf can evaluate a quality policy and produce a pass/warn/fail gate result.
5. Wolf can explain scanner run/skip decisions.
6. Wolf persists scan manifests, scanner run records, artifacts, and provenance.
7. Wolf can export valid SARIF and import external SARIF.
8. Local, GitHub, and SSH scan sources use the same findings, baseline, suppression,
   and gate pipeline.
9. UI, API, and CLI expose the core workflows.
10. Tests cover unit, integration, golden, UI, security, migration, and performance
    scenarios listed above.
11. Documentation exists for operators, admins, and engineers.
12. Default rollout is safe: gates start in warn mode, raw artifacts are protected,
    and no scan silently upgrades scanner tools.
