import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

const enterprise = (page: import("@playwright/test").Page) =>
  page.route(/\/api(?:\/v1)?\/edition$/, route => route.fulfill({ json: { data: { edition: "enterprise", entitlements: { "enterprise.remediation": true } } } }));

test("Fixer settings explain what git tokens the fixer needs @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await enterprise(page);
  await page.route(/\/api(?:\/v1)?\/settings$/, route => route.fulfill({ json: { data: { autofix_enabled: "true" } } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/engines$/, route => route.fulfill({ json: { data: { worker: [], catalog: [], defaults: {}, api_keys: {}, console_shell: false } } }));
  await page.route(/\/api(?:\/v1)?\/credentials$/, route => route.fulfill({ json: { data: [] } }));
  await page.goto("/settings?tab=git");

  const card = page.getByRole("region", { name: "Git access" });
  await card.getByRole("button", { name: "Token scopes and private hosts" }).click();
  await expect(card).toContainText("Contents: Read and write");
  await expect(card).toContainText("Pull requests: Read and write");
  await expect(card).toContainText("workflow");
  await expect(card).toContainText("gitlab.com or self-hosted");
  await expect(card).toContainText("api");
  await expect(card).toContainText("write_repository");
  await expect(card).toContainText("Developer or higher");
  await expect(card).toContainText("WOLF_GIT_ALLOWED_CIDRS");
  await expect(card).toContainText("WOLF_GIT_CA_BUNDLE");
  await expect(card).toContainText("SSH remotes are not supported");
  await expect(card).toContainText("other than GitHub and GitLab");
  await expect(card.getByRole("button", { name: "Add credential" }).first()).toBeVisible();
});

test("a host-bound GitLab token can be created, then picked when adding a git URL repo @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  const credentials: { id: string; type: string; name: string; allowed_hosts: string[] }[] = [
    { id: "c-other", type: "gitlab_token", name: "other-host", allowed_hosts: ["gitlab.com"] },
  ];
  const credentialPosts: unknown[] = [];
  const repoPosts: unknown[] = [];
  await page.route(/\/api(?:\/v1)?\/credentials$/, async route => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON();
      credentialPosts.push(body);
      credentials.push({ id: "c-new", type: body.type, name: body.name, allowed_hosts: body.allowed_hosts });
      return route.fulfill({ status: 201, json: { data: credentials.at(-1) } });
    }
    return route.fulfill({ json: { data: credentials } });
  });
  await page.route(/\/api(?:\/v1)?\/repos$/, async route => {
    if (route.request().method() !== "POST") return route.fallback();
    repoPosts.push(route.request().postDataJSON());
    return route.fulfill({ status: 201, json: { data: { id: "r-new", name: "proj", source_type: "git" } } });
  });

  // Create a token bound to a self-hosted GitLab.
  await page.route(/\/api(?:\/v1)?\/config\/secrets$/, route => route.fulfill({ json: { data: [] } }));
  await page.goto("/account?section=secrets");
  await page.getByRole("button", { name: "Add secret" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add secret" });
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: "GitLab token" }).click();
  await expect(dialog.getByText("write_repository").first()).toBeVisible();
  await expect(dialog.getByLabel("GitLab host")).toHaveValue("gitlab.com");
  await dialog.getByLabel("GitLab host").fill("https://GitLab.Example.com/");
  await dialog.getByLabel("Name", { exact: true }).fill("corp-gitlab");
  await dialog.getByLabel("Value", { exact: true }).fill("glpat-secret");
  await dialog.getByRole("button", { name: "Add secret" }).click();
  await expect.poll(() => credentialPosts.length).toBe(1);
  expect(credentialPosts[0]).toEqual({ type: "gitlab_token", name: "corp-gitlab", secret: "glpat-secret", allowed_hosts: ["gitlab.example.com"] });

  // The GitHub type shows its own scopes, no host field.
  await page.getByRole("button", { name: "Add secret" }).first().click();
  await page.getByRole("dialog").getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: "GitHub token" }).click();
  await expect(page.getByRole("dialog").getByText("Pull requests: Read and write").first()).toBeVisible();
  await expect(page.getByLabel("GitLab host")).toHaveCount(0);
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

  // Use it from the add-repo form.
  await page.goto("/repos");
  await page.getByRole("button", { name: "Add repo" }).first().click();
  await page.getByRole("button", { name: "Remote git URL" }).click();
  await page.getByLabel("Git URL").fill("https://gitlab.example.com/group/sub/proj.git");
  await expect(page.getByRole("combobox", { name: "Use credential" })).toBeVisible();
  await page.getByRole("combobox", { name: "Use credential" }).click();
  await expect(page.getByRole("option", { name: "other-host" })).toHaveCount(0); // bound to a different host
  await page.getByRole("option", { name: "corp-gitlab" }).click();
  await page.getByLabel("Name").fill("proj");
  await page.getByRole("checkbox", { name: "Scan now" }).click();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect.poll(() => repoPosts.length).toBe(1);
  expect(repoPosts[0]).toMatchObject({ name: "proj", source_type: "git", source_path: "https://gitlab.example.com/group/sub/proj.git", credential_secret_id: "c-new" });
});

test("add-repo hints at adding a GitLab token when none is bound to the URL host @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await page.route(/\/api(?:\/v1)?\/credentials$/, route => route.fulfill({ json: { data: [{ id: "c1", type: "gitlab_token", name: "gl", allowed_hosts: ["gitlab.com"] }] } }));
  await page.goto("/repos");
  await page.getByRole("button", { name: "Add repo" }).first().click();
  await page.getByRole("button", { name: "Remote git URL" }).click();
  await page.getByLabel("Git URL").fill("https://git.corp.example/team/app.git");
  await expect(page.getByText("No credential is bound to")).toContainText("git.corp.example");
  await expect(page.getByRole("link", { name: "add a GitLab token" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Use credential" })).toHaveCount(0);
});
