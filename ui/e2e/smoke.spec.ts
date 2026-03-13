import { test, expect } from "@playwright/test";

test.describe("Smoke tests", () => {
  test("login page renders", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "The Wolf" })).toBeVisible();
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("register page renders", async ({ page }) => {
    await page.goto("/register");
    await expect(
      page.getByRole("heading", { name: "Create Account" })
    ).toBeVisible();
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Create Account" })
    ).toBeVisible();
  });

  test("login page has link to register", async ({ page }) => {
    await page.goto("/login");
    const registerLink = page.getByRole("link", { name: "Register" });
    await expect(registerLink).toBeVisible();
    await registerLink.click();
    await expect(page).toHaveURL("/register");
  });

  test("register page has link to sign in", async ({ page }) => {
    await page.goto("/register");
    const loginLink = page.getByRole("link", { name: "Sign in" });
    await expect(loginLink).toBeVisible();
    await loginLink.click();
    await expect(page).toHaveURL("/login");
  });

  test("collections page loads with sidebar navigation", async ({ page }) => {
    await page.goto("/");
    // The sidebar should have navigation items
    await expect(page.getByText("Collections")).toBeVisible();
    await expect(page.getByText("Repos")).toBeVisible();
    await expect(page.getByText("Scans")).toBeVisible();
    await expect(page.getByText("Findings")).toBeVisible();
    await expect(page.getByText("Fixes")).toBeVisible();
    await expect(page.getByText("Loops")).toBeVisible();
  });

  test("navigation to scans page works", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("link", { name: /Scans/ }).first().click();
    await expect(page).toHaveURL("/scans");
    await expect(page.getByRole("heading", { name: "Scans" })).toBeVisible();
  });

  test("navigation to findings page works", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("link", { name: /Findings/ }).first().click();
    await expect(page).toHaveURL("/findings");
    await expect(
      page.getByRole("heading", { name: "Findings" })
    ).toBeVisible();
  });

  test("navigation to settings page works", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("link", { name: /Settings/ }).first().click();
    await expect(page).toHaveURL("/settings");
    await expect(
      page.getByRole("heading", { name: "Settings" })
    ).toBeVisible();
  });

  test("navigation to repos page works", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("link", { name: /Repos/ }).first().click();
    await expect(page).toHaveURL("/repos");
    await expect(
      page.getByRole("heading", { name: "Repositories" })
    ).toBeVisible();
  });
});
