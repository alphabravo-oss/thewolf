import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

test("automatic scanner capacity is visible and manual limits can return to auto @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  let settings: Record<string, string> = { scan_concurrency: "2" };
  await page.route(/\/api(?:\/v1)?\/settings$/, async (route) => {
    if (route.request().method() === "PUT") settings = { ...settings, ...route.request().postDataJSON() };
    await route.fulfill({ json: { data: settings } });
  });
  await page.route(/\/api(?:\/v1)?\/settings\/scheduling$/, async (route) => route.fulfill({ json: { data: {
    concurrency: settings.scan_concurrency === "0" ? 6 : 2, heavy_concurrency: 3, network_concurrency: 4,
    host_cpus: 16, host_memory_bytes: 30 * 2 ** 30, cpu_budget: 12,
    memory_budget_bytes: 22.5 * 2 ** 30, workers: 1, source: "Docker host",
  } } }));
  await page.goto("/settings?tab=general");
  const scheduling = page.getByRole("region", { name: "Scanner scheduling" });
  await expect(scheduling).toContainText("16 CPUs");
  await expect(scheduling).toContainText("Up to 2 tools per scan");
  const limit = page.getByRole("spinbutton", { name: "Scan concurrency", exact: true });
  await limit.fill("0");
  await limit.locator("..").getByRole("button", { name: "Save", exact: true }).click();
  await expect(scheduling).toContainText("Up to 6 tools per scan");
  await page.reload();
  await expect(limit).toHaveValue("0");
  await expect(page.getByRole("spinbutton", { name: "Heavy tool concurrency" })).toHaveValue("0");
});
