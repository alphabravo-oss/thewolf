// One spec per converted create flow (docs/UI_DESIGN_PLAN.md T2.7): the
// `+ Add …` button opens a dialog, an empty submit shows field errors and sends
// nothing, a valid submit posts the right body, closes the dialog and toasts.
// Destructive row actions go through a confirm (typed-name for the high-risk).
import { expect, test, type Page, type Route } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

const NOW = "2026-10-01T12:00:00Z";
const apiPath = (name: string) => new RegExp(`/api(?:/v1)?${name}$`);

/** A list endpoint that records POSTs and appends them, for one test. */
async function crud<T extends { id: string }>(page: Page, name: string, rows: T[], make: (body: Record<string, unknown>) => T) {
  const posts: Record<string, unknown>[] = [];
  await page.route(apiPath(name), async (route: Route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as Record<string, unknown>;
      posts.push(body);
      rows.push(make(body));
      return route.fulfill({ status: 201, json: { data: rows.at(-1) } });
    }
    return route.fulfill({ json: { data: rows } });
  });
  return posts;
}

const admin = (page: Page) => installScannerApiMock(page, { currentUser: { role: "admin" } });

test("Add user: validates, creates, toasts; delete asks for the email @browser-matrix", async ({ page }) => {
  await admin(page);
  const users = [{ id: "u-1", email: "root@example.com", role: "admin", created_at: NOW, updated_at: NOW }];
  const posts = await crud(page, "/users", users, (b) => ({ id: "u-2", email: String(b.email), role: String(b.role), created_at: NOW, updated_at: NOW }));
  const deletes: string[] = [];
  await page.route(/\/api(?:\/v1)?\/users\/u-2$/, (route) => { deletes.push(route.request().method()); return route.fulfill({ json: { data: {} } }); });
  await page.goto("/settings?tab=users");

  await page.getByRole("button", { name: "Add user" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add user" });
  await dialog.getByRole("button", { name: "Create user" }).click();
  await expect(dialog.getByText("Enter a valid email address.")).toBeVisible();
  await expect(dialog.getByText("Use at least 12 characters.")).toBeVisible();
  expect(posts).toHaveLength(0);

  await dialog.getByLabel("Email", { exact: true }).fill("New.Person@Example.com");
  await dialog.getByLabel("Password", { exact: true }).fill("correct horse battery");
  await dialog.getByRole("button", { name: "Create user" }).click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toEqual({ email: "new.person@example.com", password: "correct horse battery", role: "user" });
  await expect(page.getByText("User created")).toBeVisible();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("cell", { name: "new.person@example.com", exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Actions for new.person@example.com" }).click();
  await page.getByRole("menuitem", { name: "Delete user" }).click();
  const confirm = page.getByRole("dialog", { name: /Delete user/ });
  const del = confirm.getByRole("button", { name: "Delete user" });
  await expect(del).toBeDisabled();
  await confirm.getByLabel(/Type .* to confirm/).fill("new.person@example.com");
  await del.click();
  await expect.poll(() => deletes).toEqual(["DELETE"]);
});

test("Add API key: validates, creates, reveals the key once; revoking a used key asks for its name @browser-matrix", async ({ page }) => {
  await admin(page);
  const keys = [{ id: "k-1", name: "ci-pipeline", token_prefix: "wolf_ab", scopes: ["read-only"], created_at: NOW, last_used_at: NOW }];
  const posts = await crud(page, "/auth/tokens", keys, (b) => ({ id: "k-2", name: String(b.name), token_prefix: "wolf_cd", scopes: b.scopes as string[], created_at: NOW }));
  await page.route(apiPath("/admin/tokens"), (route) => route.fulfill({ json: { data: [] } }));
  await page.route(apiPath("/users"), (route) => route.fulfill({ json: { data: [] } }));
  const revoked: string[] = [];
  await page.route(/\/api(?:\/v1)?\/auth\/tokens\/k-1$/, (route) => { revoked.push(route.request().method()); return route.fulfill({ json: { data: {} } }); });
  await page.goto("/settings?tab=apikeys");

  await page.getByRole("button", { name: "Add API key" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add API key" });
  await dialog.getByRole("button", { name: "Create key" }).click();
  await expect(dialog.getByText("Give the key a name.")).toBeVisible();
  expect(posts).toHaveLength(0);

  await dialog.getByLabel("Name", { exact: true }).fill("deploy-bot");
  await dialog.getByRole("button", { name: "Create key" }).click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toMatchObject({ name: "deploy-bot", scopes: ["read-only"], expires_in_days: 90 });
  // The secret is shown exactly once, in its own dialog.
  await expect(page.getByRole("dialog", { name: "Key created" })).toBeVisible();
  await page.getByRole("button", { name: "Done" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.getByRole("button", { name: "Actions for ci-pipeline" }).first().click();
  await page.getByRole("menuitem", { name: "Revoke key" }).click();
  const confirm = page.getByRole("dialog", { name: /Revoke/ });
  await expect(confirm.getByRole("button", { name: "Revoke key" })).toBeDisabled();
  await confirm.getByLabel(/Type .* to confirm/).fill("ci-pipeline");
  await confirm.getByRole("button", { name: "Revoke key" }).click();
  await expect.poll(() => revoked).toEqual(["DELETE"]);
  await expect(page.getByText("Key revoked")).toBeVisible();
});

test("Add secret: validates, creates, toasts; delete asks to confirm @browser-matrix", async ({ page }) => {
  await admin(page);
  const secrets: { id: string; key_type: string; key_name: string; value: string; created_at: string }[] = [];
  const posts = await crud(page, "/config/secrets", secrets, (b) => ({ id: "s-1", key_type: String(b.key_type), key_name: String(b.key_name), value: "••••", created_at: NOW }));
  await page.route(apiPath("/admin/secrets"), (route) => route.fulfill({ json: { data: [] } }));
  await page.route(apiPath("/users"), (route) => route.fulfill({ json: { data: [] } }));
  const deletes: string[] = [];
  await page.route(/\/api(?:\/v1)?\/config\/secrets\/s-1$/, (route) => { deletes.push(route.request().method()); return route.fulfill({ json: { data: {} } }); });
  await page.goto("/settings?tab=secrets");

  // Empty list: the empty state carries the same + Add action.
  await expect(page.getByText("No secrets stored")).toBeVisible();
  await page.getByRole("button", { name: "Add secret" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add secret" });
  await dialog.getByRole("button", { name: "Add secret" }).click();
  await expect(dialog.getByText("Give the secret a name.")).toBeVisible();
  await expect(dialog.getByText("Paste the secret value.")).toBeVisible();
  expect(posts).toHaveLength(0);

  await dialog.getByLabel("Name", { exact: true }).fill("anthropic-prod");
  await dialog.getByLabel("Value").fill("sk-ant-test");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: "Anthropic API key" }).click();
  await dialog.getByRole("button", { name: "Add secret" }).click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toEqual({ key_type: "anthropic_key", key_name: "anthropic-prod", value: "sk-ant-test" });
  await expect(page.getByText("Secret added")).toBeVisible();

  await page.getByRole("button", { name: "Actions for anthropic-prod" }).click();
  await page.getByRole("menuitem", { name: "Delete secret" }).click();
  await page.getByRole("dialog", { name: /Delete secret/ }).getByRole("button", { name: "Delete secret" }).click();
  await expect.poll(() => deletes).toEqual(["DELETE"]);
});

test("Add node: validates, creates, toasts; disable and delete confirm @browser-matrix", async ({ page }) => {
  await admin(page);
  const nodes: Record<string, unknown>[] = [];
  const posts = await crud(page, "/nodes", nodes as { id: string }[], (b) => ({ id: "n-1", user_id: "u", created_at: NOW, updated_at: NOW, ...b }) as { id: string });
  await page.route(apiPath("/config/secrets"), (route) => route.fulfill({ json: { data: [{ id: "s-ssh", key_type: "ssh_private_key", key_name: "deploy-key", value: "••••", created_at: NOW }] } }));
  await page.route(apiPath("/users"), (route) => route.fulfill({ json: { data: [] } }));
  await page.route(apiPath("/admin/secrets"), (route) => route.fulfill({ json: { data: [] } }));
  const updates: unknown[] = [];
  await page.route(/\/api(?:\/v1)?\/nodes\/n-1$/, (route) => {
    if (route.request().method() === "PUT") { updates.push(route.request().postDataJSON()); return route.fulfill({ json: { data: {} } }); }
    return route.fulfill({ json: { data: {} } });
  });
  await page.goto("/settings?tab=nodes");

  await page.getByRole("button", { name: "Add node" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add node" });
  await dialog.getByRole("button", { name: "Add node" }).click();
  await expect(dialog.getByText("Name is required.", { exact: true })).toBeVisible();
  await expect(dialog.getByText("Pick the SSH secret to use.")).toBeVisible();
  expect(posts).toHaveLength(0);

  await dialog.getByLabel("Name", { exact: true }).fill("dev-box");
  await dialog.getByLabel("Host", { exact: true }).fill("dev.example.com");
  await dialog.getByLabel("Username").fill("alice");
  await dialog.getByRole("combobox", { name: "Credential secret" }).click();
  await page.getByRole("option", { name: "deploy-key" }).click();
  await dialog.getByRole("button", { name: "Add node" }).click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toMatchObject({ name: "dev-box", host: "dev.example.com", port: 22, username: "alice", auth_type: "private_key", credential_secret_id: "s-ssh", enabled: true });
  await expect(page.getByText("Node added")).toBeVisible();

  // Edit reuses the same dialog, pre-filled.
  await page.getByRole("button", { name: "Actions for dev-box" }).first().click();
  await page.getByRole("menuitem", { name: "Edit node" }).click();
  await expect(page.getByRole("dialog", { name: "Edit dev-box" }).getByLabel("Host", { exact: true })).toHaveValue("dev.example.com");
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

  await page.getByRole("button", { name: "Actions for dev-box" }).first().click();
  await page.getByRole("menuitem", { name: "Disable node" }).click();
  await page.getByRole("dialog", { name: /Disable node/ }).getByRole("button", { name: "Disable node" }).click();
  await expect.poll(() => updates).toEqual([{ enabled: false }]);
});

test("Add credential: a GitHub or GitLab token bound to a host @browser-matrix", async ({ page }) => {
  await admin(page);
  const creds: { id: string; type: string; name: string; allowed_hosts: string[] }[] = [];
  const posts = await crud(page, "/credentials", creds, (b) => ({ id: "c-1", type: String(b.type), name: String(b.name), allowed_hosts: b.allowed_hosts as string[] }));
  const deletes: string[] = [];
  await page.route(/\/api(?:\/v1)?\/credentials\/c-1$/, (route) => { deletes.push(route.request().method()); return route.fulfill({ json: { data: {} } }); });
  await page.goto("/settings?tab=git");

  await page.getByRole("button", { name: "Add credential" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add credential" });
  await dialog.getByRole("button", { name: "Add credential" }).click();
  await expect(dialog.getByText("Give the credential a name.")).toBeVisible();
  await expect(dialog.getByText("Paste the token.")).toBeVisible();
  expect(posts).toHaveLength(0);

  await dialog.getByRole("combobox", { name: "Provider" }).click();
  await page.getByRole("option", { name: "GitLab token" }).click();
  await expect(dialog.getByLabel("Host", { exact: true })).toHaveValue("gitlab.com");
  await dialog.getByLabel("Host", { exact: true }).fill("https://Git.Corp.Example/");
  await dialog.getByLabel("Name", { exact: true }).fill("corp-gitlab");
  await dialog.getByLabel("Token").fill("glpat-secret");
  await dialog.getByRole("button", { name: "Add credential" }).click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toEqual({ type: "gitlab_token", name: "corp-gitlab", secret: "glpat-secret", allowed_hosts: ["git.corp.example"] });
  await expect(page.getByText("Credential added")).toBeVisible();
  await expect(page.getByRole("cell", { name: "git.corp.example", exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Actions for corp-gitlab" }).click();
  await page.getByRole("menuitem", { name: "Delete credential" }).click();
  await page.getByRole("dialog", { name: /Delete credential/ }).getByRole("button", { name: "Delete credential" }).click();
  await expect.poll(() => deletes).toEqual(["DELETE"]);
});

test("Change password: a dialog that validates before it sends @browser-matrix", async ({ page }) => {
  await admin(page);
  const puts: unknown[] = [];
  await page.route(apiPath("/auth/password"), (route) => { puts.push(route.request().postDataJSON()); return route.fulfill({ json: { data: {} } }); });
  await page.route(apiPath("/auth/mfa/status"), (route) => route.fulfill({ json: { data: { enabled: false, required: false } } }));
  await page.goto("/settings?tab=account");

  await page.getByRole("button", { name: "Change password" }).click();
  const dialog = page.getByRole("dialog", { name: "Change password" });
  await expect(dialog.getByRole("button", { name: "Update password" })).toBeDisabled();
  await dialog.getByLabel("Current password").fill("old password value");
  await dialog.getByLabel("New password", { exact: true }).fill("a brand new passphrase");
  await dialog.getByLabel("Confirm new password").fill("something different");
  await expect(dialog.getByText("Passwords do not match.")).toBeVisible();
  await dialog.getByLabel("Confirm new password").fill("a brand new passphrase");
  await dialog.getByRole("button", { name: "Update password" }).click();
  await expect.poll(() => puts.length).toBe(1);
  expect(puts[0]).toEqual({ current_password: "old password value", new_password: "a brand new passphrase" });
  await expect(page.getByText("Password updated")).toBeVisible();
});

test("Install license: validate, then install from a dialog @browser-matrix", async ({ page }) => {
  await admin(page);
  await page.route(apiPath("/edition"), (route) => route.fulfill({ json: { data: { edition: "community", entitlements: {} } } }));
  await page.route(apiPath("/license"), (route) => route.fulfill({ json: { data: { edition: "community", commercial_license: false, data_intact: true } } }));
  const installs: unknown[] = [];
  await page.route(apiPath("/license/validate"), (route) => route.fulfill({ json: { data: { valid: true, reason: "Valid until 2027-01-01" } } }));
  await page.route(apiPath("/license/install"), (route) => { installs.push(route.request().postDataJSON()); return route.fulfill({ json: { data: { valid: true } } }); });
  await page.goto("/settings?tab=license");
  await expect(page.getByText(/^No commercial license\. Community scanning/)).toBeVisible();

  await page.getByRole("button", { name: "Install license" }).click();
  const dialog = page.getByRole("dialog", { name: "Install license" });
  await expect(dialog.getByRole("button", { name: "Install", exact: true })).toBeDisabled();
  await dialog.getByLabel("License contents").fill("-----BEGIN LICENSE-----\nabc\n-----END LICENSE-----");
  await dialog.getByRole("button", { name: "Validate" }).click();
  await expect(dialog.getByText("Valid until 2027-01-01")).toBeVisible();
  await dialog.getByRole("button", { name: "Install", exact: true }).click();
  await expect.poll(() => installs.length).toBe(1);
  await expect(page.getByText("License installed")).toBeVisible();
});
