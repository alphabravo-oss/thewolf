import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installScannerApiMock } from "./scanner-api-mock";

async function repository(page: Page) {
  const state = await installScannerApiMock(page);
  const repo = { id: "payments", user_id: "user-admin", name: "Payments", source_type: "github", source_path: "acme/payments", default_branch: "main", detected_languages: '{}', detected_frameworks: '[]', created_at: "2026-10-01T12:00:00Z" };
  const scan = { id: "latest-partial", repo_id: repo.id, branch: "main", status: "completed", tools_selected: '["semgrep","trivy"]', tools_completed: '["semgrep"]', tools_failed: '["trivy"]', finding_count: 137, created_at: "2026-10-13T10:00:00Z" };
  await page.route(/\/api(?:\/v1)?\/repos\/payments$/, (route) => route.fulfill({ json: { data: repo } }));
  await page.route(/\/api(?:\/v1)?\/repos\/payments\/branches$/, (route) => route.fulfill({ json: { data: { branches: ["main", "release/a-very-long-branch-name-that-should-stay-inside-the-dialog"], default_branch: "main" } } }));
  await page.route(/\/api(?:\/v1)?\/scans(?:\?|$)/, (route) => route.fulfill({ json: { data: [scan], meta: { total: 1 } } }));
  await page.route(/\/api(?:\/v1)?\/scans\/trends(?:\?|$)/, (route) => route.fulfill({ json: { data: [] } }));
  await page.route(/\/api(?:\/v1)?\/suppressions(?:\?|$)/, (route) => route.fulfill({ json: { data: [] } }));
  const queries: URLSearchParams[] = [];
  await page.route(/\/api(?:\/v1)?\/findings(?:\?|$)/, (route) => {
    const params = new URL(route.request().url()).searchParams;
    queries.push(params);
    const pageNumber = Number(params.get("page"));
    const size = Number(params.get("per_page"));
    const total = params.get("q") ? 3 : params.get("severity") ? 55 : 137;
    const data = Array.from({ length: Math.max(0, Math.min(size, total - (pageNumber - 1) * size)) }, (_, i) => ({ id: `finding-${(pageNumber - 1) * size + i + 1}`, title: `Finding ${(pageNumber - 1) * size + i + 1}`, severity: params.get("severity") || "high", tool_name: "semgrep", file_path: "src/a/very/long/path/that/should/not/make/the/page/overflow/payment.ts", line_start: i + 1 }));
    return route.fulfill({ json: { data, meta: { total, page: pageNumber, per_page: size } } });
  });
  return { state, queries };
}

test("repository findings page, filter and sort on the server @browser-matrix", async ({ page }, testInfo) => {
  const { queries } = await repository(page);
  await page.goto("/repos/payments?collection_id=services");
  await expect(page.getByRole("heading", { name: "Payments", exact: true })).toBeVisible();
  await expect(page.getByText("Incomplete coverage", { exact: true })).toBeVisible();
  await expect(page.getByText(/1 of 2 tools completed · 1 failed/)).toBeVisible();
  await expect(page.getByText(/In GitHub → Settings/)).toHaveCount(0);
  const findings = page.getByRole("region", { name: "Current findings", exact: true });
  await expect(findings.getByRole("row")).toHaveCount(26);
  await expect(findings.getByText("Showing 1-25 of 137")).toBeVisible();
  await findings.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(findings.getByRole("link", { name: "Finding 26", exact: true })).toBeVisible();
  expect(queries.at(-1)?.get("page")).toBe("2");
  expect(queries.at(-1)?.get("repo_id")).toBe("payments");
  expect(queries.at(-1)?.get("status")).toBe("open");
  await findings.getByRole("columnheader", { name: "Finding", exact: true }).press("Enter");
  await expect.poll(() => queries.at(-1)?.get("sort")).toBe("title");
  expect(queries.at(-1)?.get("order")).toBe("asc");
  expect(queries.at(-1)?.get("page")).toBe("1");
  await findings.getByRole("combobox", { name: "Filter by severity" }).click();
  await page.getByRole("option", { name: "Critical", exact: true }).click();
  await expect.poll(() => queries.at(-1)?.get("severity")).toBe("critical");
  await findings.getByRole("combobox", { name: "Findings per page" }).click();
  await page.getByRole("option", { name: "50 per page", exact: true }).click();
  await expect(findings.getByRole("row")).toHaveCount(51);
  await findings.getByRole("textbox", { name: "Search findings, files, or scanners…" }).fill("payment");
  await expect(findings.getByRole("row")).toHaveCount(4);
  expect(queries.at(-1)?.get("q")).toBe("payment");
  await page.reload();
  await expect(findings.getByRole("textbox")).toHaveValue("payment");
  await expect(findings.getByRole("combobox", { name: "Filter by severity" })).toHaveText("Critical");
  await expect(page.getByRole("link", { name: "Back to collection" })).toHaveAttribute("href", "/collections/services");
  await findings.getByRole("textbox").fill("");
  await findings.getByRole("combobox", { name: "Findings per page" }).click();
  await page.getByRole("option", { name: "25 per page", exact: true }).click();
  await expect(findings.getByRole("row")).toHaveCount(26);
  const a11y = await new AxeBuilder({ page }).include("#main-content").withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  for (const control of await findings.locator("input, select, button").all()) {
    const box = await control.boundingBox();
    if (box) expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  }
  expect((await findings.getByRole("textbox").boundingBox())!.width).toBeGreaterThanOrEqual(250);
  await page.screenshot({ path: testInfo.outputPath("repository-findings.png"), fullPage: true });
});

test("repository setup and scan controls stay in their own views @browser-matrix", async ({ page }) => {
  const { state } = await repository(page);
  await page.goto("/repos/payments?collection_id=services");
  await page.getByRole("tab", { name: "Settings", exact: true }).click();
  await expect(page.getByRole("heading", { name: "CI: new findings and secrets" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Schedule", exact: true })).toBeVisible();
  await expect(page.getByText("In GitHub → Settings → Webhooks", { exact: false })).toBeVisible();
  await expect(page.getByLabel("CI snippet")).toContainText("wolf scan gate");
  await expect(page.getByRole("link", { name: "Settings → Scanners → Advanced" })).toHaveAttribute("href", /advanced=true/);
  await expect(page.getByRole("region", { name: "Current findings" })).toHaveCount(0);
  await page.getByRole("tab", { name: "Scan history" }).click();
  await expect(page.getByRole("region", { name: "Scan history" }).getByRole("table")).toBeVisible();
  await page.getByRole("button", { name: "Scan repository" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("combobox", { name: "Branch" })).toHaveText("main");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const a11y = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  // Let the shared mock handle the scan POST (the history override only returns GET fixtures).
  await page.unroute(/\/api(?:\/v1)?\/scans(?:\?|$)/);
  await dialog.getByRole("button", { name: "Start scan", exact: true }).click();
  await expect.poll(() => state.requests.find((r) => r.method === "POST" && r.pathname === "/scans")?.body).toMatchObject({ repo_id: "payments", collection_id: "services", branch: "main", profile: "standard" });
});

test("repository findings failure does not appear as an empty result", async ({ page }) => {
  await repository(page);
  await page.route(/\/api(?:\/v1)?\/findings(?:\?|$)/, (route) => route.fulfill({ status: 503, json: { error: { code: "unavailable", message: "Unavailable" } } }));
  await page.goto("/repos/payments");
  const findings = page.getByRole("region", { name: "Current findings" });
  await expect(findings.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(findings.getByText("No open findings match these filters.")).toHaveCount(0);
});
