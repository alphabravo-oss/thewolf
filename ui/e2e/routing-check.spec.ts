import { test, expect } from "@playwright/test";

test.describe("Routing and dashboard check", () => {
  test("login and verify routing structure", async ({ page }) => {
    // Login
    await page.goto("/login");
    await page.getByLabel("Email").fill("admin@wolf.local");
    await page.getByLabel("Password").fill("WolfAdmin2026");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL((url) => !url.pathname.includes("/login"), {
      timeout: 10000,
    });

    // Should land on dashboard at /
    await page.goto("/");
    await page.waitForTimeout(2000);
    await page.screenshot({ path: "/tmp/wolf-dashboard.png", fullPage: true });
    const dashboardH1 = await page.locator("h1").textContent();
    console.log("Dashboard h1:", dashboardH1);

    // Check sidebar has Dashboard and Collections links
    const sidebarText = await page.locator("nav, [data-sidebar]").first().textContent().catch(() => "no sidebar");
    console.log("Sidebar:", sidebarText?.substring(0, 300));

    // Navigate to /collections
    await page.goto("/collections");
    await page.waitForTimeout(2000);
    await page.screenshot({ path: "/tmp/wolf-collections-page.png", fullPage: true });
    const collectionsH1 = await page.locator("h1").textContent();
    console.log("Collections h1:", collectionsH1);

    // Navigate to a collection detail if one exists
    const collectionLink = page.locator('a[href*="/collections/"]').first();
    if (await collectionLink.isVisible()) {
      await collectionLink.click();
      await page.waitForTimeout(2000);
      await page.screenshot({ path: "/tmp/wolf-collection-detail.png", fullPage: true });
      const detailH1 = await page.locator("h1").textContent();
      console.log("Collection detail h1:", detailH1);
    }

    // Check other pages
    for (const p of ["/scans", "/findings", "/settings"]) {
      await page.goto(p);
      await page.waitForTimeout(1000);
      const name = p.replace("/", "");
      await page.screenshot({ path: `/tmp/wolf-${name}.png`, fullPage: true });
      console.log(`Page ${p}: h1 =`, await page.locator("h1").textContent().catch(() => "none"));
    }
  });
});
