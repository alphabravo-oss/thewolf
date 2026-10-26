import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

test("audit uses full-history server pages and URL filters @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  const requests: URLSearchParams[] = [];
  await page.route(/\/api(?:\/v1)?\/audit-log(?:\?|$)/, (route) => {
    const params = new URL(route.request().url()).searchParams;
    requests.push(params);
    const index = Number(params.get("page") ?? 1);
    return route.fulfill({ json: {
      data: [{ id: `audit-${index}`, method: "POST", path: `/page-${index}`, action: "scan.created", event_type: "scan.created", user_id: "audit-owner", category: "data", severity: "info", ip: "192.0.2.1", status_code: 201, created_at: "2026-10-23T12:00:00Z" }],
      meta: { total: 150, page: index, per_page: 50 },
    } });
  });
  await page.goto("/settings?tab=audit");
  await expect(page).toHaveURL(/\/audit/);
  await expect(page.getByText("/page-1", { exact: true })).toBeVisible();
  await expect(page.getByText("201", { exact: true })).toHaveClass(/text-status-success/);
  await page.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(page.getByText("/page-2", { exact: true })).toBeVisible();
  await page.getByLabel("Actor ID", { exact: true }).fill("audit-owner");
  await expect(page).toHaveURL(/actor=audit-owner/);
  await expect.poll(() => requests.at(-1)?.get("actor")).toBe("audit-owner");
  expect(requests.at(-1)?.get("page")).toBe("1");
  await page.getByLabel("From (UTC)", { exact: true }).fill("2026-10-01");
  await expect.poll(() => requests.at(-1)?.get("from")).toBe("2026-10-01");
  await page.reload();
  await expect(page.getByLabel("Actor ID", { exact: true })).toHaveValue("audit-owner");
  await expect(page.getByLabel("From (UTC)", { exact: true })).toHaveValue("2026-10-01");
  await page.getByPlaceholder("Search events, paths, actors, IPs...").fill("older event");
  await expect.poll(() => requests.at(-1)?.get("q")).toBe("older event");
  await expect(page.getByRole("columnheader", { name: "Category" })).toBeVisible();
  expect(requests.every((p) => p.get("per_page") === "50" && !p.has("limit"))).toBe(true);
});

test("audit distinguishes denied, failed and empty history @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  let status = 403;
  await page.route(/\/api(?:\/v1)?\/audit-log(?:\?|$)/, (route) => route.fulfill({ status, json: status === 200 ? { data: [], meta: { total: 0 } } : { error: { code: "unavailable", message: "Audit unavailable" } } }));
  await page.goto("/audit");
  await expect(page.getByText("Admin only", { exact: true })).toBeVisible();
  status = 500;
  await page.reload();
  await expect(page.getByText("Audit unavailable", { exact: true })).toBeVisible();
  await expect(page.getByText("No audit entries match your filters", { exact: true })).toHaveCount(0);
  status = 200;
  await page.getByRole("button", { name: /retry/i }).click();
  await expect(page.getByText("No audit entries match your filters", { exact: true })).toBeVisible();
});
