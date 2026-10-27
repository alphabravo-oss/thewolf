// Richer fixtures for the visual baseline sweep (docs/UI_DESIGN_PLAN.md T3.5):
// real tables and cards on the list pages instead of empty states. Registered
// after installScannerApiMock so these win for the paths they know and fall
// back to the shared mock for everything else. The clock is pinned so relative
// times ("3h ago") render the same on every run.
import type { Page, Route } from "@playwright/test";

export const FIXED_NOW = new Date("2026-10-27T12:00:00Z");
const ago = (hours: number) => new Date(FIXED_NOW.getTime() - hours * 3600_000).toISOString();

const repo = (id: string, name: string, source_type: string, source_path: string, extra: Record<string, unknown> = {}) => ({
  id, user_id: "user-admin", name, source_type, source_path, default_branch: "main",
  detected_languages: '{"go":62,"typescript":38}', detected_frameworks: "[]", detected_at: ago(48), created_at: ago(24 * 30), updated_at: ago(2), ...extra,
});
const REPOS = [
  repo("payments-api", "payments-api", "github", "acme/payments-api"),
  repo("identity-service", "identity-service", "git", "https://gitlab.example.com/platform/identity.git"),
  repo("web-frontend", "web-frontend", "github", "acme/web-frontend", { detected_languages: '{"typescript":91}' }),
  repo("infra-terraform", "infra-terraform", "local", "/srv/repos/infra-terraform", { detected_languages: '{"hcl":100}' }),
  repo("ledger-worker", "ledger-worker", "ssh", "/srv/code/ledger-worker"),
  repo("docs-site", "docs-site", "local", "/srv/repos/docs-site", { detected_languages: '{"markdown":80}' }),
];

const scan = (id: string, repoId: string, status: string, extra: Record<string, unknown> = {}) => ({
  id, user_id: "user-admin", repo_id: repoId, branch: "main", status, profile: "standard",
  tools_selected: '["semgrep","trivy","gitleaks"]', tools_completed: '["semgrep","trivy","gitleaks"]', tools_running: "[]", tools_failed: "[]",
  finding_count: 0, completeness: "complete", created_at: ago(5), started_at: ago(5), completed_at: ago(4.9), updated_at: ago(4.9),
  repo: REPOS.find((r) => r.id === repoId), ...extra,
});
const SCANS = [
  scan("scan-0001aaaa", "payments-api", "completed", { finding_count: 14, commit_sha: "9f2c1d7a3b" }),
  scan("scan-0002bbbb", "identity-service", "running", { started_at: ago(0.2), completed_at: undefined, tools_completed: '["semgrep"]', tools_running: '["trivy"]', finding_count: 3, created_at: ago(0.2) }),
  scan("scan-0003cccc", "web-frontend", "completed", { finding_count: 31, tools_completed: '["semgrep","trivy"]', tools_failed: '["gitleaks"]', completeness: "incomplete", created_at: ago(9) }),
  scan("scan-0004dddd", "infra-terraform", "failed", { finding_count: 0, tools_completed: "[]", tools_failed: '["trivy"]', failure_message: "repository source is unavailable", created_at: ago(26) }),
  scan("scan-0005eeee", "ledger-worker", "completed", { finding_count: 6, created_at: ago(30) }),
  scan("scan-0006ffff", "docs-site", "cancelled", { finding_count: 1, created_at: ago(52) }),
  scan("scan-0007gggg", "payments-api", "completed", { finding_count: 17, created_at: ago(76) }),
  scan("scan-0008hhhh", "identity-service", "pending", { started_at: undefined, completed_at: undefined, tools_completed: "[]", created_at: ago(0.05) }),
];

const posture = () => ({
  generated_at: ago(0.01), repo_count: REPOS.length, gates_failing: 2,
  open_findings_by_severity: { critical: 3, high: 11, medium: 27, low: 16, info: 5 },
  week_over_week_delta: { critical: -1, high: 2 },
  priorities: [
    { finding_id: "finding-0", repo_id: "payments-api", name: "payments-api", title: "Hard-coded credential in config", severity: "critical", tool: "gitleaks", file_path: "config/prod.yaml", line: 12, state: "new", confidence: "high" },
    { finding_id: "finding-1", repo_id: "identity-service", name: "identity-service", title: "SQL query built from request input", severity: "critical", tool: "semgrep", file_path: "internal/users/query.go", line: 88, state: "resurfaced", confidence: "high" },
    { finding_id: "finding-2", repo_id: "web-frontend", name: "web-frontend", title: "Prototype pollution in lodash", severity: "high", tool: "trivy", file_path: "package-lock.json", line: 0, state: "existing", confidence: "medium" },
    { finding_id: "finding-3", repo_id: "payments-api", name: "payments-api", title: "Weak TLS cipher suite enabled", severity: "high", tool: "semgrep", file_path: "internal/tls/config.go", line: 31, state: "existing", confidence: "medium" },
  ],
  coverage: { fresh_days: 7, fresh_repos: 3, rows: [
    { repo_id: "web-frontend", name: "web-frontend", branch: "main", scan_id: "scan-0003cccc", status: "incomplete", detail: "gitleaks did not complete.", last_complete_at: ago(24 * 9), gate_failing: false },
    { repo_id: "infra-terraform", name: "infra-terraform", branch: "main", scan_id: "scan-0004dddd", status: "failed", detail: "The repository source is unavailable.", last_complete_at: ago(24 * 12), gate_failing: true },
    { repo_id: "docs-site", name: "docs-site", branch: "", status: "never_scanned", detail: "Run the first scan to establish coverage.", gate_failing: false },
    { repo_id: "payments-api", name: "payments-api", branch: "main", scan_id: "scan-0001aaaa", status: "fresh", detail: "All selected tools completed within 7 days.", last_complete_at: ago(5), gate_failing: true },
  ] },
  work: [
    { scan_id: "scan-0002bbbb", repo_id: "identity-service", name: "identity-service", branch: "main", status: "running", phase: "scanning", stalled: false },
    { scan_id: "scan-0008hhhh", repo_id: "identity-service", name: "identity-service", branch: "main", status: "pending", phase: "", stalled: false },
  ],
  changes: { window_days: 7, compared_branches: 3, excluded_branches: 1, new: 6, resurfaced: 1, resolved: 9, suppressed: 2, rows: [
    { repo_id: "payments-api", name: "payments-api", branch: "main", scan_id: "scan-0001aaaa", baseline_scan_id: "scan-0007gggg", new: 2, resurfaced: 0, resolved: 5, suppressed: 1 },
    { repo_id: "identity-service", name: "identity-service", branch: "main", scan_id: "scan-0002bbbb", baseline_scan_id: "scan-0005eeee", new: 4, resurfaced: 1, resolved: 4, suppressed: 1 },
  ] },
});

const needsAttention = [
  { repo_id: "infra-terraform", name: "infra-terraform", reason: "scan_failed", detail: "Latest scan failed: repository source is unavailable", score: 90 },
  { repo_id: "payments-api", name: "payments-api", reason: "gate_failing", detail: "Gate blocks on 1 critical finding", score: 80 },
  { repo_id: "web-frontend", name: "web-frontend", reason: "incomplete", detail: "gitleaks did not complete", score: 60 },
  { repo_id: "docs-site", name: "docs-site", reason: "stale", detail: "Never scanned", score: 40 },
];

const byRepo = [
  { repo_id: "web-frontend", name: "web-frontend", total: 31, critical: 0, high: 4, medium: 15, low: 9, info: 3 },
  { repo_id: "payments-api", name: "payments-api", total: 14, critical: 2, high: 4, medium: 5, low: 2, info: 1 },
  { repo_id: "ledger-worker", name: "ledger-worker", total: 6, critical: 0, high: 1, medium: 3, low: 2, info: 0 },
  { repo_id: "identity-service", name: "identity-service", total: 3, critical: 1, high: 2, medium: 0, low: 0, info: 0 },
];

const rules = [
  { key: "CVE-2024-4067", repos: 3, findings: 9, tool: "trivy", title: "Regular expression denial of service in micromatch", severity: "high", repo_ids: ["web-frontend", "docs-site"], repo_names: ["web-frontend", "docs-site"] },
  { key: "go.lang.security.audit.sqli", repos: 2, findings: 5, tool: "semgrep", title: "SQL query built from request input", severity: "critical", repo_ids: ["identity-service"], repo_names: ["identity-service"] },
  { key: "generic.secrets.security.detected-aws-key", repos: 1, findings: 2, tool: "gitleaks", title: "AWS access key committed", severity: "critical", repo_ids: ["payments-api"], repo_names: ["payments-api"] },
];

const vuln = (id: string, title: string, severity: string, category: string, evidence: number, tools: string[], baseline = "new") => ({
  id, repo_id: "payments-api", scan_id: "scan-0001aaaa", canonical_key: id, title, severity, category, confidence: evidence > 2 ? "high" : evidence > 1 ? "medium" : "low",
  baseline_state: baseline, composite_score: 6, evidence_count: evidence, finding_ids: [`finding-${id}`], corroborated_by: tools, created_at: ago(30), updated_at: ago(5),
});
const VULNS = [
  vuln("vuln-sqli", "SQL injection through request input", "critical", "sast", 3, ["semgrep", "gosec", "codeql"]),
  vuln("vuln-aws", "AWS access key committed to the repository", "critical", "secrets", 2, ["gitleaks", "trufflehog"]),
  vuln("vuln-lodash", "Prototype pollution in lodash", "high", "sca", 2, ["trivy", "grype"], "existing"),
  vuln("vuln-tls", "Weak TLS cipher suite enabled", "high", "sast", 1, ["semgrep"], "existing"),
  vuln("vuln-redos", "Regular expression denial of service", "medium", "sca", 2, ["trivy", "osv-scanner"], "resurfaced"),
  vuln("vuln-verbose", "Verbose error message exposes stack trace", "low", "sast", 1, ["semgrep"]),
];
const vulnDetail = {
  ...VULNS[0],
  merge_reason: "Same sink reached from the same handler, reported by three tools.",
  evidence: [
    { id: "ev-1", vulnerability_id: "vuln-sqli", finding_id: "finding-1", tool_name: "semgrep", title: "SQL query built from request input", file_path: "internal/users/query.go", line_start: 88, rule_id: "go.lang.security.audit.sqli", reason: "same location", created_at: ago(30) },
    { id: "ev-2", vulnerability_id: "vuln-sqli", finding_id: "finding-4", tool_name: "gosec", title: "G201: SQL string formatting", file_path: "internal/users/query.go", line_start: 88, rule_id: "G201", reason: "same location", created_at: ago(30) },
    { id: "ev-3", vulnerability_id: "vuln-sqli", finding_id: "finding-5", tool_name: "codeql", title: "Query built from user-controlled source", file_path: "internal/users/query.go", line_start: 90, rule_id: "go/sql-injection", reason: "same data flow", created_at: ago(30) },
  ],
};

const COLLECTIONS = [
  { id: "backend", user_id: "user-admin", name: "Backend services", description: "Payments, identity and the ledger worker", repo_count: 3, created_at: ago(24 * 60), updated_at: ago(24 * 2) },
  { id: "frontend", user_id: "user-admin", name: "Customer-facing apps", description: "Web frontend and docs site", repo_count: 2, created_at: ago(24 * 45), updated_at: ago(24 * 6) },
  { id: "infra", user_id: "user-admin", name: "Infrastructure", description: "", repo_count: 1, created_at: ago(24 * 20), updated_at: ago(24 * 20) },
];
const collectionDetail = {
  collection: COLLECTIONS[0],
  repos: [REPOS[0], REPOS[1], REPOS[4]],
  scans: [SCANS[0], SCANS[1], SCANS[4], SCANS[6]],
};

const audit = (n: number, method: string, path: string, status: number, event: string, category: string, severity: string, hours: number) => ({
  id: `audit-${n}`, method, path, status_code: status, user_id: "user-admin-0001", action: event, event_type: event, category, severity, ip: "192.0.2.14", created_at: ago(hours),
});
const AUDIT = [
  audit(1, "POST", "/api/v1/scans", 201, "scan.created", "data", "info", 0.1),
  audit(2, "PUT", "/api/v1/findings/finding-1/status", 200, "finding.status_changed", "data", "info", 0.6),
  audit(3, "POST", "/api/v1/auth/login", 401, "auth.login_failed", "auth", "warning", 1.2),
  audit(4, "DELETE", "/api/v1/repos/docs-old", 204, "repo.deleted", "data", "warning", 3),
  audit(5, "PUT", "/api/v1/settings", 200, "settings.updated", "config", "info", 6),
  audit(6, "POST", "/api/v1/credentials", 201, "credential.created", "secrets", "info", 9),
  audit(7, "POST", "/api/v1/scans", 500, "scan.created", "data", "error", 14),
  audit(8, "POST", "/api/v1/collections", 201, "collection.created", "data", "info", 26),
];

const COVERAGE_TOOLS = [
  ["semgrep", "Semgrep", "sast", "A", "1.99.0", false], ["gosec", "gosec", "sast", "A", "2.21.4", false], ["codeql", "CodeQL", "sast", "B", "2.19.2", true],
  ["trivy", "Trivy", "sca", "A", "0.58.1", true], ["grype", "Grype", "sca", "A", "0.86.1", true], ["osv-scanner", "OSV-Scanner", "sca", "A", "1.9.2", true],
  ["gitleaks", "Gitleaks", "secrets", "A", "8.22.1", false], ["trufflehog", "TruffleHog", "secrets", "B", "3.88.0", false],
  ["checkov", "Checkov", "iac", "A", "3.2.350", false], ["tfsec", "tfsec", "iac", "B", "1.28.11", false], ["hadolint", "Hadolint", "iac", "A", "2.12.0", false],
  ["bandit", "Bandit", "sast", "A", "1.8.0", false], ["syft", "Syft", "sbom", "A", "1.18.1", false], ["zap", "OWASP ZAP", "dast", "C", "2.15.0", true],
].map(([name, display_name, category, integration_tier, pinned_version, network_required]) => ({ name, display_name, category, integration_tier, pinned_version, network_required, parser_contract: true, depth: "federated-scanner" }));

const fixJob = (id: string, status: string, repoId: string, extra: Record<string, unknown> = {}) => ({
  id, user_id: "user-admin", type: "scan", repo_id: repoId, scan_id: "scan-0001aaaa", finding_ids: ["finding-0"], target_branch: "main", engine: "claude", mode: "fix",
  severity_floor: "high", max_attempts: 3, max_loops: 3, current_loop: 1, human_in_the_loop: true, model: "sonnet", pushed: false, status, summary: "",
  result_branch: `wolf-fix/${id}`, created_at: ago(3), updated_at: ago(1), ...extra,
});
const FIX_JOBS = [
  fixJob("fix-aaaa1111", "running", "payments-api", { started_at: ago(0.5) }),
  fixJob("fix-bbbb2222", "awaiting_review", "identity-service", { current_loop: 2, pause_reason: "review" }),
  fixJob("fix-cccc3333", "queued", "web-frontend", { result_branch: "", queued_behind: { id: "fix-aaaa1111" } }),
  fixJob("fix-dddd4444", "succeeded", "ledger-worker", { pushed: true, finished_at: ago(20) }),
  fixJob("fix-eeee5555", "failed", "infra-terraform", { error: "tests failed after patch", finished_at: ago(40) }),
];

/** Route handler over the richer data. Anything it does not recognise falls back. */
export async function installRichFixtures(page: Page) {
  await page.clock.setFixedTime(FIXED_NOW);
  await page.route("**/api/**", (route: Route) => {
    const url = new URL(route.request().url());
    const p = url.pathname.replace(/^\/api(?:\/v1)?/, "");
    const q = url.searchParams;
    const ok = (data: unknown, meta?: unknown) => route.fulfill({ json: { data, ...(meta ? { meta } : {}) } });
    if (route.request().method() !== "GET") return route.fallback();
    if (p === "/repos") return ok(REPOS);
    if (p === "/collections") return ok(COLLECTIONS);
    if (p === "/collections/backend") return ok(collectionDetail);
    if (p === "/scans") {
      let rows = SCANS;
      if (q.get("repo_id")) rows = rows.filter((s) => s.repo_id === q.get("repo_id"));
      if (q.get("status")) rows = rows.filter((s) => s.status === q.get("status"));
      const per = Number(q.get("per_page") || q.get("limit") || 50);
      return ok(rows.slice(0, per), { total: rows.length, page: 1, per_page: per });
    }
    if (p === "/fleet/posture") return ok(posture());
    if (p === "/fleet/needs-attention") return ok(needsAttention);
    if (p === "/findings/by-repo") return ok(byRepo);
    if (p === "/findings/aggregate") return ok(rules);
    if (p === "/vulnerabilities") return ok(VULNS, { total: VULNS.length, page: 1, per_page: 50 });
    if (p === "/vulnerabilities/vuln-sqli") return ok(vulnDetail);
    if (p === "/audit-log") return ok(AUDIT.slice(0, Number(q.get("limit") || q.get("per_page") || 50)), { total: 240, page: 1, per_page: 50 });
    if (p === "/coverage") return ok({ source: "tools.yaml", tool_count: COVERAGE_TOOLS.length, honesty: "Honest scanner matrix from tools.yaml. Wolf does not claim CPG depth from federated tools.", tools: COVERAGE_TOOLS });
    if (p === "/settings") return ok({ scan_api_enabled: true, scanner_release_management_enabled: true, scanner_notify_only: "true", autofix_enabled: "true" });
    if (p === "/fixes") return ok(FIX_JOBS);
    return route.fallback();
  });
}
