import { expect, test, type Page, type TestInfo } from "@playwright/test";

export async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(process.env.WOLF_ADMIN_EMAIL ?? "admin@wolf.local");
  // Password inputs have no textbox role; the visible reveal control exposes it.
  await page.getByRole("button", { name: "Show password", exact: true }).click();
  await page.getByRole("textbox", { name: /^Password\b/ }).fill(process.env.WOLF_ADMIN_PASSWORD ?? "WolfAdmin!2026xx");
  await page.getByRole("button", { name: "Hide password", exact: true }).click();
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Home", exact: true })).toBeVisible();
}

export async function addSample(page: Page, onWorkspace?: () => Promise<void>) {
  await page.getByRole("button", { name: /^(Add sample repository|Scan the sample repository)$/ }).click();
  await expect(async () => {
    if (await page.getByRole("dialog").getByRole("button", { name: "Start scan", exact: true }).isVisible()) {
      await configureScan(page);
    }
    await expect(page).toHaveURL(/\/(repos|scans)\/[^/?]+/, { timeout: 1000 });
  }).toPass({ timeout: 60_000 });
  // Auto-start may navigate straight to the scan; otherwise follow the workspace.
  if (new URL(page.url()).pathname.startsWith("/repos/")) {
    await onWorkspace?.();
    const latest = page.getByRole("link", { name: /View scan details/ });
    await expect(latest.or(page.getByRole("button", { name: "Scan repository", exact: true })).first()).toBeVisible();
    if (await latest.isVisible()) await latest.click();
    else await startScan(page);
  }
  await expect(page).toHaveURL(/\/scans\/[^/?]+/);
}

export async function theme(page: Page, value: "light" | "dark") {
  const desired = page.getByRole("button", { name: new RegExp(`^Theme: ${value}\\b`) });
  for (let clicks = 0; clicks < 3 && !(await desired.isVisible()); clicks++) {
    await page.getByRole("button", { name: /^Theme:/ }).click();
    const choice = page.getByRole("menuitemradio", { name: value, exact: false });
    if (await choice.isVisible()) await choice.click();
  }
  await expect(desired).toBeVisible();
}

export async function startScan(page: Page) {
  await page.getByRole("button", { name: "Scan repository", exact: true }).click();
  await configureScan(page);
}

async function configureScan(page: Page) {
  const dialog = page.getByRole("dialog");
  const profile = dialog.getByRole("combobox", { name: "Scan profile", exact: true });
  // Both the native select and the shared accessible Select are valid controls.
  if (await profile.evaluate((element) => element.tagName === "SELECT")) {
    await profile.selectOption({ label: "Standard" });
  } else {
    await profile.click();
    // Standard, not Fast: only Standard/Release scans become the automatic baseline (§9.16 step 5).
    await page.getByRole("option", { name: /^Standard\b/ }).click();
  }
  await dialog.getByRole("button", { name: "Start scan", exact: true }).click();
  await expect(async () => {
    // A new operator downloads what the scan needs; skipping would leave coverage incomplete and block the baseline.
    const download = page.getByRole("button", { name: /^Download \d+ images? and scan$/ });
    if (await download.isVisible()) await download.click();
    await expect(page).toHaveURL(/\/scans\/[^/?]+/, { timeout: 1000 });
  }).toPass({ timeout: 600_000 });
}

export async function waitForScan(page: Page) {
  await expect(page.getByText(/^(Completed|Incomplete coverage|Failed|Cancelled|Gate failed|Gate needs attention)$/).first()).toBeVisible({ timeout: 240_000 });
  await expect(page.getByRole("heading", { name: "Findings reported", exact: true })).toBeVisible();
  const coverage = page.getByRole("region", { name: "Scanner coverage", exact: true });
  await expect(coverage).toContainText(/\d+ completed/);
  if (/\b0 completed\b/.test(await coverage.innerText())) {
    test.info().annotations.push({ type: "zero-tool-guard", description: `No completed tools at ${page.url()}; checked that success is not displayed.` });
    await expect.soft(page.getByText(/^(Completed|Success|Passed)$/)).toHaveCount(0);
    await expect.soft(page.getByText(/^(?:Gate pass\b|Passed:)/i)).toHaveCount(0);
  }
}

export async function navigate(page: Page, name: RegExp) {
  const link = page.getByRole("link", { name }).first();
  if (!(await link.isVisible())) {
    await page.getByRole("button", { name: /Open (navigation|menu)|Toggle (navigation|sidebar)/i }).click();
  }
  await link.click();
}

export async function screenshot(page: Page, info: TestInfo, name: string) {
  await test.step(`Screenshot: ${name}`, async () => {
    const path = info.outputPath(`${name}.png`);
    await page.screenshot({ path, fullPage: true });
    await info.attach(name, { path, contentType: "image/png" });
  });
}
