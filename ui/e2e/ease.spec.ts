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
  await expect(
    page.getByRole("button", { name: "Scanners", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByRole("button", { name: "Set up scanners (pull images)" }),
  ).toBeVisible();

  await page.goto("/settings?tab=scanner-updates");
  await expect(
    page.getByRole("button", { name: "Scanner updates" }),
  ).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByText("Notify only (do not auto-promote scanner releases)"),
  ).toBeVisible();
});
