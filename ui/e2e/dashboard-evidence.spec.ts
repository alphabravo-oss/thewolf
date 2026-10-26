import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installScannerApiMock } from "./scanner-api-mock";

function posture() {
  return {
    generated_at: "2026-10-13T12:00:00Z", repo_count: 3, gates_failing: 1,
    open_findings_by_severity: { critical: 1, high: 1 }, week_over_week_delta: {},
    priorities: [
      { finding_id: "issue-new", repo_id: "payments", name: "Payments", title: "SQL query accepts untrusted input", severity: "critical", tool: "semgrep", file_path: "src/checkout.ts", line: 42, state: "new", confidence: "high" },
      { finding_id: "issue-old", repo_id: "api", name: "API", title: "Existing vulnerable dependency", severity: "high", tool: "trivy", file_path: "go.mod", line: 9, state: "existing", confidence: "" },
    ],
    coverage: { fresh_days: 7, fresh_repos: 1, rows: [
      { repo_id: "api", name: "API", branch: "main", scan_id: "scan-partial", status: "incomplete", detail: "Trivy did not complete.", last_complete_at: "2026-10-01T12:00:00Z", gate_failing: false },
      { repo_id: "new-repo", name: "New service", branch: "", status: "never_scanned", detail: "Run the first scan to establish coverage.", gate_failing: false },
      { repo_id: "payments", name: "Payments", branch: "main", scan_id: "scan-payments", status: "fresh", detail: "All selected tools completed within 7 days.", last_complete_at: "2026-10-13T11:00:00Z", gate_failing: true },
    ] },
    work: [{ scan_id: "scan-stuck", repo_id: "api", name: "API", branch: "release", status: "running", phase: "scanning", stalled: true }],
    changes: { window_days: 7, compared_branches: 1, excluded_branches: 2, new: 1, resurfaced: 0, resolved: 2, suppressed: 1, rows: [
      { repo_id: "payments", name: "Payments", branch: "main", scan_id: "scan-payments", baseline_scan_id: "scan-before", new: 1, resurfaced: 0, resolved: 2, suppressed: 1 },
      { repo_id: "api", name: "API", branch: "main", scan_id: "scan-partial", reason: "Needs a recent, complete scan.", new: 0, resurfaced: 0, resolved: 0, suppressed: 0 },
    ] },
  };
}

async function dashboard(page: Page, data = posture()) {
  const state = await installScannerApiMock(page);
  await page.route(/\/api(?:\/v1)?\/fleet\/posture(?:\?|$)/, (route) => route.fulfill({ json: { data } }));
  await page.route(/\/api(?:\/v1)?\/scanners\/workers$/, (route) => route.fulfill({ json: { data: [] } }));
  return state;
}

test("dashboard leads with actionable evidence and exposes gaps @browser-matrix", async ({ page }, testInfo) => {
  const state = await dashboard(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Act on high-risk findings" })).toBeVisible();
  await expect(page.getByRole("link", { name: "SQL query accepts untrusted input" })).toHaveAttribute("href", /\/findings\/issue-new$/);
  await expect(page.getByRole("link", { name: "Existing vulnerable dependency" })).toBeVisible();
  await page.getByRole("combobox", { name: "Finding priority scope" }).click();
  await page.getByRole("option", { name: "New / resurfaced · 7 days" }).click();
  await expect(page.getByText("Existing vulnerable dependency", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "SQL query accepts untrusted input" })).toBeVisible();
  await page.getByRole("combobox", { name: "Finding priority scope" }).click();
  await page.getByRole("option", { name: "All open critical / high" }).click();
  await expect(page.getByRole("link", { name: "Existing vulnerable dependency" })).toBeVisible();
  await expect(page.getByText("Trivy did not complete.", { exact: true })).toBeVisible();
  await expect(page.getByRole("progressbar", { name: "Repositories with fresh, complete coverage" })).toHaveAttribute("value", "1");
  await expect(page.getByText("Lease expired", { exact: true })).toBeVisible();
  await expect(page.getByText(/No workers reported in the last minute/)).toBeVisible();
  await expect(page.getByRole("link", { name: "Baseline scan", exact: true })).toHaveAttribute("href", /\/scans\/scan-before$/);
  await expect(page.getByRole("link", { name: "Run first scan →", exact: true })).toHaveAttribute("href", /repo_id=new-repo/);
  expect(state.getRequests.some((path) => path.startsWith("/findings/trends"))).toBe(false);
  await expect(page.getByText("Findings over time", { exact: true })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const a11y = await new AxeBuilder({ page }).include("#main-content").withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("dashboard-evidence.png"), fullPage: true });
});

test("dashboard does not call missing comparison evidence improvement @browser-matrix", async ({ page }) => {
  const data = posture();
  data.changes = { window_days: 7, compared_branches: 0, excluded_branches: 3, new: 0, resurfaced: 0, resolved: 0, suppressed: 0, rows: [] };
  data.priorities = [];
  await dashboard(page, data);
  await page.goto("/");
  await expect(page.getByText(/Progress is unknown/)).toBeVisible();
  const progress = page.getByRole("region", { name: "Are we improving?" });
  await expect(progress.getByRole("link", { name: "Verified resolved —", exact: true })).toBeVisible();
  await expect(page.getByText(/Check scan coverage before drawing conclusions|check coverage before drawing conclusions/)).toBeVisible();
});

test("dashboard read errors remain unknown and can be retried @browser-matrix", async ({ page }) => {
  await dashboard(page);
  let broken = true;
  await page.route(/\/api(?:\/v1)?\/fleet\/posture(?:\?|$)/, (route) => broken
    ? route.fulfill({ status: 503, json: { error: { code: "unavailable", message: "Unavailable" } } })
    : route.fulfill({ json: { data: posture() } }));
  await page.goto("/");
  await expect(page.getByText("Dashboard evidence could not be loaded. Coverage and risk are unknown.")).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("All clear.", { exact: true })).toHaveCount(0);
  broken = false;
  await page.getByRole("button", { name: "Retry dashboard", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Act on high-risk findings" })).toBeVisible();
});

test("non-admin dashboard never requests worker health @browser-matrix", async ({ page }) => {
  const state = await dashboard(page);
  state.currentUser.role = "user";
  state.currentUser.scopes = [];
  let workerRequests = 0;
  await page.route(/\/api(?:\/v1)?\/scanners\/workers$/, (route) => { workerRequests++; return route.fulfill({ json: { data: [] } }); });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Keep work moving" })).toBeVisible();
  expect(workerRequests).toBe(0);
  await expect(page.getByRole("link", { name: "Inspect scanner settings →" })).toHaveCount(0);
});
