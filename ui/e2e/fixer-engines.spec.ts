import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

const efforts = ["low", "medium", "high", "xhigh", "max"].map((id) => ({ id, label: id }));
const catalog = [
  { name: "claude-code", label: "Claude Code", auth: "oauth+api_key", models: [{ id: "sonnet", label: "Sonnet", default: true }], efforts },
  { name: "codex", label: "Codex", auth: "oauth+api_key", efforts, models: [
    { id: "gpt-6.1-sol", label: "GPT-6.1-Sol", default: true, plan: "live", efforts },
    { id: "gpt-6.1-terra", label: "GPT-6.1-Terra", plan: "live", efforts },
  ] },
  { name: "opencode", label: "OpenCode", auth: "oauth+api_key", efforts, models: [{ id: "openai/gpt-5.6-sol", label: "GPT-5.6 Sol", default: true, plan: "live", efforts }] },
  { name: "grok-build", label: "Grok Build", auth: "oauth+api_key", efforts, models: [{ id: "grok-4.6", label: "grok-4.6", default: true, plan: "live" }] },
];
const worker = [
  { name: "claude-code", command: "claude", available: false, installed: true, auth: "none", detail: "not logged in — run: wolf fixer login claude" },
  { name: "codex", command: "codex", available: true, installed: true, auth: "oauth", account: "Logged in using ChatGPT" },
  { name: "opencode", command: "opencode", available: true, installed: true, auth: "oauth", account: "1 credential(s)" },
  { name: "grok-build", command: "grok", available: false, installed: true, auth: "none", detail: "not logged in — run: wolf fixer login grok" },
];

test("each fixer engine is its own card with its own model and effort @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await page.route(/\/api(?:\/v1)?\/edition$/, route => route.fulfill({ json: { data: { edition: "enterprise", entitlements: { "enterprise.remediation": true } } } }));
  let settings: Record<string, string> = { autofix_enabled: "true", fixer_engine: "opencode", fixer_model: "openai/gpt-5.6-sol", fixer_effort: "high" };
  const saved: Record<string, string>[] = [];
  await page.route(/\/api(?:\/v1)?\/settings$/, async route => {
    if (route.request().method() === "PUT") { const body = route.request().postDataJSON(); saved.push(body); settings = { ...settings, ...body }; }
    await route.fulfill({ json: { data: settings } });
  });
  await page.route(/\/api(?:\/v1)?\/fixes\/engines$/, route => route.fulfill({ json: { data: { worker, catalog, defaults: settings, api_keys: {}, console_shell: false } } }));
  await page.goto("/settings?tab=fixer");

  const codex = page.getByRole("article", { name: "Codex" });
  const opencode = page.getByRole("article", { name: "OpenCode" });
  const claude = page.getByRole("article", { name: "Claude Code" });
  await expect(opencode).toContainText("Default");
  await expect(codex).toContainText("Signed in");
  await expect(codex).toContainText("Logged in using ChatGPT");
  await expect(claude).toContainText("Not connected");
  await expect(claude.getByRole("button", { name: "Sign in to Claude Code" })).toBeVisible();
  await expect(claude.getByRole("combobox")).toHaveCount(0);

  // Pick a model on the Codex card only, then make Codex the default.
  await codex.getByRole("combobox", { name: "Codex model" }).click();
  await page.getByRole("option", { name: /GPT-6\.1-Terra/ }).click();
  await codex.getByRole("button", { name: "Use Codex as default" }).click();
  await expect.poll(() => saved.length).toBe(1);
  expect(saved[0]).toMatchObject({ fixer_engine: "codex", fixer_model: "gpt-6.1-terra" });
  await expect(codex).toContainText("Default");
  await expect(opencode.getByRole("button", { name: "Use OpenCode as default" })).toBeVisible();
});
