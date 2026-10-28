import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installScannerApiMock } from "./scanner-api-mock";
import { installRichFixtures, richPosture } from "./visual-fixtures";

const CHARTS = [
  ["chart-trend", "Observed open findings (daily)"],
  ["chart-changes", "New vs resolved this week"],
  ["chart-age", "Age of open critical / high"],
  ["chart-repos", "Riskiest repositories"],
  ["chart-rules", "Top rules"],
  ["chart-scanners", "Scanner health"],
  ["chart-fixes", "Fix pipeline"],
] as const;

/** Shared mock + rich fixtures (posture, trends, by-repo, rules, scans, fixes); Enterprise turns the fix funnel on. */
async function dashboard(page: Page, edition: "enterprise" | "community" = "enterprise") {
  const state = await installScannerApiMock(page);
  await installRichFixtures(page);
  await page.route(/\/api(?:\/v1)?\/edition$/, (route) => route.fulfill({ json: { data: { edition, product: edition === "enterprise" ? "Wolf Enterprise" : "Wolf Community", licensed: edition === "enterprise", ui_routes: [], entitlements: { "enterprise.remediation": edition === "enterprise" } } } }));
  return state;
}

const bars = (page: Page, chart: string) => page.locator(`#${chart} .recharts-bar-rectangle`);
const allParams = (path: string, params: Record<string, string>) => new RegExp(`${path}\\?${Object.entries(params).map(([k, v]) => `(?=.*${k}=${v})`).join("")}`);

test("home renders every chart from fixtures with text alternatives @browser-matrix", async ({ page }, testInfo) => {
  await dashboard(page);
  await page.goto("/");
  for (const [id, title] of CHARTS) {
    const card = page.locator(`#${id}`);
    await expect(card.getByRole("heading", { name: title, exact: true })).toBeVisible();
    await expect(bars(page, id).first()).toBeVisible();
    await expect(card.getByRole("button", { name: "Data table" })).toBeVisible();
  }
  for (const title of ["Open critical / high", "New vs resolved this week", "Scan coverage", "Gates failing", "Fix branches pushed"]) {
    await expect(page.getByRole("link", { name: new RegExp(`^${title}`) })).toBeVisible();
  }
  // KPI change is critical + high vs seven days earlier (1400 vs 1500 in TRENDS), not the medium-heavy total.
  await expect(page.getByRole("link", { name: /^Open critical \/ high/ })).toContainText("-100 vs 7 days ago");
  // Thousands render as compact ticks (8K), so labels fit the axis.
  // (Recharts may portal axis layers, so read every tick: dates are MM-DD, counts compact.)
  const ticks = page.locator("#chart-trend .recharts-cartesian-axis-tick-value");
  await expect(ticks.filter({ hasText: /^\d+(\.\d)?K$/ }).first()).toBeVisible();
  expect((await ticks.allTextContents()).every((t) => t.length <= 5)).toBe(true);
  await expect(page.getByRole("heading", { name: "Act on now" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Hard-coded credential in config" })).toHaveAttribute("href", /\/findings\/finding-0$/);
  await expect(page.getByRole("button", { name: "Are we improving?" })).toHaveAttribute("aria-expanded", "false");
  // The 30/90-day toggle widens the plotted window.
  const thirty = await bars(page, "chart-trend").count();
  await page.getByRole("button", { name: "90 days" }).click();
  await expect.poll(() => bars(page, "chart-trend").count()).toBeGreaterThan(thirty);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const a11y = await new AxeBuilder({ page }).include("#main-content").withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("dashboard.png"), fullPage: true });
});

test("chart marks drill down to filtered lists @browser-matrix", async ({ page }) => {
  await dashboard(page);
  const drill = async (chart: string, url: RegExp) => {
    await page.goto("/");
    await bars(page, chart).first().click();
    await expect(page).toHaveURL(url);
  };
  // First segment of each stacked chart is its critical series.
  await drill("chart-repos", allParams("/repos/payments-api", { scope: "all", severity: "critical" }));
  await drill("chart-trend", allParams("/findings", { view: "list", scope: "all", severity: "critical" }));
  await drill("chart-age", allParams("/findings", { view: "list", scope: "all", severity: "critical", sort: "first_seen", order: "asc" }));
  await drill("chart-rules", allParams("/findings", { view: "rule", q: "CVE-2024-4067" }));
  await expect(page.getByText("Regular expression denial of service in micromatch")).toBeVisible();
  await expect(page.getByText("AWS access key committed")).toHaveCount(0);
  await drill("chart-changes", /\/scans\/[0-9a-f]{8}-[a-z]{4}$/);
  await drill("chart-scanners", /\/coverage$/);
  await drill("chart-fixes", /\/agents\?stage=queued$/);
  await expect(page.getByText(/Showing jobs at stage/)).toBeVisible();
});

test("chart data tables expose keyboard drill-down links", async ({ page }) => {
  await dashboard(page);
  await page.goto("/");
  const age = page.locator("#chart-age");
  await age.getByRole("button", { name: "Data table" }).focus();
  await page.keyboard.press("Enter");
  await age.getByRole("table").getByRole("link").first().focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(allParams("/findings", { scope: "all", severity: "critical", sort: "first_seen", order: "asc" }));
  await expect(page.getByRole("combobox", { name: "Filter by severity" })).toHaveText("critical");
});

test("community hides the fix funnel; missing comparisons stay unknown @browser-matrix", async ({ page }) => {
  await dashboard(page, "community");
  const data = { ...richPosture(), changes: { window_days: 7, compared_branches: 0, excluded_branches: 3, new: 0, resurfaced: 0, resolved: 0, suppressed: 0, rows: [] } };
  await page.route(/\/api(?:\/v1)?\/fleet\/posture(?:\?|$)/, (route) => route.fulfill({ json: { data } }));
  await page.goto("/");
  await expect(page.locator("#chart-changes").getByText(/Progress is unknown/)).toBeVisible();
  await expect(page.locator("#chart-fixes")).toHaveCount(0);
  await expect(page.getByRole("link", { name: /^Fix branches pushed/ })).toHaveCount(0);
  await page.getByRole("link", { name: /^New vs resolved this week/ }).click();
  const progress = page.getByRole("region", { name: "Are we improving?" });
  await expect(progress.getByRole("link", { name: "Verified resolved —", exact: true })).toBeVisible();
});

test("a failing card shows its own error and recovers on retry @browser-matrix", async ({ page }) => {
  await dashboard(page);
  let broken = true;
  await page.route(/\/api(?:\/v1)?\/findings\/by-repo$/, (route) => broken ? route.fulfill({ status: 500, json: { error: { code: "server_error", message: "failed" } } }) : route.fallback());
  await page.goto("/");
  const repos = page.locator("#chart-repos");
  await expect(repos.getByText("Riskiest repositories could not be loaded.")).toBeVisible({ timeout: 20_000 });
  await expect(bars(page, "chart-rules").first()).toBeVisible();
  broken = false;
  await repos.getByRole("button", { name: "Retry" }).click();
  await expect(bars(page, "chart-repos").first()).toBeVisible();
});

test("dashboard read errors remain unknown and can be retried @browser-matrix", async ({ page }) => {
  await dashboard(page);
  let broken = true;
  await page.route(/\/api(?:\/v1)?\/fleet\/posture(?:\?|$)/, (route) => broken
    ? route.fulfill({ status: 503, json: { error: { code: "unavailable", message: "Unavailable" } } })
    : route.fallback());
  await page.goto("/");
  await expect(page.getByText("Dashboard evidence could not be loaded. Coverage and risk are unknown.")).toBeVisible({ timeout: 20_000 });
  // Charts that do not depend on posture still render.
  await expect(bars(page, "chart-repos").first()).toBeVisible();
  broken = false;
  await page.getByRole("button", { name: "Retry dashboard", exact: true }).click();
  await expect(page.getByRole("link", { name: "Hard-coded credential in config" })).toBeVisible();
});

test("non-admin dashboard never requests the audit log @browser-matrix", async ({ page }) => {
  const state = await dashboard(page);
  state.currentUser.role = "user";
  state.currentUser.scopes = [];
  let auditRequests = 0;
  await page.route(/\/api(?:\/v1)?\/audit-log/, (route) => { auditRequests++; return route.fulfill({ json: { data: [] } }); });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Act on now" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recent activity" })).toHaveCount(0);
  expect(auditRequests).toBe(0);
});
