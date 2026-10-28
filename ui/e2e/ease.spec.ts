import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

test("ease path: home, findings, scanners, scanner updates", async ({
  page,
}) => {
  await installScannerApiMock(page);

  await page.goto("/");
  await expect(
    page.getByRole("heading", { level: 1, name: "Home" }),
  ).toBeVisible();

  await page.goto("/findings");
  await expect(page.getByRole("heading", { name: "Findings" })).toBeVisible();

  await page.goto("/settings?tab=scanners");
  await expect(
    page.getByRole("heading", { level: 1, name: "Settings" }),
  ).toBeVisible();
  if (page.viewportSize()!.width < 1024) {
    await expect(page.getByRole("combobox", { name: "Administration section" })).toHaveText("Scanners & updates");
  } else {
    await expect(page.getByRole("button", { name: "Scanners & updates", exact: true })).toHaveAttribute("aria-current", "page");
  }
  await expect(
    page.getByRole("button", { name: "Run Doctor" }),
  ).toBeVisible();

  await page.goto("/settings?tab=images");
  await expect(
    page.getByRole("button", { name: "Update Wolf images" }),
  ).toBeVisible();

  // The old "Advanced" tab now redirects into Scanners & updates → Advanced.
  await page.goto("/settings?tab=scanner-updates");
  await expect(page).toHaveURL(/tab=scanners.*advanced=true|advanced=true.*tab=scanners/);
  await expect(
    page.getByText("Notify only (do not auto-promote scanner releases)"),
  ).toBeVisible();
});
