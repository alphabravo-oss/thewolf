import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

test("scan search and status filter survive reload and reach the server @browser-matrix", async ({ page }) => {
  const state = await installScannerApiMock(page);
  await page.goto("/scans");
  const search = page.getByRole("textbox", { name: "Search scans, repositories, branches..." });
  await search.fill("release/next");
  await expect.poll(() => state.getRequests.some((path) => {
    const url = new URL(path, "http://wolf.test");
    return url.pathname === "/scans" && url.searchParams.get("q") === "release/next";
  })).toBe(true);
  await page.getByLabel("Scan status").selectOption("completed");
  await expect(page).toHaveURL(/status=completed/);
  await page.reload();
  await expect(search).toHaveValue("release/next");
  await expect(page.getByLabel("Scan status")).toHaveValue("completed");
  // URL state must not bounce back to stale component state after hydration.
  await search.fill("hotfix");
  await expect.poll(() => new URL(page.url()).searchParams.get("q")).toBe("hotfix");
});

test("completed scans with a failed tool show incomplete coverage @browser-matrix", async ({ page }) => {
  const state = await installScannerApiMock(page);
  Object.assign(state.apiCreatedScan, {
    status: "completed", tools_selected: '["semgrep","trivy"]',
    tools_completed: '["semgrep"]', tools_failed: '["trivy"]',
  });
  await page.goto("/scans");
  await expect(page.getByText("Incomplete coverage", { exact: true })).toBeVisible();
});

test("scan API errors offer retry without presenting an empty fleet @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  await page.route(/\/api(?:\/v1)?\/scans(?:\?|$)/, (route) => route.fulfill({
    status: 503, contentType: "application/json",
    body: JSON.stringify({ error: { code: "unavailable", message: "Temporarily unavailable" } }),
  }));
  await page.goto("/scans");
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("No scans yet", { exact: true })).toHaveCount(0);
  await expect(page.getByText("No scans match your filters", { exact: true })).toHaveCount(0);
});
