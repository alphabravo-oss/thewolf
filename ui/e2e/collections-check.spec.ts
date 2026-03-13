import { test, expect } from "@playwright/test";

test.describe("Collections page check", () => {
  test("login and check collections page", async ({ page }) => {
    // Login
    await page.goto("/login");
    await page.getByLabel("Email").fill("admin@wolf.local");
    await page.getByLabel("Password").fill("WolfAdmin2026");
    await page.getByRole("button", { name: "Sign in" }).click();

    // Wait for redirect after login
    await page.waitForURL((url) => !url.pathname.includes("/login"), {
      timeout: 10000,
    });

    // Take screenshot of where we end up
    await page.screenshot({
      path: "/tmp/wolf-after-login.png",
      fullPage: true,
    });

    // Check what URL we're on
    const url = page.url();
    console.log("After login URL:", url);

    // Navigate to collections (currently at /)
    await page.goto("/");
    await page.waitForTimeout(2000);

    await page.screenshot({
      path: "/tmp/wolf-collections.png",
      fullPage: true,
    });

    // Check for sidebar nav items
    const sidebarItems = await page
      .locator("[data-sidebar]")
      .textContent()
      .catch(() => "no sidebar found");
    console.log("Sidebar content:", sidebarItems?.substring(0, 500));

    // Check page content
    const mainContent = await page
      .locator("main")
      .textContent()
      .catch(() => "no main found");
    console.log(
      "Main content (first 500):",
      mainContent?.substring(0, 500)
    );

    // Check for any error messages
    const errors = await page
      .locator(".text-red-500, .text-destructive, [role='alert']")
      .allTextContents();
    if (errors.length > 0) {
      console.log("Errors found:", errors);
    }

    // Check console errors
    const consoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") {
        consoleErrors.push(msg.text());
      }
    });

    // Navigate through key pages and screenshot
    const pages = ["/scans", "/findings", "/settings"];
    for (const p of pages) {
      await page.goto(p);
      await page.waitForTimeout(1000);
      const name = p.replace("/", "") || "home";
      await page.screenshot({
        path: `/tmp/wolf-${name}.png`,
        fullPage: true,
      });
      console.log(`Page ${p}:`, page.url());
    }

    if (consoleErrors.length > 0) {
      console.log("Console errors:", consoleErrors);
    }
  });
});
