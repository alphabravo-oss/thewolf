import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { installScannerApiMock, recorded } from "./scanner-api-mock";

test("readiness failure, shared image download, and idempotent scan recovery @browser-matrix", async ({ page }) => {
  // HTTP intranets expose getRandomValues, but not randomUUID.
  await page.addInitScript(() => Object.defineProperty(window.crypto, "randomUUID", { value: undefined }));
  const state = await installScannerApiMock(page);
  let checks = 0, downloaded = false;
  const submissions: { key?: string; body: unknown }[] = [];
  await page.route(/\/api(?:\/v1)?\/scans\/preflight$/, async (route) => {
    if (checks++ === 0) return route.fulfill({ status: 503, body: "temporarily unavailable" });
    return route.fulfill({ json: { data: { missing: downloaded ? [] : [
      { tool: "gosec", image: "shared-image" }, { tool: "gokart", image: "shared-image" },
    ] } } });
  });
  await page.route(/\/api(?:\/v1)?\/scanners\/images\/pull$/, async (route) => {
    downloaded = true;
    await route.fallback();
  });
  await page.route(/\/api(?:\/v1)?\/scans$/, async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    submissions.push({ key: route.request().headers()["idempotency-key"], body: route.request().postDataJSON() });
    if (submissions.length === 1) return route.fulfill({ status: 503, json: { error: { message: "submission response unavailable" } } });
    return route.fallback();
  });
  await page.goto("/scans");
  await page.getByRole("button", { name: "New scan" }).click();
  await page.getByLabel("Repo *").selectOption("repo-api-created");
  await page.getByRole("button", { name: "Start scan", exact: true }).click();
  await page.getByRole("button", { name: "Retry readiness check" }).waitFor();
  expect(submissions).toHaveLength(0);
  await page.getByRole("button", { name: "Retry readiness check" }).click();
  await page.getByRole("button", { name: /^Download \d+ images? and scan$/ }).click();
  await page.getByRole("button", { name: "Retry scan submission" }).waitFor();
  expect(recorded(state, "POST", "/scanners/images/pull")).toHaveLength(1);
  expect(checks).toBe(3);
  const a11y = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  await page.getByRole("button", { name: "Retry scan submission" }).click();
  await expect(page).toHaveURL(/\/scans\/scan-api-created/);
  expect(submissions).toHaveLength(2);
  expect(submissions[0].key).toBeTruthy();
  expect(submissions[0]).toEqual(submissions[1]);
});

test("locally built scanner images support private registry ports without a registry digest", async ({ page }) => {
  await installScannerApiMock(page);
  await page.route(/\/api(?:\/v1)?\/scanners\/images$/, (route) => route.fulfill({ json: { data: [{
    image: "registry.example:5000/wolf-scanners:local", local_present: true, updates_available: false,
  }] } }));
  await page.goto("/settings?tab=images");
  await expect(page.getByText("Present (local build)", { exact: true })).toBeVisible();
  const wolfCard = page.getByRole("region", { name: "Wolf scanner images" });
  await expect(wolfCard.getByText("registry.example:5000/wolf-scanners:local", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Pull", exact: true })).toHaveCount(0);
});
