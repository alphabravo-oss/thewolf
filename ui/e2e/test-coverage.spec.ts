import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installScannerApiMock } from "./scanner-api-mock";

const commit = "a".repeat(40);
async function coveragePage(page: Page, measured = true) {
  await installScannerApiMock(page);
  const repo = { id: "payments", user_id: "user-admin", name: "Payments", source_type: "github", default_branch: "main", detected_languages: "{}", detected_frameworks: "[]" };
  const scan = { id: "tested", repo_id: repo.id, branch: "main", commit_sha: commit, status: "completed", tools_selected: '["trivy"]', tools_completed: '["trivy"]', tools_failed: "[]", created_at: "2026-10-13T10:00:00Z" };
  const formats = ["lcov", "cobertura", "jacoco", "clover", "opencover", "go", "istanbul", "simplecov", "sonar"];
  const data = { status: measured ? "measured" : "not_measured", scan_id: scan.id, repo_id: repo.id, commit_sha: commit, file_count: 60, formats, scope_note: "Coverage measures source files included by your test tooling.", policy: { id: "default", name: "security", scope: "global", scope_id: "", mode: "fail", rules_json: '[{"id":"secrets","action":"fail","category":["secrets"]}]' }, checks: null, report: measured ? { imported_at: "2026-10-13T10:01:00Z", metrics: { lines: { covered: 75, total: 100 }, statements: { covered: 60, total: 100 } }, metric_files: { lines: 40, statements: 20 }, sources: [{ format: "lcov" }, { format: "go" }], excluded_paths: ["generated/**"] } : null };
  const queries: URLSearchParams[] = [];
  const imports: Record<string, unknown>[] = [];
  const policies: Record<string, unknown>[] = [];
  await page.route(/\/api(?:\/v1)?\/repos\/payments$/, (r) => r.fulfill({ json: { data: repo } }));
  await page.route(/\/api(?:\/v1)?\/scans(?:\?|$)/, (r) => r.fulfill({ json: { data: [scan], meta: { total: 1 } } }));
  await page.route(/\/api(?:\/v1)?\/scans\/tested\/test-coverage$/, async (r) => {
    if (r.request().method() === "PUT") imports.push(r.request().postDataJSON() as Record<string, unknown>);
    await r.fulfill({ json: { data } });
  });
  await page.route(/\/api(?:\/v1)?\/scans\/tested\/test-coverage\/files(?:\?|$)/, (r) => {
    const p = new URL(r.request().url()).searchParams; queries.push(p);
    const count = p.get("q") ? 1 : 60; const offset = (Number(p.get("page")) - 1) * Number(p.get("per_page"));
    const rows = Array.from({ length: Math.min(Number(p.get("per_page")), Math.max(0, count - offset)) }, (_, i) => ({ path: `src/a/very/long/component/path/file-${offset + i + 1}.ts`, metrics: { lines: { covered: i, total: 25 } }, uncovered_count: 2, uncovered_lines: [5, 8] }));
    return r.fulfill({ json: { data: rows, meta: { total: count, page: Number(p.get("page")), per_page: Number(p.get("per_page")) } } });
  });
  await page.route(/\/api(?:\/v1)?\/policies$/, (r) => { policies.push(r.request().postDataJSON() as Record<string, unknown>); return r.fulfill({ json: { data: {} } }); });
  await page.goto("/repos/payments?collection_id=services&tab=tests");
  return { queries, imports, policies };
}

test("measured coverage is scoped, paginated and accessible @browser-matrix", async ({ page }) => {
  const { queries } = await coveragePage(page);
  const coverage = page.getByRole("region", { name: "Automated test coverage", exact: true });
  await expect(coverage.getByText("75.0%", { exact: true })).toBeVisible();
  await expect(coverage.getByText("60.0%", { exact: true })).toBeVisible();
  await expect(coverage.getByText("Not measured", { exact: true })).toHaveCount(2);
  const files = coverage.getByRole("region", { name: "Coverage by file" });
  await expect(files.getByRole("row")).toHaveCount(26);
  await expect(files.getByRole("columnheader", { name: "Lines covered" })).toHaveAttribute("aria-sort", "ascending");
  await files.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(files.getByText(/file-26\.ts/)).toBeVisible();
  await files.getByRole("columnheader", { name: "Lines covered" }).press("Enter");
  await expect.poll(() => queries.at(-1)?.get("order")).toBe("desc");
  expect(queries.at(-1)?.get("page")).toBe("1");
  await files.getByRole("textbox", { name: "Search source files" }).fill("payment");
  await expect.poll(() => queries.at(-1)?.get("q")).toBe("payment");
  await expect(files.getByRole("row")).toHaveCount(2);
  await expect(page.getByRole("link", { name: "Back to collection" })).toHaveAttribute("href", "/collections/services");
  const result = await new AxeBuilder({ page }).include("#main-content").withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(result.violations).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("report import and coverage policies preserve security rules @browser-matrix", async ({ page }) => {
  const { imports, policies } = await coveragePage(page, false);
  await expect(page.getByRole("heading", { name: "Not measured", exact: true })).toBeVisible();
  await page.getByText("Import coverage reports", { exact: true }).click();
  const content = "SF:src/a.ts\nDA:1,1\nend_of_record\n";
  await page.getByLabel("Coverage report files", { exact: true }).setInputFiles({ name: "lcov.info", mimeType: "text/plain", buffer: Buffer.from(content) });
  await expect(page.getByLabel("Format for lcov.info")).toHaveText("lcov");
  await page.getByRole("combobox", { name: "Format for lcov.info" }).click();
  await page.getByRole("option", { name: "cobertura", exact: true }).click();
  await expect(page.getByRole("combobox", { name: "Format for lcov.info" })).toHaveText("cobertura");
  await page.getByRole("combobox", { name: "Format for lcov.info" }).click();
  await page.getByRole("option", { name: "lcov", exact: true }).click();
  await page.getByLabel("Tested commit SHA").fill(commit);
  await page.getByRole("button", { name: "Import measured coverage", exact: true }).click();
  await expect.poll(() => imports.length).toBe(1);
  expect(imports[0]).toMatchObject({ commit_sha: commit, reports: [{ format: "lcov", content }] });
  await page.getByText("Configure coverage requirements", { exact: true }).click();
  await page.getByRole("button", { name: "Add requirement", exact: true }).click();
  await page.getByRole("combobox", { name: "Metric for requirement 1" }).click();
  await page.getByRole("option", { name: "statements", exact: true }).click();
  await page.getByLabel("Source root for requirement 1").fill("api");
  await page.getByRole("button", { name: "Save coverage requirements", exact: true }).click();
  await expect.poll(() => policies.length).toBe(1);
  expect(policies[0]).toMatchObject({ scope: "repo", scope_id: "payments", rules: [{ id: "secrets", action: "fail", category: ["secrets"] }, { action: "fail", test_coverage: { metric: "statements", minimum: 80, path_prefix: "api" } }] });
  const result = await new AxeBuilder({ page }).include("#main-content").withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
  expect(result.violations).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("coverage API failures stay distinct from missing evidence", async ({ page }) => {
  await coveragePage(page);
  await page.route(/\/api(?:\/v1)?\/scans\/tested\/test-coverage$/, (r) => r.fulfill({ status: 503, json: { error: { code: "unavailable", message: "Unavailable" } } }));
  await page.reload();
  await expect(page.getByRole("button", { name: "Retry test coverage" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Not measured", exact: true })).toHaveCount(0);
});
