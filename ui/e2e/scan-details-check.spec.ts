import { test, expect } from "@playwright/test";

test.describe("Scan details and collection scan history", () => {
  test("check scan results and collection scans", async ({ page }) => {
    // Login
    await page.goto("/login");
    await page.getByLabel("Email").fill("admin@wolf.local");
    await page.getByLabel("Password").fill("WolfAdmin2026");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL((url) => !url.pathname.includes("/login"), { timeout: 10000 });

    // Navigate to latest full scan
    await page.goto("/scans/6f311081-8ab1-4ee3-abb5-63fd9c50df61");
    await page.waitForTimeout(3000);
    await page.screenshot({ path: "/tmp/wolf-scan-results.png", fullPage: true });
    console.log("Scan results h1:", await page.locator("h1").textContent());

    // Click on semgrep to expand raw output
    const semgrepBtn = page.locator("button", { hasText: "semgrep" });
    if (await semgrepBtn.isVisible()) {
      await semgrepBtn.click();
      await page.waitForTimeout(2000);
      await page.screenshot({ path: "/tmp/wolf-tool-expanded.png", fullPage: true });
    }

    // Navigate to collection detail — check scan history with tool counts
    await page.goto("/collections");
    await page.waitForTimeout(1000);
    const collectionCard = page.locator("text=wolf").first();
    await collectionCard.click();
    await page.waitForTimeout(3000);
    await page.screenshot({ path: "/tmp/wolf-collection-full.png", fullPage: true });
  });
});
