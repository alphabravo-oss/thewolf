import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { addSample, navigate, screenshot, signIn, startScan, theme, waitForScan } from "./helpers";

test("§9.16: new operator completes the real sample journey", async ({ page, context }, info) => {
  await test.step("Sign in → Home", () => signIn(page));
  await screenshot(page, info, "home-dark");
  await theme(page, "light");
  await screenshot(page, info, "home-light");
  await theme(page, "dark");
  await test.step("Home → Add sample repository → Fast/Standard scan", () => addSample(page));
  const firstScan = page.url();
  await test.step("Wait for a terminal scan", () => waitForScan(page));
  await screenshot(page, info, "first-scan-dark");

  await test.step("One-line verdict and zero-tool invalidity", async () => {
    await expect.soft(page.getByText(/^(?:Gate (?:passed?|warning|warn|failed?|error|incomplete)|Passed:|Incomplete coverage:|Scan failed:|Scan cancelled:)/i).first(), "A terminal scan must explain its verdict").toBeVisible();
    const progress = page.getByRole("region", { name: "Scanner coverage", exact: true });
    await expect(progress).toBeVisible();
    const counts = (await progress.innerText()).match(/(\d+) completed/);
    expect(counts, "Scanner coverage must expose the completed tool count").not.toBeNull();
    expect.soft(Number(counts![1]), "The sample journey must execute real tools").toBeGreaterThan(0);
    if (Number(counts![1]) === 0) {
      await expect.soft(page.getByText(/^(Completed|Success|Passed)$/)).toHaveCount(0);
      await expect.soft(page.getByText(/^(?:Gate pass\b|Passed:)/i)).toHaveCount(0);
    }
  });

  await test.step("Distinct sample CVEs retain distinct findings", async () => {
    const search = page.getByRole("textbox", { name: "Search findings", exact: true });
    await search.fill("lodash");
    // OSV uses the official GHSA aliases for these two CVEs in lodash 4.17.20:
    // https://github.com/advisories/GHSA-29mw-wpgm-hmr9
    // https://github.com/advisories/GHSA-35jh-r3h4-6jhm
    const advisories = [
      /CVE-2020-28500|GHSA-29mw-wpgm-hmr9/,
      /CVE-2021-23337|GHSA-35jh-r3h4-6jhm/,
    ];
    const links: string[] = [];
    for (const advisory of advisories) {
      const row = page.getByRole("row").filter({ hasText: advisory }).first();
      await expect.soft(row, `The sample must preserve ${advisory}`).toBeVisible();
      if (await row.isVisible()) {
        await expect.soft(row).toContainText("package-lock.json");
        const href = await row.getByRole("link").first().getAttribute("href");
        if (href) links.push(href);
      }
    }
    expect.soft(new Set(links).size, "Two known CVEs in one lockfile must have distinct finding links").toBe(2);
    await search.clear();
  });

  await test.step("Open a finding → corroboration and evidence", async () => {
    await page.getByRole("textbox", { name: "Search findings", exact: true }).fill("main.go");
    const findings = page.getByRole("table").or(page.getByRole("grid")).getByRole("link");
    // The sample's unsafe command / weak crypto has independent scanner evidence.
    const finding = findings.filter({ hasText: /dangerous-exec-command|G401|weak.crypto|use.of.md5/i }).first();
    await expect(finding, "The sample must publish a corroborated SAST issue").toBeVisible();
    await finding.click();
    await expect(page).toHaveURL(/\/findings\/[^/?]+/);
    await expect.soft(page.getByText(/^[\w-]+(?: \+ [\w-]+)+$/), "Corroboration must identify multiple scanners, not just one tool").toBeVisible();
    await expect.soft(page.getByRole("region", { name: "Scanner evidence", exact: true })).toBeVisible();
    await expect.soft.poll(() => page.getByRole("region", { name: "Scanner evidence", exact: true }).getByRole("listitem").count(), { message: "Show at least two independent scanner evidence rows" }).toBeGreaterThanOrEqual(2);
    await screenshot(page, info, "finding-dark");
    await navigate(page, /^Repositories$/);
    await page.getByRole("link", { name: /^sample\b/ }).click();
  });

  await test.step("Second scan → automatic baseline → N new", async () => {
    // A developer commits new vulnerable code after the baseline, as a real team would.
    const sample = process.env.WOLF_LIVE_SAMPLE_DIR;
    expect(sample, "scripts/e2e-live.sh exports the sample checkout path").toBeTruthy();
    writeFileSync(`${sample}/handler.go`, [
      "package main",
      "",
      'import "os/exec"',
      "",
      "// Intentionally vulnerable: introduced after the baseline.",
      "func runReport(name string) ([]byte, error) {",
      '\treturn exec.Command("sh", "-c", "report "+name).Output()',
      "}",
      "",
    ].join("\n"));
    const git = (...args: string[]) => execFileSync("git", ["-C", sample!, "-c", "user.name=E2E", "-c", "user.email=e2e@wolf.local", ...args]);
    git("add", "handler.go");
    git("commit", "-q", "-m", "Add report handler");
    await startScan(page);
    expect(page.url()).not.toBe(firstScan);
    await waitForScan(page);
    const secondScanURL = page.url();
    // The workspace is where operators interpret the automatic baseline.
    await navigate(page, /^Repositories$/);
    await page.getByRole("link", { name: /^sample\b/ }).click();
    await expect(page.getByRole("link", { name: /^[1-9]\d* new since baseline$/ }).first(), "Second scan must report the newly committed issues against the automatic baseline").toBeVisible();
    await screenshot(page, info, "second-scan-dark");
    await page.goto(secondScanURL);
  });
  const secondScan = page.url();

  await test.step("Find and copy CI snippet → fails on new issues and secrets", async () => {
    await navigate(page, /^Repositories$/);
    await page.getByRole("link", { name: /^sample\b/ }).click();
    await page.getByRole("tab", { name: "Settings", exact: true }).click();
    await page.getByRole("button", { name: "Copy CI snippet", exact: true }).click();
    await expect(page.getByText("CI snippet copied", { exact: true })).toBeVisible();
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const snippet = await page.evaluate(() => navigator.clipboard.readText());
    await info.attach("copied-ci.yml", { body: snippet, contentType: "text/plain" });
    expect.soft(snippet, "Copied CI must propagate a failing gate's exit code").toMatch(/set -e[^\n]*\n[\s\S]*wolf scan gate[^\n]*--fail-exit-code/);
    expect.soft(snippet, "Copied CI must enable an enforcing policy").toMatch(/--mode enforce\b[^\n]*--enabled\b/);
    const rules = JSON.parse(snippet.match(/--rules-json '(\[[^\n]+\])'/)?.[1] ?? "[]");
    expect.soft(rules, "An enforcing rule must fail new findings").toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "fail", baseline_state: expect.arrayContaining(["new"]) }),
    ]));
    expect.soft(rules, "Secrets must fail regardless of baseline state").toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "fail", category: expect.arrayContaining(["secrets"]) }),
    ]));
    expect.soft(rules.filter((rule: { category?: string[]; baseline_state?: string[] }) => rule.category?.includes("secrets")).every((rule: { baseline_state?: string[] }) => !rule.baseline_state?.length)).toBe(true);
  });

  await test.step("Retry a failed tool from the scan view", async () => {
    // A realistic failure: the operator stops one slow scanner, then reruns only that tool.
    await page.goto(secondScan);
    await navigate(page, /^Repositories$/);
    await page.getByRole("link", { name: /^sample\b/ }).click();
    await startScan(page);
    const stoppedScan = page.url();
    await page.locator("summary", { hasText: /^Tools \(\d+\)/ }).click();
    const stop = page.getByRole("button", { name: /^Cancel grype; the rest of the scan keeps going$/ });
    await expect(stop, "A slow scanner must be stoppable while the scan runs").toBeVisible({ timeout: 60_000 });
    await stop.click();
    await waitForScan(page);
    await expect(page.getByText(/1 stopped/).first()).toBeVisible();
    await page.getByRole("button", { name: /^Retry 1 tool$/ }).click();
    await expect(page).not.toHaveURL(stoppedScan, { timeout: 60_000 });
    await waitForScan(page);
    await expect(page.getByText(/^1 of 1 scanners? finished$/)).toBeVisible();
    await screenshot(page, info, "retry-dark");
  });
});
