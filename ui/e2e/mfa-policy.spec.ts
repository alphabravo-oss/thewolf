import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

const unavailable = {
  status: 503,
  contentType: "application/json",
  body: JSON.stringify({ error: { code: "mfa_policy_unavailable", message: "MFA policy is unavailable; retry shortly" } }),
};

test("MFA status failures show unknown state and retry safely @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  let available = false;
  await page.route("**/auth/mfa/status", (route) => route.fulfill(available ? {
    contentType: "application/json",
    body: JSON.stringify({ data: { enabled: true, required: true } }),
  } : unavailable));
  await page.goto("/account?section=security");
  await expect(page.getByRole("alert").filter({ hasText: "Two-factor status unavailable" })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("Off", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Set up two-factor authentication", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Disable", exact: true })).toHaveCount(0);
  available = true;
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByText("On", { exact: true })).toBeVisible();
  await expect(page.getByText(/Your administrator requires two-factor authentication/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Disable", exact: true })).toHaveCount(0);
});

test("MFA status refresh failures preserve one-time recovery codes @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  let activated = false;
  let failStatus = false;
  await page.route("**/auth/mfa/status", (route) => route.fulfill(failStatus ? unavailable : {
    contentType: "application/json",
    body: JSON.stringify({ data: { enabled: activated, required: false } }),
  }));
  await page.route("**/auth/mfa/setup", (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ data: {
      secret: "MOCKENROLLMENTSECRET", otpauth_uri: "otpauth://totp/test",
      qr: "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='1' height='1'/%3E",
    } }),
  }));
  await page.route("**/auth/mfa/activate", (route) => {
    activated = true;
    failStatus = true;
    return route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ data: { recovery_codes: ["abcd-efgh", "ijkl-mnop"] } }),
    });
  });
  await page.goto("/account?section=security");
  await page.getByRole("button", { name: "Set up two-factor authentication", exact: true }).click();
  await page.getByPlaceholder("123456", { exact: true }).fill("123456");
  await page.getByRole("button", { name: "Activate", exact: true }).click();
  await expect(page.getByText("Save your recovery codes", { exact: true })).toBeVisible();
  await expect(page.getByRole("alert").filter({ hasText: "Two-factor status unavailable" })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("abcd-efgh", { exact: true })).toBeVisible();
  await expect(page.getByText("ijkl-mnop", { exact: true })).toBeVisible();
  await expect(page.getByText("Off", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Set up two-factor authentication", exact: true })).toHaveCount(0);
  failStatus = false;
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByText("On", { exact: true })).toBeVisible();
  await expect(page.getByText("abcd-efgh", { exact: true })).toBeVisible();
});
