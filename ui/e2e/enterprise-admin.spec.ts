// Enterprise hub and modules (docs/UI_DESIGN_PLAN.md T2.4/T2.5): grouped cards
// with entitlement state, and the records page template.
import { expect, test, type Page } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

const apiPath = (name: string) => new RegExp(`/api(?:/v1)?${name}$`);
const edition = (page: Page, entitlements: Record<string, boolean>, name = "enterprise") =>
  page.route(apiPath("/edition"), (route) => route.fulfill({ json: { data: { edition: name, product: "Wolf Enterprise", licensed: name === "enterprise", entitlements } } }));

test("hub groups every module and shows entitlement state @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await edition(page, { "enterprise.integrations": true, "enterprise.identity": true });
  // Later routes win, so the catch-all 404 goes first.
  await page.route(/\/api(?:\/v1)?\/enterprise\/.+$/, (route) => route.fulfill({ status: 404, json: { error: { code: "not_found", message: "not found" } } }));
  await page.route(/\/api(?:\/v1)?\/enterprise\/(siem|ticketing|group-mappings)$/, (route) => route.fulfill({ json: { data: [{ id: "x-1", name: "primary" }] } }));
  await page.goto("/enterprise");

  for (const group of ["Identity & access", "Governance", "Integrations", "Data", "Operations"]) {
    await expect(page.getByRole("heading", { name: group })).toBeVisible();
  }
  const siem = page.getByRole("article", { name: "SIEM" });
  await expect(siem).toContainText("Entitled");
  await expect(siem).toContainText("1 configured");
  await siem.getByRole("link", { name: "Manage SIEM" }).click();
  await expect(page).toHaveURL(/\/enterprise\/siem$/);

  await page.goto("/enterprise");
  const residency = page.getByRole("article", { name: "Residency" });
  await expect(residency).toContainText("Locked");
  await expect(residency).toContainText("is a Wolf Enterprise feature");
  await expect(residency.getByRole("link", { name: "Manage residency" })).toHaveCount(0);
});

test("SIEM: + Add destination, send test event and delete each confirm @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await edition(page, { "enterprise.integrations": true });
  const rows: { id: string; name: string; kind?: string }[] = [{ id: "d-1", name: "splunk-prod", kind: "webhook" }];
  const posts: unknown[] = [];
  const sends: string[] = [];
  const deletes: string[] = [];
  await page.route(apiPath("/enterprise/siem"), (route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as { name: string };
      posts.push(body);
      rows.push({ id: "d-2", name: body.name });
      return route.fulfill({ status: 201, json: { data: rows.at(-1) } });
    }
    return route.fulfill({ json: { data: rows } });
  });
  await page.route(/\/api(?:\/v1)?\/enterprise\/siem\/d-1\/send$/, (route) => { sends.push(route.request().method()); return route.fulfill({ json: { data: {} } }); });
  await page.route(/\/api(?:\/v1)?\/enterprise\/siem\/d-2$/, (route) => { deletes.push(route.request().method()); return route.fulfill({ json: { data: {} } }); });
  await page.goto("/enterprise/siem");

  await expect(page.getByRole("heading", { level: 1, name: "SIEM" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "splunk-prod", exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Add destination" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add destination" });
  await dialog.getByRole("button", { name: "Add destination" }).click();
  await expect(dialog.getByText("Give the record a name.")).toBeVisible();
  await dialog.getByRole("button", { name: /Additional fields/ }).click();
  await dialog.getByLabel("Fields (JSON object)").fill("{not json");
  await dialog.getByLabel("Name", { exact: true }).fill("elastic-eu");
  await dialog.getByRole("button", { name: "Add destination" }).click();
  await expect(dialog.getByText(/Must be a JSON object/)).toBeVisible();
  expect(posts).toHaveLength(0);
  await dialog.getByLabel("Fields (JSON object)").fill('{"region": "eu"}');
  await dialog.getByRole("button", { name: "Add destination" }).click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toEqual({ region: "eu", name: "elastic-eu" });
  await expect(page.getByText("Destination added")).toBeVisible();

  await page.getByRole("button", { name: "Actions for splunk-prod" }).click();
  await page.getByRole("menuitem", { name: "Send test event" }).click();
  await page.getByRole("dialog", { name: /Send a test event/ }).getByRole("button", { name: "Send test event" }).click();
  await expect.poll(() => sends).toEqual(["POST"]);
  await expect(page.getByText("Delivered")).toBeVisible();

  await page.getByRole("button", { name: "Actions for elastic-eu" }).click();
  await page.getByRole("menuitem", { name: "Delete destination" }).click();
  await page.getByRole("dialog", { name: /Delete destination/ }).getByRole("button", { name: "Delete destination" }).click();
  await expect.poll(() => deletes).toEqual(["DELETE"]);
});

test("Community shows the locked treatment, not an empty module @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await edition(page, {}, "community");
  await page.goto("/enterprise/identity");
  await expect(page.getByText("Identity is a Wolf Enterprise feature")).toBeVisible();
  await expect(page.getByRole("button", { name: /Add/ })).toHaveCount(0);
});
