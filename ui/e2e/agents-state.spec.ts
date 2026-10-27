import { expect, test, type Page } from "@playwright/test";
import { installScannerApiMock } from "./scanner-api-mock";

// AI remediation is an Enterprise capability; the UI reads it from /edition.
async function editionWith(page: Page, remediation: boolean) {
  await page.route(/\/api(?:\/v1)?\/edition$/, route => route.fulfill({ json: { data: {
    edition: remediation ? "enterprise" : "community",
    product: remediation ? "Wolf Enterprise" : "Wolf Community",
    entitlements: { "enterprise.remediation": remediation },
  } } }));
}

test("disabled autofix keeps review and cleanup accessible with role-aware setup @browser-matrix", async ({ page }) => {
  const state = await installScannerApiMock(page, { currentUser: { role: "user" } });
  await editionWith(page, true);
  const job = { id: "fix-disabled", repo_id: "repo-disabled", status: "awaiting_review", pushed: false, result_branch: "wolf-fix/review", engine: "codex", error: "", summary: "{}", finding_ids: [], attempts: [], pause_reason: "", claimed_by: "" };
  let deleted = false;
  let resumed = false;
  let engineRequests = 0;
  await page.route(/\/api(?:\/v1)?\/settings$/, route => route.fulfill({ json: { data: { autofix_enabled: "false" } } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/engines$/, route => { engineRequests++; return route.fulfill({ json: { data: { configured: true } } }); });
  await page.route(/\/api(?:\/v1)?\/repos\/repo-disabled$/, route => route.fulfill({ json: { data: { name: "Disabled repository", source_path: "example/repo" } } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-disabled$/, route => {
    if (route.request().method() === "DELETE") { deleted = true; job.pause_reason = ""; job.result_branch = ""; }
    return route.fulfill({ json: { data: job } });
  });
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-disabled\/resume$/, route => { resumed = true; return route.fulfill({ status: 500 }); });
  await page.goto("/agents/fix-disabled");
  await expect(page.getByText(/Ask an administrator to enable it/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue", exact: true })).toBeDisabled();
  await expect(page.getByRole("link", { name: /Enable it in Settings/ })).toHaveCount(0);
  job.status = "cancelled";
  job.pause_reason = "cancellation_cleanup";
  await page.reload();
  await page.getByRole("button", { name: "Retry cleanup", exact: true }).click();
  await expect(page.getByRole("button", { name: "Retry cleanup", exact: true })).toHaveCount(0);
  expect(deleted).toBe(true);
  expect(resumed).toBe(false);
  await page.goto("/agents");
  await expect(page.getByText(/Ask an administrator to enable it/)).toBeVisible();
  state.currentUser.role = "admin";
  await page.reload();
  await expect(page.getByRole("link", { name: /Enable it in Settings/ })).toBeVisible();
  state.apiCreatedScan.status = "completed";
  await page.goto("/scans/scan-api-created");
  await expect(page.getByRole("heading", { name: /API imported repository/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Agents", exact: true })).toHaveCount(0);
  expect(engineRequests).toBe(0);
});

test("agent failures stay distinct from disabled, unconfigured and empty states @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  await editionWith(page, true);
  let settingsFailed = false;
  let enginesFailed = false;
  let jobsFailed = true;
  const unavailable = { status: 503, json: { error: { code: "unavailable", message: "Temporarily unavailable" } } };
  await page.route(/\/api(?:\/v1)?\/settings$/, route => route.fulfill(settingsFailed ? unavailable : { json: { data: { autofix_enabled: "true" } } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/engines$/, route => route.fulfill(enginesFailed ? unavailable : { json: { data: { worker: [], api_keys: { openai: true } } } }));
  await page.route(/\/api(?:\/v1)?\/fixes(?:\?|$)/, route => route.fulfill(jobsFailed ? unavailable : { json: { data: [{ id: "fix-one", status: "push_failed", engine: "codex", created_at: "2026-10-23T12:00:00Z", current_loop: 1, max_loops: 3 }] } }));
  await page.goto("/agents");
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(page.getByText("No agents yet", { exact: true })).toHaveCount(0);
  jobsFailed = false;
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByText("Push failed", { exact: true })).toBeVisible();
  await expect(page.getByText("Push failed", { exact: true })).toHaveClass(/text-status-warning/);

  enginesFailed = true;
  await page.reload();
  await expect(page.getByText("Unable to check fixer setup", { exact: true })).toBeVisible();
  await expect(page.getByText("Finish fixer setup", { exact: true })).toHaveCount(0);
  enginesFailed = false;
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByText("Unable to check fixer setup", { exact: true })).toHaveCount(0);

  settingsFailed = true;
  await page.reload();
  await expect(page.getByText("Unable to load fixing settings", { exact: true })).toBeVisible();
  await expect(page.getByText("Autonomous fixing is off", { exact: true })).toHaveCount(0);
  settingsFailed = false;
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByText("Push failed", { exact: true })).toBeVisible();
});


test("verification failures cannot publish and PR failures remain actionable @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  await editionWith(page, true);
  const job = { id: "fix-review", repo_id: "repo-review", status: "failed", pushed: false, result_branch: "wolf-fix/review", engine: "codex", error: "Verification image unavailable", summary: "{}", finding_ids: [], attempts: [] };
  await page.route(/\/api(?:\/v1)?\/settings$/, route => route.fulfill({ json: { data: { autofix_enabled: "true" } } }));
  await page.route(/\/api(?:\/v1)?\/repos\/repo-review$/, route => route.fulfill({ json: { data: { name: "Review repository", source_path: "example/repo" } } }));
  await page.route(/\/api(?:\/v1)?\/repos\/repo-review\/fixable$/, route => route.fulfill({ json: { data: { can_push: true, writable: true } } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-review$/, route => route.fulfill({ json: { data: job } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-review\/stream/, route => route.fulfill({ contentType: "text/event-stream", body: 'data: {"type":"fix_completed"}\n\n' }));
  await page.goto("/agents/fix-review");
  await expect(page.getByRole("alert")).toHaveText("Verification image unavailable");
  await expect(page.getByRole("button", { name: /Retry push|Push branch and open PR/ })).toHaveCount(0);
  await expect(page.getByText(/GitHub push failed/)).toHaveCount(0);

  job.status = "push_failed";
  job.error = "workflow scope missing";
  await page.reload();
  await expect(page.getByRole("button", { name: "Retry push", exact: true })).toBeVisible();
  await expect(page.getByText("GitHub push failed; branch retained", { exact: true })).toBeVisible();

  job.summary = JSON.stringify({ publication_error: "publication_not_ready" });
  job.error = "GitHub CLI is not installed on this fixer worker";
  await page.reload();
  await expect(page.getByText("Publication not ready; branch retained", { exact: true })).toBeVisible();
  job.summary = "{}";

  job.pushed = true;
  job.error = "Pull request permission denied";
  await page.reload();
  await expect(page.getByRole("button", { name: "Retry PR creation", exact: true })).toBeVisible();
  await expect(page.getByText("Branch pushed; pull request creation failed", { exact: true })).toBeVisible();
  await expect(page.getByText("Pushed to GitHub", { exact: true })).toHaveCount(0);

  job.status = "succeeded";
  job.error = "";
  job.summary = JSON.stringify({ pr_url: "https://github.com/example/repo/pull/1" });
  await page.reload();
  await expect(page.getByRole("link", { name: "View pull request" })).toHaveAttribute("href", "https://github.com/example/repo/pull/1");
});

test("cancelled agents wait for the worker and retry cleanup without restarting @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  await editionWith(page, true);
  const job = { id: "fix-cleanup", repo_id: "repo-cleanup", status: "cancelled", pause_reason: "cancellation_cleanup", claimed_by: "worker", pushed: false, result_branch: "wolf-fix/cleanup", engine: "codex", error: "", summary: "{}", finding_ids: [], attempts: [] };
  let deletes = 0;
  let restarted = false;
  await page.route(/\/api(?:\/v1)?\/settings$/, route => route.fulfill({ json: { data: { autofix_enabled: "true" } } }));
  await page.route(/\/api(?:\/v1)?\/repos\/repo-cleanup$/, route => route.fulfill({ json: { data: { name: "Cleanup repository", source_path: "example/repo" } } }));
  await page.route(/\/api(?:\/v1)?\/repos\/repo-cleanup\/fixable$/, route => route.fulfill({ json: { data: { can_push: true, writable: true } } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-cleanup$/, async route => {
    if (route.request().method() === "DELETE") {
      deletes++;
      if (deletes === 1) {
        job.error = "Workspace is locked";
        await route.fulfill({ status: 500, json: { error: { code: "cleanup_pending", message: "Cleanup pending" } } });
        return;
      }
      job.pause_reason = "";
      job.result_branch = "";
      job.error = "";
    }
    await route.fulfill({ json: { data: job } });
  });
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-cleanup\/resume$/, async route => { restarted = true; await route.fulfill({ status: 500 }); });
  await page.goto("/agents/fix-cleanup");
  await expect(page.getByText("Agent cancelled. Waiting for the worker to stop and clean up.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry cleanup" })).toHaveCount(0);
  job.claimed_by = "";
  // A cancelled job with pending cleanup continues polling until acknowledged.
  await expect(page.getByRole("button", { name: "Retry cleanup" })).toBeVisible();
  await page.getByRole("button", { name: "Retry cleanup" }).click();
  await expect(page.getByRole("alert")).toHaveText("Workspace is locked");
  await page.getByRole("button", { name: "Retry cleanup" }).click();
  await expect(page.getByRole("button", { name: "Retry cleanup" })).toHaveCount(0);
  await expect(page.getByText(/Workspace cleanup is pending/)).toHaveCount(0);
  expect(deletes).toBe(2);
  expect(restarted).toBe(false);
});

test("published agents retry workspace cleanup without republishing @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page);
  await editionWith(page, true);
  const job = { id: "fix-published", repo_id: "repo-published", scan_id: "scan-published", remediation_id: "rem-published", status: "succeeded", pushed: true, workspace_path: "/retained/clone", result_branch: "wolf-fix/published", engine: "codex", error: "workspace cleanup failed; recovery path retained: workspace locked", summary: JSON.stringify({ pr_url: "https://github.com/example/repo/pull/1" }), finding_ids: [], attempts: [] };
  const remediation = { id: "rem-published", state: "frozen", workspace_path: job.workspace_path };
  let cleanups = 0;
  let republished = false;
  await page.route(/\/api(?:\/v1)?\/settings$/, route => route.fulfill({ json: { data: { autofix_enabled: "true" } } }));
  await page.route(/\/api(?:\/v1)?\/repos\/repo-published$/, route => route.fulfill({ json: { data: { name: "Published repository", source_path: "example/repo" } } }));
  await page.route(/\/api(?:\/v1)?\/repos\/repo-published\/fixable$/, route => route.fulfill({ json: { data: { can_push: true, writable: true } } }));
  await page.route(/\/api(?:\/v1)?\/scans\/scan-published$/, route => route.fulfill({ json: { data: { id: "scan-published", status: "completed" } } }));
  await page.route(/\/api(?:\/v1)?\/scans\/scan-published\/lineage$/, route => route.fulfill({ json: { data: { remediation, children: [], runs: [] } } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-published$/, route => route.fulfill({ json: { data: job } }));
  await page.route(/\/api(?:\/v1)?\/fixes\/fix-published\/resume$/, async route => { republished = true; await route.fulfill({ status: 500 }); });
  await page.route(/\/api(?:\/v1)?\/remediations\/rem-published\/accept$/, async route => {
    cleanups++;
    if (cleanups === 1) {
      await route.fulfill({ status: 500, json: { error: { code: "handoff_failed", message: "Workspace is locked" } } });
      return;
    }
    job.workspace_path = "";
    job.error = "";
    remediation.workspace_path = "";
    await route.fulfill({ json: { data: remediation } });
  });
  await page.goto("/agents/fix-published");
  await expect(page.getByRole("link", { name: "View pull request" })).toHaveAttribute("href", "https://github.com/example/repo/pull/1");
  await page.getByRole("button", { name: "Retry cleanup" }).click();
  await expect(page.getByText("Workspace is locked", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Retry cleanup" }).click();
  await expect(page.getByRole("button", { name: "Retry cleanup" })).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(cleanups).toBe(2);
  expect(republished).toBe(false);
});

test("Community shows AI fixing as an Enterprise feature @browser-matrix", async ({ page }) => {
  await installScannerApiMock(page, { currentUser: { role: "admin" } });
  await editionWith(page, false);
  await page.route(/\/api(?:\/v1)?\/settings$/, route => route.fulfill({ json: { data: { autofix_enabled: "true" } } }));
  await page.goto("/agents");
  await expect(page.getByText(/Autonomous AI fixing is a Wolf Enterprise feature/)).toBeVisible();
  await expect(page.getByRole("link", { name: "Fixes", exact: true })).toHaveCount(0);
});
