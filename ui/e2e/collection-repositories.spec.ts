import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installScannerApiMock } from "./scanner-api-mock";

async function collection(page: Page) {
  const state = await installScannerApiMock(page);
  const repo = { id: "payments", user_id: "user-admin", name: "Payments", source_type: "github", source_path: "acme/payments", default_branch: "main", detected_languages: '{}', detected_frameworks: '[]', created_at: "2026-10-01T12:00:00Z" };
  const scan = { id: "latest-partial", repo_id: "payments", branch: "release", status: "completed", tools_selected: '["semgrep","trivy"]', tools_completed: '["semgrep"]', tools_failed: '["trivy"]', finding_count: 7, created_at: "2026-10-13T10:00:00Z" };
  await page.route(/\/api(?:\/v1)?\/collections\/services$/, (route) => route.fulfill({ json: { data: {
    collection: { id: "services", user_id: "user-admin", name: "Services", description: "Customer-facing services" },
    repos: [repo, { ...repo, id: "worker", name: "Worker", source_type: "local", source_path: "/work/worker" }],
    scans: [
      { ...scan, id: "child-scan", origin_scan_id: "latest-partial", created_at: "2026-10-13T11:00:00Z", finding_count: 0 },
      { ...scan, id: "old-scan", created_at: "2026-10-10T10:00:00Z", finding_count: 19 },
      scan,
    ],
  } } }));
  await page.route(/\/api(?:\/v1)?\/fleet\/posture(?:\?|$)/, (route) => route.fulfill({ json: { data: { open_findings_by_severity: { high: 7 }, week_over_week_delta: {}, repo_count: 2, gates_failing: 0 } } }));
  return state;
}

test("collection repository table shows scan evidence and preserves context @browser-matrix", async ({ page }, testInfo) => {
  await collection(page);
  await page.goto("/collections/services");
  const table = page.getByRole("table");
  await expect(table.getByRole("columnheader", { name: "Repository", exact: true })).toBeVisible();
  const payments = table.getByRole("row").filter({ has: page.getByRole("link", { name: "Payments", exact: true }) });
  await expect(payments.getByText("Incomplete coverage", { exact: true })).toBeVisible();
  await expect(payments.getByRole("cell", { name: "1 / 2 1 failed", exact: true })).toBeVisible();
  await expect(payments.getByRole("link", { name: "7", exact: true })).toHaveAttribute("href", "/scans/latest-partial");
  await expect(payments.getByRole("link", { name: "View 2 scans for Payments" })).toHaveAttribute("href", /repo_id=payments/);
  await expect(payments.getByRole("link", { name: "Findings", exact: true })).toHaveAttribute("href", /\/repos\/payments\?collection_id=services#findings$/);
  await expect(table.getByText("Never scanned", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: /Recent scans/ })).toHaveCount(0);
  const a11y = await new AxeBuilder({ page }).include("#main-content").withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("collection-repositories.png"), fullPage: true });
  const repositoryHeader = table.getByRole("columnheader", { name: "Repository", exact: true });
  await repositoryHeader.focus();
  await repositoryHeader.press("Enter");
  await expect(repositoryHeader).toHaveAttribute("aria-sort", "ascending");
  await repositoryHeader.press("Space");
  await expect(repositoryHeader).toHaveAttribute("aria-sort", "descending");
  await expect(table.getByRole("row").nth(1).getByRole("link", { name: "Worker", exact: true })).toBeVisible();
  await page.getByRole("textbox", { name: "Search repositories in this collection..." }).fill("Worker");
  await expect(table.getByRole("link", { name: "Payments", exact: true })).toHaveCount(0);
  await expect(table.getByRole("link", { name: "Worker", exact: true })).toBeVisible();
});

test("scan from collection sends the repository and collection @browser-matrix", async ({ page }) => {
  const state = await collection(page);
  await page.goto("/collections/services");
  await page.getByRole("button", { name: "Scan Payments", exact: true }).click();
  await expect.poll(() => state.requests.find((r) => r.method === "POST" && r.pathname === "/scans")?.body).toMatchObject({ repo_id: "payments", collection_id: "services", branch: "main" });
});

test("collection errors do not look unscanned @browser-matrix", async ({ page }) => {
  await collection(page);
  let broken = true;
  await page.route(/\/api(?:\/v1)?\/collections\/services$/, (route) => broken
    ? route.fulfill({ status: 503, json: { error: { code: "unavailable", message: "Unavailable" } } })
    : route.fallback());
  await page.goto("/collections/services");
  await expect(page.getByRole("button", { name: "Retry collection", exact: true })).toBeVisible();
  await expect(page.getByText("Never scanned", { exact: true })).toHaveCount(0);
  broken = false;
  await page.getByRole("button", { name: "Retry collection", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Services", exact: true })).toBeVisible();
  await expect(page.getByText("Incomplete coverage", { exact: true })).toBeVisible();
});
