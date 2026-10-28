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
  { name: "fixes", url: "/fixes" },
  { name: "settings-general", url: "/settings?tab=general" },
  { name: "settings-users", url: "/settings?tab=users" },
  { name: "settings-api-keys", url: "/settings?tab=apikeys" },
  { name: "settings-secrets", url: "/settings?tab=secrets" },
  { name: "settings-git-access", url: "/settings?tab=git" },
  { name: "settings-fixer", url: "/settings?tab=fixer" },
  { name: "settings-scanners", url: "/settings?tab=scanners" },
  { name: "settings-images", url: "/settings?tab=images" },
  { name: "settings-license", url: "/settings?tab=license" },
  { name: "account", url: "/account" },
  { name: "enterprise-hub", url: "/enterprise", edition: "enterprise" },
  { name: "enterprise-hub-community", url: "/enterprise", edition: "community" },
  { name: "enterprise-siem", url: "/enterprise/siem", edition: "enterprise" },
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
    if (p === "/edition") return json({ edition, licensed: edition === "enterprise", entitlements: Object.fromEntries(CAPABILITIES.map((c) => [`enterprise.${c}`, edition === "enterprise" && c !== "residency"])) });
    if (p === `/findings/${apiCreatedFinding.id}`) return json(apiCreatedFinding);
    if (/^\/scans\/[^/]+\/findings\/stats$/.test(p)) {
      return json({ total: 5, by_severity: { critical: 1, high: 1, medium: 1, low: 1, info: 1 }, by_tool: { semgrep: 5 }, by_category: { sast: 5 } });
    }
    if (p === "/findings") return json(findings, { total: findings.length, page: 1, per_page: 50, suppressed: 0 });
    return json([], { total: 0, page: 1, per_page: 50 });
  });
  await installScannerApiMock(page, { fallthroughUnmocked: true });
}

const iso = "2026-10-01T12:00:00Z";
const CAPABILITIES = ["identity", "tenancy", "compliance", "integrations", "plugins", "residency", "remediation", "support", "packaging", "intelligence", "verification"];
const efforts = ["low", "medium", "high"].map((id) => ({ id, label: id }));

/** Rows for settings and enterprise pages, so the baseline shows tables rather than empty states. Registered last so it wins over the generic fallback. */
async function installSettingsFixtures(page: Page, edition: "community" | "enterprise") {
  const data: Record<string, unknown> = {
    "/config/secrets": [
      { id: "s-1", key_type: "github_token", key_name: "github-personal", value: "••••", metadata: { login: "octocat", scopes: ["repo"] }, created_at: iso },
      { id: "s-2", key_type: "anthropic_key", key_name: "anthropic-prod", value: "••••", created_at: iso },
    ],
    "/admin/secrets": [{ id: "s-9", user_id: "user-1", key_type: "openai_key", key_name: "openai-ci", value: "••••", created_at: iso }],
    "/auth/tokens": [
      { id: "k-1", name: "ci-pipeline", token_prefix: "wolf_ab12", scopes: ["read-only"], created_at: iso, last_used_at: iso, expires_at: "2027-01-01T00:00:00Z" },
      { id: "k-2", name: "old-script", token_prefix: "wolf_cd34", scopes: ["read-write"], created_at: iso, revoked_at: iso },
    ],
    "/admin/tokens": [{ id: "k-3", user_id: "user-1", name: "deploy-bot", token_prefix: "wolf_ef56", scopes: ["admin"], created_at: iso }],
    "/credentials": [
      { id: "c-1", type: "github_token", name: "github-fixer", allowed_hosts: ["github.com"] },
      { id: "c-2", type: "gitlab_token", name: "corp-gitlab", allowed_hosts: ["gitlab.corp.example"] },
    ],
    "/license": { edition, product: edition === "enterprise" ? "Wolf Enterprise" : "Wolf Community", commercial_license: edition === "enterprise", data_intact: true },
    "/auth/providers": { providers: [{ name: "okta", kind: "redirect" }, { name: "ldap", kind: "directory" }], local_break_glass: true },
    "/fixes/engines": {
      worker: [{ name: "claude-code", available: true, installed: true, auth: "oauth", account: "dev@example.com" }, { name: "codex", available: false, installed: true, auth: "none", detail: "not logged in — run: wolf fixer login codex" }],
      catalog: [
        { name: "claude-code", label: "Claude Code", auth: "oauth+api_key", efforts, models: [{ id: "sonnet", label: "Sonnet", default: true }] },
        { name: "codex", label: "Codex", auth: "oauth+api_key", efforts, models: [{ id: "gpt", label: "GPT", default: true }] },
      ],
      defaults: {}, api_keys: {}, console_shell: false,
    },
    "/enterprise/siem": [{ id: "d-1", name: "splunk-prod", kind: "webhook", created_at: iso }, { id: "d-2", name: "elastic-eu", kind: "webhook", created_at: iso }],
    "/enterprise/ticketing": [{ id: "t-1", name: "jira-security", created_at: iso }],
    "/enterprise/group-mappings": [{ id: "g-1", name: "sec-admins", created_at: iso }],
    "/enterprise/workspaces": [{ id: "w-1", name: "platform", created_at: iso }, { id: "w-2", name: "payments", created_at: iso }],
    "/enterprise/applications": [{ id: "a-1", name: "checkout", created_at: iso }],
    "/enterprise/projects": [],
    "/enterprise/custom-roles": [{ id: "r-1", name: "auditor", created_at: iso }],
  };
  await page.route("**/api/**", (route) => {
    const p = new URL(route.request().url()).pathname.replace(/^\/api(?:\/v1)?/, "");
    if (route.request().method() === "GET" && p in data) return route.fulfill({ json: { data: data[p] } });
    if (p === "/edition") return route.fallback();
    if (edition === "enterprise" && p.startsWith("/enterprise/") && route.request().method() === "GET") return route.fulfill({ json: { data: [] } });
    return route.fallback();
  });
}

for (const theme of ["light", "dark"] as const) {
  for (const route of ROUTES) {
    test(`baseline ${route.name} ${theme}`, async ({ page }, testInfo) => {
      test.skip(!testInfo.project.name.startsWith("desktop-chromium"), "one reference viewport");
      await page.addInitScript((t) => localStorage.setItem("wolf-theme", t), theme);
      await installSweepMock(page, route.edition ?? "enterprise");
      await installSettingsFixtures(page, route.edition ?? "enterprise");
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
