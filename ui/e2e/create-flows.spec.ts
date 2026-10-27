// Create flows that moved from inline forms into dialogs (docs/UI_DESIGN_PLAN.md
// T3.2): each opens from its `Add …` button, validates, submits, and confirms.
import { expect, test } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

test("add repository is a dialog that validates, creates and confirms @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  const posts: unknown[] = [];
  await page.route(/\/api(?:\/v1)?\/repos$/, (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    posts.push(route.request().postDataJSON());
    return route.fulfill({ status: 201, json: { data: { id: "repo-new", name: "proj" } } });
  });
  await page.goto("/repos");
  await page.getByRole("button", { name: "Add repo" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add a repository" });
  const create = dialog.getByRole("button", { name: "Create", exact: true });
  await expect(create).toBeDisabled();
  await dialog.getByLabel("Name").fill("proj");
  await expect(create).toBeDisabled();
  await dialog.getByLabel("Absolute path on host").fill("/srv/repos/proj");
  await expect(create).toBeEnabled();
  await dialog.getByRole("checkbox", { name: "Scan now" }).click();
  await create.click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toMatchObject({ name: "proj", source_type: "local", source_path: "/srv/repos/proj", default_branch: "main" });
  await expect(page.getByText("Repository created", { exact: true })).toBeVisible();
  await expect(dialog).toBeHidden();
});

test("add repository asks before discarding typed input @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await page.goto("/repos");
  await page.getByRole("button", { name: "Add repo" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Add a repository" });
  await dialog.getByLabel("Name").fill("half-typed");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "Discard changes?" })).toBeVisible();
  await page.getByRole("button", { name: "Keep editing" }).click();
  await expect(dialog.getByLabel("Name")).toHaveValue("half-typed");
});

test("collections are created from a dialog @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  const created: unknown[] = [];
  await page.route(/\/api(?:\/v1)?\/collections$/, (route) => {
    if (route.request().method() === "POST") {
      created.push(route.request().postDataJSON());
      return route.fulfill({ status: 201, json: { data: { id: "backend", name: "Backend services" } } });
    }
    return route.fulfill({ json: { data: [] } });
  });
  await page.goto("/collections");
  await page.getByRole("button", { name: "Create collection" }).click();
  const dialog = page.getByRole("dialog", { name: "New collection" });
  const submit = dialog.getByRole("button", { name: "Create", exact: true });
  await expect(submit).toBeDisabled();
  await dialog.getByLabel("Name").fill("Backend services");
  await dialog.getByLabel("Description").fill("Payments and ledger");
  await submit.click();
  await expect.poll(() => created.length).toBe(1);
  expect(created[0]).toEqual({ name: "Backend services", description: "Payments and ledger" });
  await expect(dialog).toBeHidden();
});

test("new scan is a dialog and keeps the page behind it @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  await page.goto("/scans");
  await page.getByRole("button", { name: "New scan" }).click();
  const dialog = page.getByRole("dialog", { name: "New scan" });
  const start = dialog.getByRole("button", { name: "Start scan", exact: true });
  await expect(start).toBeDisabled();
  await dialog.getByLabel("Repo *").selectOption("repo-api-created");
  await expect(start).toBeEnabled();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "Discard changes?" })).toBeVisible();
  await page.getByRole("button", { name: "Discard", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("heading", { name: "All scans" })).toBeVisible();
});

test("merging vulnerabilities uses a dialog instead of a browser prompt @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  const merges: unknown[] = [];
  await page.route(/\/api(?:\/v1)?\/vulnerabilities\/vuln-1$/, (route) => route.fulfill({ json: { data: {
    id: "vuln-1", repo_id: "repo-api-created", scan_id: "scan-api-created", canonical_key: "k", title: "Shell injection", severity: "high", category: "sast",
    composite_score: 4, evidence_count: 1, evidence: [], created_at: "2026-10-13T10:00:00Z", updated_at: "2026-10-13T10:00:00Z",
  } } }));
  await page.route(/\/api(?:\/v1)?\/vulnerabilities\/vuln-1\/merge$/, (route) => {
    merges.push(route.request().postDataJSON());
    return route.fulfill({ json: { data: {} } });
  });
  await page.goto("/vulnerabilities/vuln-1");
  await page.getByRole("button", { name: "Merge…" }).click();
  const dialog = page.getByRole("dialog", { name: "Merge vulnerabilities" });
  const merge = dialog.getByRole("button", { name: "Merge", exact: true });
  await expect(merge).toBeDisabled();
  await dialog.getByLabel("Merge from vulnerability id").fill("vuln-2");
  await merge.click();
  await expect.poll(() => merges.length).toBe(1);
  expect(merges[0]).toEqual({ vulnerability_id: "vuln-2" });
});
