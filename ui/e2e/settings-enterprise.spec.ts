// Enterprise capabilities live in Settings (docs/UI_DESIGN_PLAN.md Phase 4):
// SSO under Users & access, SIEM/ticketing under Integrations, entitlements and
// diagnostics under License & support; old /enterprise links redirect.
import { expect, test, type Page } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

const apiPath = (name: string) => new RegExp(`/api(?:/v1)?${name}$`);
const setup = async (page: Page, entitlements: Record<string, boolean>, name = "enterprise") => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await page.route(apiPath("/edition"), (route) =>
    route.fulfill({ json: { data: { edition: name, product: name === "enterprise" ? "Wolf Enterprise" : "Wolf Community", licensed: name === "enterprise", entitlements } } }));
};

test("SSO is gated in Community and lists providers in Enterprise @browser-matrix", async ({ page }) => {
  await setup(page, {}, "community");
  await page.goto("/settings?tab=sso");
  await expect(page.getByText("Single sign-on is a Wolf Enterprise feature")).toBeVisible();
  await expect(page.getByRole("region", { name: "Sign-in providers" })).toHaveCount(0);

  await page.unrouteAll({ behavior: "ignoreErrors" });
  await setup(page, { "enterprise.identity": true });
  await page.route(apiPath("/auth/providers"), (route) => route.fulfill({ json: { data: { providers: [{ name: "okta", kind: "redirect" }], local_break_glass: true } } }));
  await page.reload();
  const providers = page.getByRole("region", { name: "Sign-in providers" });
  await expect(providers.getByRole("cell", { name: "okta", exact: true })).toBeVisible();
  await providers.getByRole("button", { name: "Actions for okta" }).click();
  await expect(page.getByRole("menuitem", { name: "Test sign-in" })).toBeVisible();
});

test("Integrations: + Add destination validates, posts, sends a test and deletes behind confirms @browser-matrix", async ({ page }) => {
  await setup(page, { "enterprise.integrations": true });
  const rows: { id: string; name: string; url: string }[] = [{ id: "d-1", name: "splunk-prod", url: "https://splunk.example.com/hec" }];
  const posts: unknown[] = [];
  const sends: unknown[] = [];
  const deletes: string[] = [];
  await page.route(apiPath("/enterprise/ticketing"), (route) => route.fulfill({ json: { data: [] } }));
  await page.route(apiPath("/enterprise/siem"), (route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as { name: string; url: string };
      posts.push(body);
      rows.push({ id: "d-2", ...body });
      return route.fulfill({ status: 201, json: { data: rows.at(-1) } });
    }
    return route.fulfill({ json: { data: rows } });
  });
  await page.route(apiPath("/enterprise/siem/d-1/send"), (route) => { sends.push(route.request().postDataJSON()); return route.fulfill({ json: { data: { status: 200 } } }); });
  await page.route(apiPath("/enterprise/siem/d-2"), (route) => { deletes.push(route.request().method()); return route.fulfill({ status: 204, body: "" }); });
  await page.goto("/settings?tab=integrations");

  const siem = page.getByRole("region", { name: "SIEM" });
  await expect(siem.getByRole("cell", { name: "splunk-prod", exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Ticketing" }).getByText("No destinations yet")).toBeVisible();

  await siem.getByRole("button", { name: "Add destination" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add SIEM destination" });
  await dialog.getByRole("button", { name: "Add destination" }).click();
  await expect(dialog.getByText("Give the destination a name.")).toBeVisible();
  await dialog.getByLabel("Name", { exact: true }).fill("elastic-eu");
  await dialog.getByLabel("URL", { exact: true }).fill("ftp://nope");
  await dialog.getByRole("button", { name: "Add destination" }).click();
  await expect(dialog.getByText("Enter an http(s) URL.")).toBeVisible();
  expect(posts).toHaveLength(0);
  await dialog.getByLabel("URL", { exact: true }).fill("https://elastic.example.com/in");
  await dialog.getByRole("button", { name: "Add destination" }).click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toEqual({ name: "elastic-eu", url: "https://elastic.example.com/in" });
  await expect(page.getByText("Destination added")).toBeVisible();

  await siem.getByRole("button", { name: "Actions for splunk-prod" }).click();
  await page.getByRole("menuitem", { name: "Send test" }).click();
  await page.getByRole("dialog", { name: /Send a test event/ }).getByRole("button", { name: "Send test" }).click();
  await expect.poll(() => sends).toEqual([{ event: "wolf.test" }]);
  await expect(page.getByText("Delivered (HTTP 200)")).toBeVisible();

  await siem.getByRole("button", { name: "Actions for elastic-eu" }).click();
  await page.getByRole("menuitem", { name: "Delete destination" }).click();
  await page.getByRole("dialog", { name: /Delete destination/ }).getByRole("button", { name: "Delete destination" }).click();
  await expect.poll(() => deletes).toEqual(["DELETE"]);
});

test("License & support lists what is included and downloads diagnostics @browser-matrix", async ({ page }) => {
  await setup(page, { "enterprise.identity": true, "enterprise.support": true, "enterprise.integrations": false });
  await page.route(apiPath("/license"), (route) => route.fulfill({ json: { data: { edition: "enterprise", product: "Wolf Enterprise", commercial_license: false, reason: "license blob is empty", data_intact: true } } }));
  await page.route(apiPath("/enterprise/diagnostics"), (route) => route.fulfill({ json: { data: { edition: "enterprise", go: "go1.25" } } }));
  await page.goto("/settings?tab=license");
  // The Enterprise binary is the entitlement; no license file is installed (as live).
  await expect(page.getByText(/^Licensed\. This Enterprise build/)).toBeVisible();
  await expect(page.getByText(/No commercial license/)).toHaveCount(0);

  const included = page.getByRole("region", { name: "What's included" });
  await expect(included.getByText("Single sign-on (OIDC, SAML, LDAP) and SCIM directory sync (included)")).toBeVisible();
  await expect(included.getByText("SIEM and ticketing destinations (not included)")).toBeVisible();
  await expect(included).not.toContainText("enterprise.");

  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download diagnostics bundle" }).click();
  expect((await download).suggestedFilename()).toMatch(/^wolf-diagnostics-.*\.json$/);
  await expect(page.getByRole("link", { name: "Contact AlphaBravo" })).toBeVisible();
});

test("old /enterprise links redirect to the matching Settings section", async ({ page }) => {
  await setup(page, {});
  for (const [from, tab] of [
    ["/enterprise", "license"],
    ["/enterprise/identity", "sso"],
    ["/enterprise/siem", "integrations"],
    ["/enterprise/ticketing", "integrations"],
    ["/enterprise/integrations", "integrations"],
    ["/enterprise/diagnostics", "license"],
    ["/enterprise/attack-paths", "license"],
  ]) {
    await page.goto(from);
    await expect(page).toHaveURL(new RegExp(`/settings\\?tab=${tab}$`));
  }
  await expect(page.getByRole("navigation", { name: "Primary" }).getByText("Enterprise")).toHaveCount(0);
});
