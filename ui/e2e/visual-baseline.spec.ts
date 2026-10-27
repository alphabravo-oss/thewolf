// Visual baseline sweep (docs/UI_DESIGN_PLAN.md T1.9): every route, light and
// dark, on the mock fixtures, with an axe contrast/WCAG check per shot.
//
//   pnpm test:e2e                       screenshots land in test-results/ (untracked)
//   pnpm test:e2e:baseline              refreshes the committed reference in
//                                       e2e/__screenshots__/baseline/<theme>/
//
// Re-run the baseline deliberately when the design changes and review the diff.
import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import path from "node:path";
import { apiCreatedFinding, installScannerApiMock } from "./scanner-api-mock";
import { installRichFixtures } from "./visual-fixtures";

type Fixture = { edition?: "community" | "enterprise" };
const ROUTES: { name: string; url: string; edition?: Fixture["edition"] }[] = [
  { name: "dashboard", url: "/" },
  { name: "repositories", url: "/repos" },
  { name: "repository-detail", url: "/repos/repo-api-created" },
  { name: "scans", url: "/scans" },
  { name: "scan-detail", url: "/scans/scan-source" },
  { name: "findings", url: "/findings?view=list&scope=all" },
  { name: "finding-detail", url: `/findings/${apiCreatedFinding.id}` },
  { name: "vulnerabilities", url: "/vulnerabilities" },
  { name: "collections", url: "/collections" },
  { name: "coverage", url: "/coverage" },
  { name: "audit", url: "/audit" },
  { name: "agents", url: "/agents" },
  { name: "findings-by-repo", url: "/findings" },
  { name: "vulnerability-detail", url: "/vulnerabilities/vuln-sqli" },
  { name: "collection-detail", url: "/collections/backend" },
  { name: "fixes", url: "/fixes" },
  { name: "settings-general", url: "/settings?tab=general" },
  { name: "settings-scanners", url: "/settings?tab=scanners" },
  { name: "account", url: "/account" },
  { name: "enterprise-locked", url: "/enterprise/identity", edition: "community" },
  { name: "enterprise-tenancy", url: "/enterprise/tenancy", edition: "enterprise" },
];

const findings = ["critical", "high", "medium", "low", "info"].map((severity, i) => ({
  ...apiCreatedFinding,
  id: `finding-${i}`,
  severity,
  title: ["Hard-coded credential in config", apiCreatedFinding.title, "Weak TLS cipher suite", "Verbose error message", "Unused dependency"][i],
}));

/** Generic API fallback so routes without a dedicated fixture render their empty state. */
async function installSweepMock(page: Page, edition: "community" | "enterprise") {
  await page.route("**/api/**", (route) => {
    const p = new URL(route.request().url()).pathname.replace(/^\/api(?:\/v1)?/, "");
    const json = (data: unknown, meta?: unknown) => route.fulfill({ json: { data, ...(meta ? { meta } : {}) } });
    if (route.request().method() !== "GET") return json({});
    if (p === "/edition") return json({ edition, licensed: edition === "enterprise", entitlements: { "enterprise.remediation": edition === "enterprise" } });
    if (p === `/findings/${apiCreatedFinding.id}`) return json(apiCreatedFinding);
    if (/^\/scans\/[^/]+\/findings\/stats$/.test(p)) {
      return json({ total: 5, by_severity: { critical: 1, high: 1, medium: 1, low: 1, info: 1 }, by_tool: { semgrep: 5 }, by_category: { sast: 5 } });
    }
    if (p === "/findings") return json(findings, { total: findings.length, page: 1, per_page: 50, suppressed: 0 });
    return json([], { total: 0, page: 1, per_page: 50 });
  });
  await installScannerApiMock(page, { fallthroughUnmocked: true });
}

for (const theme of ["light", "dark"] as const) {
  for (const route of ROUTES) {
    test(`baseline ${route.name} ${theme}`, async ({ page }, testInfo) => {
      test.skip(!testInfo.project.name.startsWith("desktop-chromium"), "one reference viewport");
      await page.addInitScript((t) => localStorage.setItem("wolf-theme", t), theme);
      await installSweepMock(page, route.edition ?? "enterprise");
      // Settings, account and Enterprise pages keep the plain mock; the rest get real data.
      if (!/^(settings|account|enterprise)/.test(route.name)) await installRichFixtures(page);
      await page.goto(route.url);
      await expect(page.locator("#main-content")).toBeVisible();
      await expect(page.locator("html")).toHaveClass(theme === "dark" ? /dark/ : /^(?!.*dark)/);
      await page.waitForLoadState("networkidle");
      const a11y = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
      expect(a11y.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
      const out = process.env.UPDATE_VISUAL_BASELINE
        ? path.join(testInfo.config.rootDir, "__screenshots__", "baseline", theme, `${route.name}.png`)
        : testInfo.outputPath(`${route.name}-${theme}.png`);
      await page.screenshot({ path: out, fullPage: true, animations: "disabled" });
    });
  }
}
