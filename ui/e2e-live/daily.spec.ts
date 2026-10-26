import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { addSample, navigate, screenshot, signIn, theme, waitForScan } from "./helpers";

test("Daily: repository → filtered findings → triage → audit, accessible throughout", async ({ page }, info) => {
  async function accessible(name: string) {
    await test.step(`Accessibility: ${name}`, async () => {
      const result = await new AxeBuilder({ page }).analyze();
      const violations = result.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
      await info.attach(`axe-${name}.json`, { body: JSON.stringify(violations, null, 2), contentType: "application/json" });
      expect.soft(violations, `${name}: no serious/critical axe violations`).toEqual([]);
      await screenshot(page, info, name);
    });
  }

  await test.step("Prepare real sample through onboarding", async () => {
    await page.goto("/login");
    await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
    await accessible("login-dark");
    await signIn(page);
    await accessible("home-dark");
    await addSample(page, () => accessible("onboarding-workspace-dark"));
    await waitForScan(page);
    await accessible("scan-dark");
  });

  await test.step("Repository list", async () => {
    await navigate(page, /^Repositories$/);
    await expect(page.getByRole("heading", { name: "Repositories", exact: true })).toBeVisible();
    await accessible("repositories-dark");
  });
  await test.step("Repository workspace", async () => {
    await page.getByRole("link", { name: /^sample\b/ }).click();
    await expect(page.getByRole("heading", { name: "sample", exact: true })).toBeVisible();
    await accessible("workspace-dark");
    await theme(page, "light");
    await accessible("workspace-light");
    await theme(page, "dark");
  });
  let title = "";
  await test.step("Findings list → URL filter/sort survives reload", async () => {
    await navigate(page, /^(All )?Findings$/);
    await page.getByRole("button", { name: "List", exact: true }).click();
    await page.getByRole("button", { name: "All open", exact: true }).click();
    const finding = page.getByRole("table").or(page.getByRole("grid")).getByRole("link").first();
    await expect(finding).toBeVisible();
    // The link holds the human title, then the rule ID as secondary text.
    title = await finding.evaluate((el) => (el.childNodes[0]?.textContent ?? "").trim());
    await page.getByRole("textbox", { name: "Filter findings...", exact: true }).fill(title);
    await page.getByRole("columnheader", { name: /Title/ }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get("q")).toBe(title);
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("title");
    await page.reload();
    await expect(page.getByRole("textbox", { name: "Filter findings...", exact: true })).toHaveValue(title);
    await expect(page.getByRole("link", { name: title }).first()).toBeVisible();
    await accessible("findings-dark");
  });
  let findingID = "";
  await test.step("Finding detail → triage", async () => {
    await page.getByRole("link", { name: title }).first().click();
    await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
    findingID = new URL(page.url()).pathname.split("/").pop()!;
    await accessible("finding-dark");
    await page.getByRole("button", { name: "Won't fix", exact: true }).click();
    await expect(page.getByText("Status updated", { exact: true })).toBeVisible();
  });
  await test.step("Audit contains this finding's successful triage event", async () => {
    await navigate(page, /^Audit(?: log)?$/);
    await expect(page.getByRole("heading", { name: "Audit log", exact: true })).toBeVisible();
    const event = page.getByRole("row").filter({ hasText: `/findings/${findingID}/status` });
    await expect(event).toContainText("PUT");
    await expect(event).toContainText("200");
    await accessible("audit-dark");
  });
});
