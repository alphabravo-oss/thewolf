import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installScannerApiMock } from "./scanner-api-mock";

async function scan(page: Page, initialTotal = 1234) {
  const state = await installScannerApiMock(page);
  Object.assign(state.apiCreatedScan, { status: "running", finding_count: 12 });
  const totals = { value: initialTotal };
  const queries: URLSearchParams[] = [];
  await page.route(/\/api(?:\/v1)?\/scans\/scan-api-created\/findings\/stats$/, (route) => route.fulfill({ json: { data: { total: totals.value, by_severity: { critical: totals.value ? 20 : 0, high: Math.max(0, totals.value - 20) }, by_tool: { semgrep: totals.value }, by_category: { sast: totals.value } } } }));
  await page.route(/\/api(?:\/v1)?\/scans\/scan-api-created\/findings\?/, (route) => {
    const p = new URL(route.request().url()).searchParams;
    queries.push(p);
    const pageNumber = Number(p.get("page") || 1);
    const total = totals.value === 0 ? 0 : p.get("q") ? 1 : 120;
    const size = Number(p.get("per_page"));
    return route.fulfill({ json: { data: Array.from({ length: Math.max(0, Math.min(size, total - (pageNumber - 1) * size)) }, (_, i) => ({ id: `finding-${pageNumber}-${i}`, title: p.get("q") ? "Off-page search result" : `Finding ${(pageNumber - 1) * size + i + 1}`, severity: p.get("order") === "asc" && p.get("sort") === "severity" ? "low" : "critical", tool_name: "semgrep", category: "sast", file_path: "src/main.go", line_start: i + 1 })), meta: { total, page: pageNumber, per_page: size, suppressed: 0 } } });
  });
  return { state, queries, totals };
}

test("all scan columns sort on the server and live counts ignore table filters @browser-matrix", async ({ page }, testInfo) => {
  const { queries, totals } = await scan(page);
  await page.goto("/scans/scan-api-created");
  const summary = page.getByRole("region", { name: "Scan findings summary", exact: true });
  const findings = page.getByRole("region", { name: "Scan findings", exact: true });
  await expect(summary.getByText("1,234", { exact: true })).toBeVisible();
  await expect(summary.getByRole("heading", { name: "Findings reported so far" })).toBeVisible();
  const severity = findings.getByRole("columnheader", { name: "Severity", exact: true });
  await expect(severity).toHaveAttribute("aria-sort", "descending");
  expect(queries.at(-1)?.get("sort")).toBe("severity");
  expect(queries.at(-1)?.get("order")).toBe("desc");
  await severity.press("Enter");
  await expect.poll(() => queries.at(-1)?.get("order")).toBe("asc");
  await expect(severity).toHaveAttribute("aria-sort", "ascending");
  await severity.press("Space");
  await expect.poll(() => queries.at(-1)?.get("order")).toBe("desc");
  for (const [label, field] of [["Tool", "tool_name"], ["Title", "title"], ["File", "file_path"], ["Line", "line_start"]]) {
    await findings.getByRole("button", { name: "Next page", exact: true }).click();
    await expect.poll(() => queries.at(-1)?.get("page")).toBe("2");
    const header = findings.getByRole("columnheader", { name: label, exact: true });
    await header.press("Enter");
    await expect.poll(() => [queries.at(-1)?.get("sort"), queries.at(-1)?.get("order"), queries.at(-1)?.get("page")]).toEqual([field, "asc", "1"]);
    await header.press("Enter");
    await expect.poll(() => queries.at(-1)?.get("order")).toBe("desc");
  }
  await findings.getByRole("textbox", { name: "Search findings" }).fill("off-page");
  await expect(findings.getByRole("link", { name: "Off-page search result", exact: true })).toBeVisible();
  await expect(findings.getByRole("row")).toHaveCount(2);
  await expect(summary.getByText("1,234", { exact: true })).toBeVisible();
  totals.value = 2345;
  await expect(summary.getByText("2,345", { exact: true })).toBeVisible();
  await expect(findings.getByRole("columnheader", { name: "Line", exact: true })).toHaveAttribute("aria-sort", "descending");
  expect(queries.at(-1)?.get("q")).toBe("off-page");
  const a11y = await new AxeBuilder({ page }).include("#main-content").withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("scan-live-counts.png"), fullPage: true });
});

test("a running scan with no findings does not claim completion @browser-matrix", async ({ page }) => {
  await scan(page, 0);
  await page.goto("/scans/scan-api-created");
  await expect(page.getByText("Waiting for findings", { exact: true })).toBeVisible();
  await expect(page.getByText(/completed without any issues/)).toHaveCount(0);
});

test("failed scan findings request offers retry", async ({ page }) => {
  await scan(page);
  await page.route(/\/api(?:\/v1)?\/scans\/scan-api-created\/findings\?/, (route) => route.fulfill({ status: 503, json: { error: { code: "unavailable", message: "Unavailable" } } }));
  await page.goto("/scans/scan-api-created");
  await expect(page.getByRole("button", { name: "Retry findings", exact: true })).toBeVisible();
  await expect(page.getByText("No findings reported", { exact: true })).toHaveCount(0);
});

test("partial scanner results are distinguished from a scanner crash @browser-matrix", async ({ page }) => {
  const { state } = await scan(page);
  state.apiCreatedScan.tools_failed = '["semgrep","gokart"]';
  await page.route(/\/api(?:\/v1)?\/scans\/scan-api-created\/tools$/, route => route.fulfill({ json: { data: [
    {name:"semgrep",status:"failed",finding_count:150,raw_count:156,error:"197 analysis warnings; incomplete coverage"},
    {name:"gokart",status:"failed",finding_count:0,error:"gokart execution failed: exit status 2"},
  ] } }));
  await page.goto("/scans/scan-api-created");
  await expect(page.getByText("1 partial", {exact:true})).toBeVisible();
  await expect(page.getByText("1 failed", {exact:true})).toBeVisible();
  await expect(page.getByText("156 findings retained · incomplete coverage", {exact:true}).filter({ visible: true })).toBeVisible();
  await page.getByText("Tools (2)", { exact: true }).click();
  await page.getByRole("button", {name:"Show details for semgrep"}).click();
  await expect(page.locator("pre").filter({hasText:"197 analysis warnings; incomplete coverage"})).toBeVisible();
});
