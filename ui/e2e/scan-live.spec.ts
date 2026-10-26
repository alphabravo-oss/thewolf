import { expect, test } from "@playwright/test";
import { createServer } from "node:http";
import { once } from "node:events";
import { installScannerApiMock } from "./scanner-api-mock";

test("live scan reconnects with its cursor, shows output and refreshes terminal state @browser-matrix", async ({ page }) => {
  const state = await installScannerApiMock(page);
  const cursors: (string | undefined)[] = [];
  // Use an actual HTTP stream: Chromium's intercepted fulfill/retry path
  // does not expose the native Last-Event-ID header reliably.
  const server = createServer((request, response) => {
    cursors.push(request.headers["last-event-id"] as string | undefined);
    response.setHeader("Access-Control-Allow-Origin", "http://127.0.0.1:43719");
    response.setHeader("Access-Control-Allow-Credentials", "true");
    if (cursors.length > 2) { response.writeHead(204); response.end(); return; }
    const first = cursors.length === 1;
    if (!first) state.apiCreatedScan.status = "cancelled";
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(first
      ? 'retry: 100\nid: 1\ndata: {"type":"tool_output","tool_name":"trivy","line":"download started"}\n\n'
      : 'id: 2\ndata: {"type":"tool_output","tool_name":"trivy","line":"download finished"}\n\nid: 3\ndata: {"type":"scan_complete","status":"cancelled"}\n\n');
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("missing fixture address");
  await page.addInitScript((url) => {
    const NativeEventSource = window.EventSource;
    window.EventSource = class extends NativeEventSource {
      constructor(path: string | URL, options?: EventSourceInit) {
        super(String(path).endsWith("/scan-api-created/stream") ? url : path, options);
      }
    };
  }, `http://127.0.0.1:${address.port}/events`);
  try {
  await page.goto("/scans/scan-api-created/live");
  await expect(page).toHaveURL(/\/scans\/scan-api-created\/?$/);
  await page.getByText("Tool output · latest 200 lines", { exact: true }).click();
  const output = page.getByLabel("Live tool output", { exact: true });
  await expect(output).toHaveText("[trivy] download started\n[trivy] download finished");
  expect(cursors.slice(0, 2)).toEqual([undefined, "1"]);
  await expect(page.getByRole("status").filter({ hasText: "Scan cancelled." })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel scan", exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Command injection from request input" })).toBeVisible();
  } finally {
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
});


test("scan header exposes attempts, incomplete execution and changed scope @browser-matrix", async ({ page }) => {
  const state = await installScannerApiMock(page);
  Object.assign(state.apiCreatedScan, {
    status: "failed", attempt: 2, tools_completed: "[]", tools_failed: '["trivy"]',
    completeness: "incomplete", failure_code: "worker_lost", failure_message: "Worker stopped before completion.",
    scanner_scopes: [{ tool_name: "trivy", status: "failed", requested_scope: { include_paths: ["src/**"] }, effective_scope: { profile: "standard" }, scope_message: "Repository-scoped scanner: path selectors could not be enforced." }],
  });
  await page.goto("/scans/scan-api-created");
  await expect(page.getByText(/0\/2 tools completed.*Attempt 2/)).toBeVisible();
  await expect(page.getByText("worker_lost", { exact: true })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Worker stopped before completion." })).toBeVisible();
  await page.getByText("Requested and effective scan scope", { exact: true }).click();
  await expect(page.getByText("Requested: Include: src/**", { exact: true })).toBeVisible();
  await expect(page.getByText("Effective: Profile standard", { exact: true })).toBeVisible();
  await expect(page.getByText(/path selectors could not be enforced/)).toBeVisible();
});


test("worker warning survives list, submit and queued detail and can recover @browser-matrix", async ({ page }) => {
  await page.clock.install();
  const state = await installScannerApiMock(page);
  let workerStatus = "unavailable";
  await page.route(/\/scanners\/runtime-capabilities$/, (route) => route.fulfill({ json: { data: {
    scan_runtime: "docker", queue_execution: true, docker_image_management: true,
    worker_readiness: { status: workerStatus, active_workers: workerStatus === "ready" ? 1 : 0, available_slots: workerStatus === "ready" ? 1 : 0, last_active_heartbeat: "2026-10-23T10:00:00Z" },
  } } }));
  await page.route(/\/scans\/preflight$/, (route) => route.fulfill({ json: { data: { missing: [] } } }));
  await page.route(/\/api(?:\/v1)?\/scans$/, async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    state.apiCreatedScan.status = "pending";
    return route.fulfill({ status: 201, json: { data: state.apiCreatedScan } });
  });
  await page.goto("/scans");
  const warning = page.getByText(/No active docker scan worker has checked in/);
  await expect(warning).toBeVisible();
  await expect(page.getByText(/Last active heartbeat:/)).toBeVisible();
  await page.getByRole("button", { name: "New scan" }).click();
  await page.getByLabel("Repo *").selectOption("repo-api-created");
  await page.getByRole("button", { name: "Start scan", exact: true }).click();
  await expect(page).toHaveURL(/\/scans\/scan-api-created/);
  await expect(page.getByText("Scan queued", { exact: true })).toBeVisible();
  await expect(warning).toBeVisible();
  // Verify manual refresh while readiness is stable, then verify automatic
  // recovery. A polling response must not remove the button mid-click.
  await Promise.all([
    page.waitForRequest(/\/scanners\/runtime-capabilities$/),
    page.getByRole("button", { name: "Refresh worker status" }).click(),
  ]);
  workerStatus = "ready";
  await page.clock.fastForward(10_000);
  await expect(warning).toHaveCount(0);
});
