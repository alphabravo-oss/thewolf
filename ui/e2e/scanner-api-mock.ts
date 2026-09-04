import type { Page, Route } from "@playwright/test";

export type ScannerReleaseMode =
  | "read_only"
  | "candidate"
  | "canary"
  | "stable_control";

export interface RecordedRequest {
  method: string;
  pathname: string;
  body?: unknown;
  headers?: Record<string, string>;
}

export interface ScannerApiState {
  currentUser: UserFixture;
  users: UserFixture[];
  candidateState: string;
  candidateApprovals: Array<Record<string, unknown>>;
  notificationState: string;
  signerProfiles: SignerFixture[];
  registries: Array<Record<string, unknown>>;
  registryJobs: Array<Record<string, unknown>>;
  policy: Record<string, unknown>;
  policyHistory: Array<Record<string, unknown>>;
  customBuilds: Array<Record<string, unknown>>;
  customBuildEventRequests: Array<{ id: string; lastEventId?: string }>;
  apiCreatedScan: Record<string, unknown>;
  requests: RecordedRequest[];
  getRequests: string[];
}

interface SignerFixture {
  id: string;
  name: string;
  provider: string;
  algorithm: string;
  key_reference: string;
  secret_reference?: string;
  secret_reference_configured: boolean;
  workload_identity: boolean;
  identity: string;
  issuer: string;
  subject: string;
  trust_root_reference: string;
  state: "active" | "disabled" | "revoked";
  revision: number;
  rotated_from_id?: string;
  revocation_reason?: string;
  revoked_by?: string;
  revoked_at?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

interface SignerInputFixture {
  name: string;
  provider: string;
  algorithm: string;
  key_reference: string;
  secret_reference?: string;
  workload_identity: boolean;
  identity: string;
  issuer: string;
  subject: string;
  trust_root_reference: string;
}

interface ScannerMockOptions {
  mode?: ScannerReleaseMode;
  scanRuntime?: "docker" | "kubernetes";
  failGetCountByPath?: Record<string, number>;
  delayGetMsByPath?: Record<string, number>;
  currentUser?: Partial<UserFixture>;
  users?: UserFixture[];
}

interface UserFixture {
  id: string;
  email: string;
  role: "admin" | "user";
  display_name?: string;
  scopes: string[];
  scanner_supply_chain_personas: string[];
  scanner_supply_chain_scopes: string[];
  created_at: string;
  updated_at: string;
}

const now = "2026-07-30T12:06:00Z";
const releaseId = "release-next";

const userFixtures: UserFixture[] = [
  {
    id: "user-admin",
    email: "security.admin@example.test",
    role: "admin",
    display_name: "Security Admin",
    scopes: ["admin"],
    scanner_supply_chain_personas: ["supply_chain_administrator"],
    scanner_supply_chain_scopes: ["admin:scanner-supply-chain"],
    created_at: "2026-07-01T12:00:00Z",
    updated_at: now,
  },
  {
    id: "user-operator",
    email: "operator@example.test",
    role: "user",
    scopes: ["read:scanner-supply-chain"],
    scanner_supply_chain_personas: ["viewer"],
    scanner_supply_chain_scopes: ["read:scanner-supply-chain"],
    created_at: "2026-07-15T12:00:00Z",
    updated_at: now,
  },
];

function scopesForPersonas(personas: string[]): string[] {
  if (personas.includes("supply_chain_administrator")) {
    return ["admin:scanner-supply-chain"];
  }
  const scopes = new Set<string>(["read:scanner-supply-chain"]);
  if (personas.includes("scanner_operator")) {
    scopes.add("operate:scanner-supply-chain");
  }
  if (personas.includes("release_approver")) {
    scopes.add("approve:scanner-releases");
  }
  if (personas.includes("registry_administrator")) {
    scopes.add("manage:scanner-registries");
  }
  return [...scopes];
}

export async function installScannerApiMock(
  page: Page,
  {
    mode = "stable_control",
    scanRuntime = "docker",
    failGetCountByPath = {},
    delayGetMsByPath = {},
    currentUser = {},
    users = userFixtures,
  }: ScannerMockOptions = {},
): Promise<ScannerApiState> {
  const remainingGetFailures = new Map(Object.entries(failGetCountByPath));
  const state: ScannerApiState = {
    currentUser: {
      ...userFixtures[0],
      ...currentUser,
      scopes: currentUser.scopes ?? [...userFixtures[0].scopes],
      scanner_supply_chain_personas:
        currentUser.scanner_supply_chain_personas ?? [
          ...userFixtures[0].scanner_supply_chain_personas,
        ],
      scanner_supply_chain_scopes: currentUser.scanner_supply_chain_scopes ?? [
        ...userFixtures[0].scanner_supply_chain_scopes,
      ],
    },
    users: users.map((user) => ({
      ...user,
      scopes: [...user.scopes],
      scanner_supply_chain_personas: [...user.scanner_supply_chain_personas],
      scanner_supply_chain_scopes: [...user.scanner_supply_chain_scopes],
    })),
    candidateState: "awaiting_approval",
    candidateApprovals: [],
    notificationState: "dead_letter",
    signerProfiles: signerProfiles.map((profile) => ({ ...profile })),
    registries: registryTargets.map((registry) => ({ ...registry })),
    registryJobs: registryJobs.map((job) => ({ ...job })),
    policy: cloneFixture(activePolicy),
    policyHistory: policyRevisions.map((revision) => cloneFixture(revision)),
    customBuilds: customBuilds.map((build) => ({ ...build })),
    customBuildEventRequests: [],
    apiCreatedScan: cloneFixture(apiCreatedScan),
    requests: [],
    getRequests: [],
  };

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    // The client's BASE_URL is "/api/v1" (lib/api.ts). Strip the version
    // segment as well as the "/api" prefix, or every handler below — which is
    // keyed on the unversioned path — silently misses and the app renders a
    // blank page with no failed request to point at.
    const path = url.pathname.replace(/^\/api(?:\/v1)?/, "");
    const method = request.method();

    if (method === "GET") {
      state.getRequests.push(`${path}${url.search}`);
      const delay = delayGetMsByPath[path] ?? 0;
      if (delay > 0) {
        await new Promise((resolve) => setTimeout(resolve, delay));
      }
      const remaining = remainingGetFailures.get(path) ?? 0;
      if (remaining > 0) {
        remainingGetFailures.set(path, remaining - 1);
        await jsonError(
          route,
          503,
          "E2E_TEMPORARILY_UNAVAILABLE",
          "Scanner release data is temporarily unavailable",
        );
        return;
      }
    }

    if (method !== "GET") {
      state.requests.push({
        method,
        pathname: path,
        body: request.postDataJSON(),
        headers: request.headers(),
      });
    }

    const customBuildEventMatch =
      /^\/v1\/scanners\/custom-builds\/([^/]+)\/events$/.exec(path);
    if (method === "GET" && customBuildEventMatch) {
      const id = decodeURIComponent(customBuildEventMatch[1]);
      const lastEventId = request.headers()["last-event-id"];
      state.customBuildEventRequests.push({ id, lastEventId });
      const build = state.customBuilds.find((item) => item.id === id);
      const terminal = ["completed", "partial", "failed", "cancelled"].includes(
        String(build?.state),
      );
      const firstSequence = lastEventId ? 2 : 1;
      const terminalFrame = terminal
        ? `id: 4001\nevent: ${build?.state === "completed" ? "done" : "error"}\ndata: {"raw":"dckr_pat_terminal_never_render"}\n\n`
        : "";
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body:
          `id: ${firstSequence}\nevent: log\ndata: [default] durable log ${firstSequence}\n\n` +
          terminalFrame,
      });
      return;
    }

    if (
      path ===
      "/v1/scanner-supply-chain/registry-jobs/registry-job-completed/events"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: registryJobEventStream,
      });
      return;
    }

    if (path.endsWith("/events")) {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: "event: heartbeat\ndata: {}\n\n",
      });
      return;
    }

    if (method === "GET" && path === "/auth/me") {
      return json(route, state.currentUser);
    }
    if (method === "GET" && path === "/users") {
      return json(route, state.users);
    }
    const scannerAccessMatch =
      /^\/users\/([^/]+)\/scanner-supply-chain-access$/.exec(path);
    if (method === "PUT" && scannerAccessMatch) {
      const target = state.users.find(
        (user) => user.id === decodeURIComponent(scannerAccessMatch[1]),
      );
      if (!target) {
        return jsonError(route, 404, "not_found", "user not found");
      }
      const body = request.postDataJSON() as { personas?: string[] };
      target.scanner_supply_chain_personas = body.personas?.length
        ? [...body.personas]
        : ["viewer"];
      target.scanner_supply_chain_scopes = scopesForPersonas(
        target.scanner_supply_chain_personas,
      );
      return json(route, target);
    }
    if (method === "GET" && path === "/version") {
      return json(route, {
        version: "2.0.0-e2e",
        commit: "e2e0001",
        edition: "community",
        product: "Wolf Community",
        community: { version: "2.0.0-e2e", commit: "e2e0001" },
      });
    }
    if (method === "GET" && path === "/settings") {
      return json(route, {
        scan_api_enabled: true,
        scanner_release_management_enabled: true,
        scanner_notify_only: "true",
      });
    }
    if (method === "PUT" && path === "/settings") {
      return json(route, request.postDataJSON() ?? {});
    }
    if (method === "GET" && path === "/setup/status") {
      return json(route, {
        repo_count: 1,
        collection_count: 0,
        has_completed_scan: true,
        overall_ok: true,
      });
    }
    if (method === "POST" && path === "/setup/sample-repo") {
      return json(route, apiCreatedRepository, 201);
    }
    if (method === "GET" && path === "/notifications") {
      return json(route, { items: [] });
    }
    if (method === "GET" && path === "/admin/disk") {
      return json(route, {
        artifacts_bytes: 1_048_576,
        workspaces_bytes: 2_097_152,
        db_bytes: 4_096,
      });
    }
    if (method === "POST" && path === "/admin/workspaces/reap") {
      return json(route, { reaped: 0 });
    }
    if (method === "POST" && path === "/webhooks/outbound/test") {
      return json(route, { ok: true });
    }
    if (method === "GET" && path === "/schedules") {
      return json(route, []);
    }
    if (method === "POST" && path === "/schedules") {
      const body = (request.postDataJSON() ?? {}) as Record<string, unknown>;
      return json(route, { id: "schedule-1", ...body }, 201);
    }
    const scheduleMatch = /^\/schedules\/([^/]+)$/.exec(path);
    if (method === "PUT" && scheduleMatch) {
      const body = (request.postDataJSON() ?? {}) as Record<string, unknown>;
      return json(route, { id: scheduleMatch[1], ...body });
    }
    if (method === "DELETE" && scheduleMatch) {
      return json(route, { ok: true });
    }
    if (method === "GET" && path === "/scans") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: [state.apiCreatedScan],
          meta: { total: 1, page: 1, per_page: 50 },
        }),
      });
      return;
    }
    if (method === "GET" && path === "/repos") {
      return json(route, [apiCreatedRepository]);
    }
    if (method === "GET" && path === "/repos/repo-api-created/branches") {
      return json(route, {
        branches: ["main", "release/enterprise"],
        default_branch: "main",
        current_branch: "main",
      });
    }
    if (method === "GET" && path === "/scanners/list") {
      return json(route, [
        { name: "semgrep", category: "sast", languages: ["python"] },
        { name: "trivy", category: "sca", languages: [] },
      ]);
    }
    if (method === "POST" && path === "/scans/preflight") {
      return json(route, { missing: [] });
    }
    if (method === "POST" && path === "/scans") {
      const body = request.postDataJSON() as {
        repo_id?: string;
        branch?: string;
        tools?: string[];
      };
      state.apiCreatedScan = {
        ...state.apiCreatedScan,
        repo_id: body.repo_id,
        branch: body.branch,
        tools_selected: JSON.stringify(body.tools ?? ["semgrep", "trivy"]),
        status: "running",
      };
      return json(route, state.apiCreatedScan, 201);
    }
    if (method === "GET" && path === "/scans/scan-api-created") {
      return json(route, state.apiCreatedScan);
    }
    if (method === "GET" && path === "/scans/scan-api-created/tools") {
      return json(route, [
        {
          name: "semgrep",
          status:
            state.apiCreatedScan.status === "cancelled"
              ? "completed"
              : "completed",
          finding_count: 1,
          has_output: true,
        },
        {
          name: "trivy",
          status:
            state.apiCreatedScan.status === "cancelled"
              ? "cancelled"
              : "running",
          finding_count: 0,
          has_output: false,
        },
      ]);
    }
    if (method === "GET" && path === "/scans/scan-api-created/findings/stats") {
      return json(route, {
        total: 1,
        by_severity: { critical: 0, high: 1, medium: 0, low: 0, info: 0 },
        by_tool: { semgrep: 1 },
        by_category: { sast: 1 },
      });
    }
    if (method === "GET" && path === "/scans/scan-api-created/findings") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: [apiCreatedFinding],
          meta: { total: 1, suppressed: 0 },
        }),
      });
      return;
    }
    if (method === "DELETE" && path === "/scans/scan-api-created") {
      state.apiCreatedScan = {
        ...state.apiCreatedScan,
        status: "cancelled",
        tools_running: "[]",
        tools_completed: '["semgrep"]',
        completed_at: now,
        updated_at: now,
      };
      return json(route, state.apiCreatedScan);
    }
    if (method === "GET" && path === "/scans/scan-source") {
      return json(route, sourceScan);
    }
    if (method === "GET" && path === "/scans/scan-source/tools") {
      return json(route, []);
    }
    if (method === "GET" && path === "/scans/scan-source/findings") {
      return json(route, []);
    }
    if (method === "POST" && path === "/scans/scan-source/release-rescans") {
      const body = request.postDataJSON() as {
        release_id?: string;
        reason?: string;
      };
      return json(route, {
        ...sourceScan,
        id: "scan-release-rescan",
        status: "pending",
        finding_count: 0,
        tools_completed: "[]",
        scanner_release_id: body.release_id,
        release_manifest_digest: release.manifest_digest,
        rescan_of_scan_id: sourceScan.id,
        release_selection_reason: body.reason,
        created_at: now,
        updated_at: now,
      });
    }

    if (method === "GET" && path === "/scanners/runtime-capabilities") {
      return json(route, {
        scan_runtime: scanRuntime,
        queue_execution: true,
        docker_image_management: scanRuntime === "docker",
        scanner_jobs: true,
        durable_events: true,
        tool_cancellation: true,
      });
    }
    if (method === "GET" && path === "/scanners/config") {
      return json(route, {
        image: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
        image_overrides: {
          semgrep: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
        },
        pull_policy: "IfNotPresent",
        network: "wolf-scanners",
        memory: "4g",
        cpus: "2",
        db_volume: "wolf-vuln-db",
        host_repos_root: "/var/lib/wolf/repos",
        in_container_repos_root: "/work",
        uid: 1000,
        gid: 1000,
      });
    }
    if (method === "GET" && path === "/scanners/images") {
      return json(route, scannerImages);
    }
    if (method === "GET" && path === "/scanners/tools") {
      return json(route, scannerTools);
    }
    if (method === "GET" && path === "/config/secrets") {
      return json(route, []);
    }
    if (method === "POST" && path === "/scanners/doctor") {
      return json(route, {
        overall_ok: true,
        checks: [
          {
            label: "Docker daemon",
            ok: true,
            detail: "reachable",
          },
          {
            label: "Scanner cache",
            ok: true,
            detail: "writable",
          },
        ],
      });
    }
    if (method === "POST" && path === "/scanners/pull") {
      return json(route, {
        pulled: scannerImages.map((image) => image.image),
        errors: [],
      });
    }
    if (method === "POST" && path === "/scanners/images/pull") {
      const body = request.postDataJSON() as { image?: string };
      return json(route, {
        image: body.image,
        local_digest: "sha256:updated0001",
      });
    }
    if (
      method === "POST" &&
      (path === "/scanners/tools/check-updates" ||
        /^\/scanners\/tools\/[^/]+\/check-update$/.test(path))
    ) {
      return json(route, {
        tool_name: "semgrep",
        pinned_version: "1.127.0",
        latest_version: "1.128.0",
        latest_reference: "v1.128.0",
        status: "update_available",
        checked_at: now,
        source_type: "github",
        source_url: "https://github.com/semgrep/semgrep",
      });
    }

    const customBuildBase = "/v1/scanners/custom-builds";
    if (method === "GET" && path === customBuildBase) {
      const requestedState = url.searchParams.get("state");
      return json(
        route,
        state.customBuilds.filter(
          (build) => !requestedState || build.state === requestedState,
        ),
      );
    }
    if (method === "POST" && path === customBuildBase) {
      const body = request.postDataJSON() as {
        variants?: string[];
        push?: boolean;
        platforms?: string[];
        namespace?: string;
        reason?: string;
      };
      const requested = body.variants ?? [];
      const variants =
        requested.length === 1 && requested[0] === "all"
          ? ["default", "jvm", "rust", "codeql"]
          : requested;
      const id =
        variants.length === 1 && variants[0] === "codeql"
          ? "custom-build-codeql-local"
          : `custom-build-queued-${state.customBuilds.length + 1}`;
      state.customBuilds.unshift({
        id,
        variants: JSON.stringify(variants),
        push: body.push === true,
        platforms: JSON.stringify(body.platforms ?? ["linux/amd64"]),
        namespace: body.namespace ?? "",
        reserved_version: "2.0.2",
        state: "queued",
        actor: "security.admin@example.test",
        reason: body.reason ?? "",
        attempt: 0,
        max_attempts: 3,
        available_at: now,
        version: 1,
        created_at: now,
        updated_at: now,
      });
      return json(
        route,
        {
          id,
          state: "queued",
          status_url: `${customBuildBase}/${id}`,
          events_url: `${customBuildBase}/${id}/events`,
        },
        202,
        {
          "X-Wolf-Operation-ID": `op_custom_build_${String(state.customBuilds.length).padStart(4, "0")}`,
          "X-Wolf-Trace-ID": "0123456789abcdef0123456789abcdef",
        },
      );
    }
    const customBuildMatch = new RegExp(`^${customBuildBase}/([^/]+)$`).exec(
      path,
    );
    if (method === "GET" && customBuildMatch) {
      const id = decodeURIComponent(customBuildMatch[1]);
      const build = state.customBuilds.find((item) => item.id === id);
      if (!build) {
        return jsonError(
          route,
          404,
          "custom_build_not_found",
          "custom build not found",
        );
      }
      return json(
        route,
        {
          build,
          variants:
            customBuildVariantRecords[id] ?? queuedVariantRecords(build),
        },
        200,
        { ETag: `"${String(build.version ?? 1)}"` },
      );
    }
    const customBuildMutationMatch = new RegExp(
      `^${customBuildBase}/([^/]+)/(cancel|retry)$`,
    ).exec(path);
    if (method === "POST" && customBuildMutationMatch) {
      const id = decodeURIComponent(customBuildMutationMatch[1]);
      const action = customBuildMutationMatch[2];
      const build = state.customBuilds.find((item) => item.id === id);
      if (!build) {
        return jsonError(
          route,
          404,
          "custom_build_not_found",
          "custom build not found",
        );
      }
      build.state = action === "cancel" ? "cancelled" : "queued";
      build.version = Number(build.version ?? 1) + 1;
      build.updated_at = now;
      return json(route, build);
    }

    const base = "/v1/scanner-supply-chain";
    if (method === "GET" && path === "/v1/health") {
      return json(route, {
        status: "degraded",
        uptime_ms: 7_500_000,
        release_factory: releaseFactoryHealth,
      });
    }
    if (method === "GET" && path === `${base}/overview`) {
      return json(route, overview(mode));
    }
    if (method === "GET" && path === `${base}/updates`) {
      return json(route, { items: updates, total: updates.length });
    }
    if (method === "GET" && path === `${base}/discovery-runs`) {
      return json(route, { items: discoveryRuns, total: discoveryRuns.length });
    }
    if (method === "POST" && path === `${base}/discovery-runs`) {
      return json(route, {
        id: "discovery-on-demand",
        state: "queued",
        status_url: `${base}/discovery-runs/discovery-on-demand`,
      });
    }
    if (method === "GET" && path === `${base}/candidates`) {
      return json(route, {
        items: [candidate(state.candidateState, state.candidateApprovals)],
        total: 1,
      });
    }
    if (method === "POST" && path === `${base}/candidates`) {
      state.candidateState = "awaiting_approval";
      return json(route, {
        id: "candidate-1",
        state: "queued",
        status_url: `${base}/candidates/candidate-1`,
      });
    }
    if (method === "GET" && path === `${base}/candidates/candidate-1`) {
      return json(
        route,
        candidate(state.candidateState, state.candidateApprovals),
      );
    }
    if (
      method === "POST" &&
      path === `${base}/candidates/candidate-1/exceptions`
    ) {
      const body = request.postDataJSON() as {
        gate: string;
        owner_id: string;
        reason: string;
        compensating_control: string;
        evidence_digest: string;
        expires_at: string;
      };
      const approval = {
        id: `approval-exception-${state.candidateApprovals.length + 1}`,
        actor: "security.admin@example.test",
        action: "exception",
        reason: body.reason,
        exception_scope: body.gate,
        exception_owner_id: body.owner_id,
        compensating_control: body.compensating_control,
        evidence_digest: body.evidence_digest,
        expires_at: body.expires_at,
        created_at: now,
      };
      state.candidateApprovals.push(approval);
      return json(route, approval, 201);
    }
    if (
      method === "POST" &&
      path === `${base}/candidates/candidate-1/approve`
    ) {
      state.candidateState = "approved";
      state.candidateApprovals.push({
        id: "approval-1",
        actor: "security.admin@example.test",
        action: "approve",
        reason: "Reviewed compatible tool update evidence",
        created_at: now,
      });
      return json(route, {
        id: "candidate-1",
        state: state.candidateState,
      });
    }
    if (
      method === "POST" &&
      path === `${base}/candidates/candidate-1/publish`
    ) {
      state.candidateState = "published";
      return json(route, {
        id: releaseId,
        state: "published",
      });
    }
    if (
      method === "GET" &&
      path === `${base}/candidates/candidate-1/diffs/manifest`
    ) {
      return json(route, artifactDiff("manifest"));
    }
    if (
      method === "GET" &&
      path === `${base}/candidates/candidate-1/diffs/lock`
    ) {
      return json(route, artifactDiff("lock"));
    }
    if (method === "GET" && path === `${base}/releases`) {
      return json(route, { items: [release], total: 1 });
    }
    if (method === "GET" && path === `${base}/policy`) {
      return json(route, state.policy);
    }
    if (method === "POST" && path === `${base}/policy/validate`) {
      return json(route, {
        valid: true,
        errors: [],
        warnings: [],
        next_execution: {
          daily_discovery: "2026-07-31T06:00:00Z",
          weekly_candidate: "2026-08-02T07:00:00Z",
        },
      });
    }
    if (method === "POST" && path === `${base}/policy/dry-run`) {
      const body = request.postDataJSON() as { candidate_id?: string };
      return json(route, {
        candidate_id: body.candidate_id,
        outcome: "awaiting_approval",
        auto_promotion: false,
        blocking_reasons: ["A second independent approval is required."],
        advisories: ["The vulnerability exception expires before rollout."],
        policy_decision_digest: `sha256:${"f".repeat(64)}`,
      });
    }
    if (method === "PUT" && path === `${base}/policy`) {
      const body = request.postDataJSON() as {
        schedule?: Record<string, unknown>;
        rules?: Record<string, unknown>;
      };
      const revision = Number(state.policy.revision ?? 0) + 1;
      state.policy = {
        ...state.policy,
        revision,
        schedule: cloneFixture(body.schedule ?? {}),
        rules: cloneFixture(body.rules ?? {}),
        created_by: "security.admin@example.test",
        updated_at: now,
      };
      state.policyHistory = state.policyHistory.map((item) => ({
        ...item,
        enabled: false,
      }));
      state.policyHistory.unshift(cloneFixture(state.policy));
      return json(route, state.policy, 200, { ETag: `"${revision}"` });
    }
    if (method === "GET" && path === `${base}/policy/revisions`) {
      return json(route, state.policyHistory);
    }
    const policyRestoreMatch = new RegExp(
      `^${base}/policy/revisions/(\\d+)/restore$`,
    ).exec(path);
    if (method === "POST" && policyRestoreMatch) {
      const sourceRevision = Number(policyRestoreMatch[1]);
      const historical = state.policyHistory.find(
        (item) => Number(item.revision) === sourceRevision,
      );
      if (!historical) {
        return jsonError(route, 404, "not_found", "policy revision not found");
      }
      const revision = Number(state.policy.revision ?? 0) + 1;
      state.policyHistory = state.policyHistory.map((item) => ({
        ...item,
        enabled: false,
      }));
      state.policy = {
        ...cloneFixture(historical),
        id: "scanner-policy-global",
        revision,
        enabled: true,
        created_by: "security.admin@example.test",
        updated_at: now,
      };
      state.policyHistory.unshift(cloneFixture(state.policy));
      return json(route, state.policy, 201, { ETag: `"${revision}"` });
    }
    if (method === "GET" && path === `${base}/registries`) {
      return json(route, state.registries);
    }
    if (method === "POST" && path === `${base}/registries`) {
      const body = request.postDataJSON() as Record<string, unknown>;
      const created: Record<string, unknown> = {
        ...body,
        id: "registry-created",
        secret_reference: undefined,
        credential_reference_configured: Boolean(body.secret_reference),
        credential_reference_kind: body.secret_reference
          ? "wolf_secret"
          : undefined,
        version: 1,
        health: "unknown",
        updated_at: now,
      };
      state.registries.push(created);
      return json(route, created, 201, { ETag: '"1"' });
    }
    const registryTargetMatch = new RegExp(`^${base}/registries/([^/]+)$`).exec(
      path,
    );
    if (method === "PATCH" && registryTargetMatch) {
      const id = decodeURIComponent(registryTargetMatch[1]);
      const body = request.postDataJSON() as Record<string, unknown>;
      const index = state.registries.findIndex((item) => item.id === id);
      if (index < 0) {
        return jsonError(route, 404, "not_found", "registry target not found");
      }
      const current = state.registries[index];
      const updated: Record<string, unknown> = {
        ...current,
        ...body,
        secret_reference: undefined,
        credential_reference_configured:
          Boolean(body.secret_reference) ||
          Boolean(current.credential_reference_configured),
        version: Number(current.version ?? 0) + 1,
        updated_at: now,
      };
      state.registries[index] = updated;
      return json(route, updated, 200, {
        ETag: `"${String(updated.version)}"`,
      });
    }
    const registryActionMatch = new RegExp(
      `^${base}/registries/([^/]+)/(check|reconcile)$`,
    ).exec(path);
    if (method === "POST" && registryActionMatch) {
      const registryId = decodeURIComponent(registryActionMatch[1]);
      return json(route, {
        registry_id: registryId,
        reachable: true,
        matched: registryActionMatch[2] === "reconcile" ? true : undefined,
        checked_at: now,
        latency_ms: 42,
      });
    }
    if (method === "GET" && path === `${base}/registry-jobs`) {
      const registryTarget = url.searchParams.get("registry_target_id");
      const kind = url.searchParams.get("kind");
      const requestedState = url.searchParams.get("state");
      const items = state.registryJobs.filter(
        (job) =>
          (!registryTarget || job.registry_target_id === registryTarget) &&
          (!kind || job.kind === kind) &&
          (!requestedState || job.state === requestedState),
      );
      return json(route, items);
    }
    const registryJobMatch = new RegExp(`^${base}/registry-jobs/([^/]+)$`).exec(
      path,
    );
    if (method === "GET" && registryJobMatch) {
      const id = decodeURIComponent(registryJobMatch[1]);
      const job = state.registryJobs.find((item) => item.id === id);
      if (!job) {
        return jsonError(route, 404, "not_found", "registry job not found");
      }
      return json(
        route,
        {
          job,
          images: id === "registry-job-completed" ? registryImageEvidence : [],
          events_url: `${base}/registry-jobs/${id}/events`,
        },
        200,
        { ETag: `"${String(job.version ?? 1)}"` },
      );
    }
    const retryRegistryJobMatch = new RegExp(
      `^${base}/registry-jobs/([^/]+)/retry$`,
    ).exec(path);
    if (method === "POST" && retryRegistryJobMatch) {
      const id = decodeURIComponent(retryRegistryJobMatch[1]);
      const job = state.registryJobs.find((item) => item.id === id);
      if (!job) {
        return jsonError(route, 404, "not_found", "registry job not found");
      }
      job.state = "queued";
      job.attempt = 0;
      job.version = Number(job.version ?? 1) + 1;
      job.updated_at = now;
      return json(route, {
        id,
        state: "queued",
        status_url: `${base}/registry-jobs/${id}`,
        events_url: `${base}/registry-jobs/${id}/events`,
      });
    }
    const createRegistryJobMatch = new RegExp(
      `^${base}/registries/([^/]+)/jobs$`,
    ).exec(path);
    if (method === "POST" && createRegistryJobMatch) {
      const body = request.postDataJSON() as {
        kind?: string;
        release_id?: string;
        source_registry_id?: string;
        re_sign_policy?: string;
        reason?: string;
        max_attempts?: number;
      };
      const id = `registry-job-${body.kind ?? "reconcile"}-queued`;
      state.registryJobs.unshift({
        id,
        registry_target_id: decodeURIComponent(createRegistryJobMatch[1]),
        source_registry_target_id: body.source_registry_id,
        release_id: body.release_id,
        kind: body.kind ?? "reconcile",
        re_sign_policy: body.re_sign_policy ?? "preserve",
        state: "queued",
        actor: "security.admin@example.test",
        reason: body.reason ?? "",
        attempt: 0,
        max_attempts: body.max_attempts ?? 5,
        available_at: now,
        version: 1,
        created_at: now,
        updated_at: now,
      });
      return json(route, {
        id,
        state: "queued",
        status_url: `${base}/registry-jobs/${id}`,
        events_url: `${base}/registry-jobs/${id}/events`,
      });
    }
    const cleanupRegistryJobMatch = new RegExp(
      `^${base}/registries/([^/]+)/cleanup-jobs$`,
    ).exec(path);
    if (method === "POST" && cleanupRegistryJobMatch) {
      const body = request.postDataJSON() as {
        reason?: string;
        max_attempts?: number;
      };
      const id = "registry-job-cleanup-queued";
      state.registryJobs.unshift({
        id,
        registry_target_id: decodeURIComponent(cleanupRegistryJobMatch[1]),
        kind: "cleanup",
        re_sign_policy: "forbidden",
        state: "queued",
        actor: "security.admin@example.test",
        reason: body.reason ?? "",
        attempt: 0,
        max_attempts: body.max_attempts ?? 5,
        available_at: now,
        version: 1,
        created_at: now,
        updated_at: now,
      });
      return json(route, {
        id,
        state: "queued",
        status_url: `${base}/registry-jobs/${id}`,
        events_url: `${base}/registry-jobs/${id}/events`,
      });
    }
    if (method === "GET" && path === `${base}/registry-quarantine`) {
      const registryTarget = url.searchParams.get("registry_target_id");
      const requestedState = url.searchParams.get("state");
      const items = registryQuarantine.filter(
        (object) =>
          (!registryTarget || object.registry_target_id === registryTarget) &&
          (!requestedState || object.state === requestedState),
      );
      return json(route, items);
    }
    if (method === "POST" && path === `${base}/legacy-release-imports`) {
      return json(route, legacyImportResult, 201);
    }
    if (method === "GET" && path === `${base}/signers`) {
      return json(route, state.signerProfiles);
    }
    const signerMatch = new RegExp(`^${base}/signers/([^/]+)$`).exec(path);
    if (method === "GET" && signerMatch) {
      const signer = state.signerProfiles.find(
        (profile) => profile.id === decodeURIComponent(signerMatch[1]),
      );
      if (!signer) {
        return jsonError(route, 404, "not_found", "signer profile not found");
      }
      return json(route, signer, 200, { ETag: `"${signer.revision}"` });
    }
    if (method === "POST" && path === `${base}/signers`) {
      const input = request.postDataJSON() as SignerInputFixture;
      const created = signerFromInput(input, "signer-created", 1);
      state.signerProfiles.push(created);
      return json(route, created, 201, { ETag: '"1"' });
    }
    const rotateMatch = new RegExp(`^${base}/signers/([^/]+)/rotate$`).exec(
      path,
    );
    if (method === "POST" && rotateMatch) {
      const id = decodeURIComponent(rotateMatch[1]);
      const current = state.signerProfiles.find((profile) => profile.id === id);
      if (!current) {
        return jsonError(route, 404, "not_found", "signer profile not found");
      }
      current.state = "disabled";
      current.updated_at = now;
      const input = request.postDataJSON() as SignerInputFixture;
      const replacement = {
        ...signerFromInput(input, "signer-rotated", current.revision + 1),
        rotated_from_id: current.id,
      };
      state.signerProfiles.push(replacement);
      return json(route, replacement, 201, {
        ETag: `"${replacement.revision}"`,
      });
    }
    const revokeMatch = new RegExp(`^${base}/signers/([^/]+)/revoke$`).exec(
      path,
    );
    if (method === "POST" && revokeMatch) {
      const id = decodeURIComponent(revokeMatch[1]);
      const current = state.signerProfiles.find((profile) => profile.id === id);
      if (!current) {
        return jsonError(route, 404, "not_found", "signer profile not found");
      }
      const input = request.postDataJSON() as { reason: string };
      current.state = "revoked";
      current.revision += 1;
      current.revocation_reason = input.reason;
      current.revoked_by = "security.admin@example.test";
      current.revoked_at = now;
      current.updated_at = now;
      return json(route, current, 200, { ETag: `"${current.revision}"` });
    }
    if (method === "GET" && path === `${base}/releases/${releaseId}`) {
      return json(route, releaseDetail);
    }
    if (method === "POST" && path === `${base}/releases/${releaseId}/promote`) {
      return json(route, {
        id: "rollout-1",
        state: "pending",
        status_url: `${base}/rollouts/rollout-1`,
      });
    }
    if (method === "GET" && path === `${base}/rollouts`) {
      return json(route, { items: [rollout], total: 1 });
    }
    if (method === "GET" && path === `${base}/rollouts/rollout-1`) {
      return json(route, rolloutDetail);
    }
    if (method === "POST" && path === `${base}/rollouts/rollout-1/rollback`) {
      return json(route, {
        id: "rollout-1",
        state: "rolling_back",
      });
    }
    if (method === "GET" && path === `${base}/audit`) {
      const traceId = url.searchParams.get("trace_id");
      const operationId = url.searchParams.get("operation_id");
      const aggregateType = url.searchParams.get("aggregate_type");
      const eventType = url.searchParams.get("event_type");
      const actor = url.searchParams.get("actor");
      const items = auditEvents.filter(
        (event) =>
          (!traceId || event.trace_id === traceId) &&
          (!operationId || event.operation_id === operationId) &&
          (!aggregateType || event.aggregate_type === aggregateType) &&
          (!eventType || event.event_type.includes(eventType)) &&
          (!actor || event.actor.includes(actor)),
      );
      return json(route, { items, total: items.length });
    }
    if (method === "GET" && path === `${base}/alerts`) {
      const requestedState = url.searchParams.get("state") ?? "open";
      const requestedKind = url.searchParams.get("kind");
      const requestedSeverity = url.searchParams.get("severity");
      const items = [scannerAlertOpen, scannerAlertResolved].filter(
        (item) =>
          (requestedState === "all" || item.state === requestedState) &&
          (!requestedKind || item.kind === requestedKind) &&
          (!requestedSeverity || item.severity === requestedSeverity),
      );
      return json(route, { items });
    }
    if (method === "GET" && path === `${base}/alerts/alert-rollout`) {
      return json(route, scannerAlertOpen, 200, { ETag: '"5"' });
    }
    if (method === "GET" && path === `${base}/alerts/alert-discovery`) {
      return json(route, scannerAlertResolved, 200, { ETag: '"9"' });
    }
    if (method === "GET" && path === `${base}/notifications`) {
      const requestedState = url.searchParams.get("state");
      const requestedDestination = url.searchParams.get("destination_type");
      const items = [
        notificationDeadLetter(state.notificationState),
        notificationUi,
      ].filter(
        (item) =>
          (!requestedState || item.state === requestedState) &&
          (!requestedDestination ||
            item.destination_type === requestedDestination),
      );
      return json(route, {
        items,
        next_cursor: undefined,
      });
    }
    if (
      method === "GET" &&
      path === `${base}/notifications/notification-dead`
    ) {
      return json(route, notificationDeadLetter(state.notificationState), 200, {
        ETag: '"notification-version-7"',
      });
    }
    if (method === "GET" && path === `${base}/notifications/notification-ui`) {
      return json(route, notificationUi, 200, {
        ETag: '"notification-version-2"',
      });
    }
    if (
      method === "POST" &&
      path === `${base}/notifications/notification-dead/retry`
    ) {
      state.notificationState = "retry";
      return json(
        route,
        {
          ...notificationDeadLetter(state.notificationState),
          version: 8,
        },
        200,
        {
          ETag: '"notification-version-8"',
        },
      );
    }

    await jsonError(
      route,
      404,
      "E2E_ROUTE_NOT_MOCKED",
      `${method} ${path} is not part of the deterministic scanner fixture`,
    );
  });

  return state;
}

function capabilities(mode: ScannerReleaseMode) {
  return {
    mode,
    read: true,
    candidates: ["candidate", "canary", "stable_control"].includes(mode),
    canary: ["canary", "stable_control"].includes(mode),
    stable_control: mode === "stable_control",
  };
}

function overview(mode: ScannerReleaseMode) {
  return {
    capabilities: capabilities(mode),
    freshness: {
      status: "incomplete",
      current: 22,
      updates_available: 2,
      incomplete: 1,
      failed: 0,
      total: 24,
      last_checked_at: now,
    },
    stable_release: {
      id: "release-stable",
      name: "scanner-set-2026.30.4",
      state: "stable",
      published_at: "2026-07-29T12:00:00Z",
    },
    stable_release_age_seconds: 86_400,
    registry_health: {
      healthy: 1,
      degraded: 0,
      failed: 1,
      total: 2,
    },
    active_rollout: rollout,
    alerts: {
      open_warning: 2,
      open_critical: 1,
      resolved: 5,
    },
    generated_at: now,
  };
}

const releaseFactoryHealth = {
  status: "degraded",
  ready: false,
  database: "ok",
  uptime_ms: 7_500_000,
  components: [
    {
      component: "build",
      enabled: true,
      status: "degraded",
      ready: false,
      last_activity: "2026-07-30T12:00:00Z",
      last_success: "2026-07-30T11:00:00Z",
      stuck_work: { expired_lease: 2 },
      queue_depth: {
        pending: 3,
        retry: 1,
        customer_repository_id: 999,
      },
      run_counts: {
        success: 12,
        error: 2,
        cancelled: 1,
        customer_repository_id: 999,
      },
      result_counts: {
        completed: 10,
        partial: 2,
        failed: 2,
        customer_repository_id: 999,
      },
      average_run_duration_ms: 3_000,
    },
    {
      component: "discovery",
      enabled: true,
      status: "idle",
      ready: true,
      last_activity: "2026-07-30T12:05:00Z",
      last_success: "2026-07-30T12:05:00Z",
    },
    {
      component: "proposal",
      enabled: false,
      status: "disabled",
      ready: true,
      stuck_work: {},
    },
  ],
};

const updates = [
  {
    id: "update-semgrep",
    discovery_run_id: "discovery-previous",
    component_type: "tool",
    component_name: "semgrep",
    current_value: "1.127.0",
    available_value: "1.128.0",
    source_evidence: {
      source: "github",
      url: "https://github.com/semgrep/semgrep/releases/tag/v1.128.0",
      checked_at: now,
      coverage: "complete",
      version_status: "newer",
    },
    risk_class: "low",
    compatibility: {
      compatible: true,
      reasons: ["parser contract unchanged"],
      required_gates: ["parser", "signature", "provenance"],
    },
    selection_state: "available",
    integration_tier: "default",
    source: "github",
    last_checked_at: now,
    updated_at: now,
  },
  {
    id: "update-codeql",
    discovery_run_id: "discovery-previous",
    component_type: "tool",
    component_name: "codeql",
    current_value: "2.22.0",
    available_value: "2.22.1",
    source_evidence: {
      source: "github",
      checked_at: now,
      coverage: "partial",
    },
    risk_class: "high",
    compatibility: {
      compatible: false,
      reasons: ["license evidence requires operator review"],
    },
    selection_state: "held",
    integration_tier: "upstream",
    source: "github",
    last_checked_at: now,
    updated_at: now,
  },
];

const discoveryRuns = [
  {
    id: "discovery-previous",
    trigger: "schedule",
    definition_commit: "e2e0001",
    policy_revision: 7,
    state: "completed",
    available_count: 2,
    selected_count: 0,
    actor: "scanner-release-scheduler",
    version: 1,
    started_at: "2026-07-30T11:55:00Z",
    completed_at: "2026-07-30T12:00:00Z",
    created_at: "2026-07-30T11:55:00Z",
    updated_at: "2026-07-30T12:00:00Z",
  },
];

function candidate(
  state: string,
  approvals: Array<Record<string, unknown>> = [],
) {
  const vulnerabilityExcepted = approvals.some(
    (approval) =>
      approval.action === "exception" &&
      approval.exception_scope === "vulnerability",
  );
  return {
    id: "candidate-1",
    state,
    risk: "low",
    risk_summary: {
      highest_risk: "low",
      reasons: ["one compatible scanner tool update"],
      changed_components: 1,
    },
    definition_commit: "e2e0001",
    proposed_commit: "e2e0002",
    proposal_url: "https://github.com/example/wolf/pull/314",
    lock_digest: "sha256:lock0001",
    lock_uri: "artifact://candidate-1/scanner-lock.yaml",
    policy_decision: `sha256:${"d".repeat(64)}`,
    publication_receipt_digest: `sha256:${"e".repeat(64)}`,
    policy_revision: 7,
    actor: "security.admin@example.test",
    version: state === "published" ? 5 : state === "approved" ? 4 : 3,
    created_at: "2026-07-30T12:01:00Z",
    updated_at: now,
    changes: [updates[0]],
    gates: [
      {
        name: "parser",
        state: "passed",
        summary: "Parser contract regression suite passed",
        evidence_uri: "https://github.com/example/wolf/actions/runs/1234",
      },
      {
        name: "vulnerability",
        state: vulnerabilityExcepted ? "excepted" : "failed",
        excepted: vulnerabilityExcepted,
        summary:
          'Review required; token="dckr_pat_candidate_summary_never_render" was present in upstream output.',
        evidence_digest: `sha256:${"a".repeat(64)}`,
        evidence_uri: "javascript:alert('unsafe-evidence-link')",
      },
      {
        name: "signature",
        state: "passed",
        summary: "Signature verified",
      },
      {
        name: "provenance",
        state: "passed",
        summary: "SLSA provenance verified",
      },
    ],
    build_steps: [
      {
        id: "step-1",
        step_key: "build",
        state: "completed",
        attempt: 1,
        summary: "All scanner images built",
        started_at: "2026-07-30T12:01:00Z",
        completed_at: "2026-07-30T12:04:00Z",
      },
    ],
    required_gates: ["parser", "signature", "provenance"],
    comparisons: {
      findings: {
        status: "passed",
        baseline: 42,
        candidate: 42,
        delta: 0,
      },
      performance: {
        status: "passed",
        baseline: 120,
        candidate: 118,
        delta: -2,
      },
    },
    signature: {
      state: "verified",
      identity: "scanner-release@wolf.example",
      digest: "sha256:signature0001",
      checked_at: now,
      total_count: 1,
      verified_count: 1,
      failed_count: 0,
      pending_count: 0,
      keys: ["linux-amd64"],
      digests: ["sha256:signature0001"],
      detail: "All required image signatures verified.",
    },
    provenance: {
      state: "verified",
      digest: "sha256:provenance0001",
      checked_at: now,
      total_count: 1,
      verified_count: 1,
      failed_count: 0,
      pending_count: 0,
      keys: ["linux-amd64"],
      digests: ["sha256:provenance0001"],
      detail: "All required image provenance verified.",
    },
    separation_of_duties: {
      creator: "scanner-release-scheduler",
      current_actor_can_approve: true,
      required_approvals: 1,
      valid_approvals: state === "approved" ? 1 : 0,
    },
    approvals,
  };
}

const release = {
  id: releaseId,
  name: "scanner-set-2026.31.1",
  state: "published",
  channels: ["candidate"],
  definition_commit: "e2e0002",
  lock_digest: "sha256:lock0001",
  manifest_digest: "sha256:manifest0001",
  signer_identity: "scanner-release@wolf.example",
  platforms: ["linux/amd64", "linux/arm64"],
  rollout_coverage: 0.1,
  published_at: "2026-07-30T12:05:00Z",
  rollback_eligible: true,
  protected: true,
  version: 2,
  policy_revision: 7,
};

const policySchedule = {
  timezone: "America/New_York",
  daily_discovery: {
    enabled: true,
    frequency: "daily",
    at: "02:00",
    jitter: "20m",
    catch_up: "6h",
  },
  weekly_candidate: {
    enabled: true,
    frequency: "weekly",
    weekday: "Sunday",
    at: "03:00",
    jitter: "20m",
    catch_up: "48h",
  },
  maintenance_windows: [
    {
      id: "weekly-security-maintenance",
      name: "Weekly security maintenance",
      cron: "0 3 * * 0",
      duration: "2h",
    },
  ],
};

const policyRules = {
  schema_version: "wolf.scanner-policy/v1",
  revision: 7,
  approval_mode: "manual",
  required_approvals: 1,
  separate_creator: true,
  auto_promote_risks: ["low"],
  auto_promote_changes: ["rebuild_only", "patch"],
  required_gates: [
    "lock",
    "artifacts",
    "platforms",
    "smoke",
    "parser",
    "vulnerability",
    "license",
    "sbom",
    "signature",
    "provenance",
    "source",
    "secret_scan",
    "compose",
    "kubernetes",
  ],
  allow_exceptions: { vulnerability: true, license: true },
  exception_max_age: "720h",
  canary: { size: 1, minimum_samples: 10, observation: "15m" },
  rollback: {
    automatic: true,
    max_infrastructure_failure_rate: 0.02,
    max_duration_regression: 0.2,
    max_parser_failures: 0,
  },
  retention: { artifacts: "2160h", logs: "720h" },
  notifications: {
    destinations: ["security-webhook", "release-email", "siem"],
  },
};

const activePolicy = {
  id: "scanner-policy-global",
  scope: "global",
  revision: 7,
  enabled: true,
  schedule: policySchedule,
  rules: policyRules,
  created_by: "security-platform@example.test",
  created_at: "2026-07-20T12:00:00Z",
  updated_at: now,
};

const policyRevisions = [
  activePolicy,
  {
    ...activePolicy,
    id: "scanner-policy-global-revision-6",
    revision: 6,
    enabled: false,
    rules: { ...policyRules, revision: 6, required_approvals: 1 },
    created_at: "2026-07-13T12:00:00Z",
    updated_at: "2026-07-13T12:00:00Z",
  },
];

const registryTargets = [
  {
    id: "registry-primary",
    name: "Managed primary",
    type: "managed",
    host: "ghcr.io",
    namespace: "wolf/scanners",
    enabled: true,
    trust_policy_reference: "trust-policy://***",
    platform_policy: {
      platforms: ["linux/amd64", "linux/arm64"],
    },
    version: 4,
    health: "healthy",
    last_checked_at: now,
    digest_parity: true,
    permissions: ["pull", "push", "referrers"],
    signer_identity: "scanner-release@wolf.example",
    protected_releases: 3,
    updated_at: now,
  },
  {
    id: "registry-mirror",
    name: "Docker Hub mirror",
    type: "mirror",
    host: "docker.io",
    namespace: "alphabravodevops",
    enabled: true,
    credential_reference_configured: true,
    credential_reference_kind: "wolf_secret",
    trust_policy_reference: "trust-policy://***",
    platform_policy: {
      platforms: ["linux/amd64", "linux/arm64"],
    },
    version: 7,
    health: "degraded",
    last_checked_at: now,
    mirror_lag_seconds: 420,
    digest_parity: false,
    permissions: ["pull", "push"],
    signer_identity: "scanner-release@wolf.example",
    protected_releases: 3,
    error:
      "authorization=dckr_pat_registry_target_never_render; raw destination response",
    updated_at: now,
  },
];

const registryJobs = [
  {
    id: "registry-job-completed",
    registry_target_id: "registry-mirror",
    source_registry_target_id: "registry-primary",
    release_id: releaseId,
    kind: "repair",
    re_sign_policy: "preserve",
    state: "completed",
    actor: "security.admin@example.test",
    reason: "Restore the exact verified mirror closure",
    attempt: 1,
    max_attempts: 5,
    available_at: "2026-07-30T12:00:00Z",
    version: 4,
    started_at: "2026-07-30T12:01:00Z",
    completed_at: now,
    created_at: "2026-07-30T12:00:00Z",
    updated_at: now,
    summary:
      '{"credential":"dckr_pat_registry_summary_never_render","raw":"never render"}',
  },
  {
    id: "registry-job-dead",
    registry_target_id: "registry-mirror",
    release_id: releaseId,
    kind: "reconcile",
    re_sign_policy: "preserve",
    state: "dead_letter",
    actor: "scanner-release-scheduler",
    reason: "Scheduled exact mirror verification",
    attempt: 5,
    max_attempts: 5,
    available_at: "2026-07-30T10:00:00Z",
    error_class: "registry_unavailable",
    error_detail:
      "authorization=dckr_pat_registry_error_never_render raw upstream response",
    version: 9,
    started_at: "2026-07-30T10:00:00Z",
    dead_lettered_at: "2026-07-30T10:15:00Z",
    created_at: "2026-07-30T10:00:00Z",
    updated_at: "2026-07-30T10:15:00Z",
  },
];

const customBuilds = [
  {
    id: "custom-build-running",
    user_id: "user-never-render",
    variants: '["default"]',
    push: false,
    platforms: '["linux/amd64"]',
    namespace: "",
    reserved_version: "2.0.1",
    state: "running",
    actor: "security.admin@example.test",
    reason: "Refresh the local default scanner",
    idempotency_key: "idempotency-never-render",
    request_digest: "request-digest-never-render",
    worker_id: "worker-never-render",
    lease_expires_at: "2099-07-30T12:10:00Z",
    heartbeat_at: now,
    attempt: 1,
    max_attempts: 3,
    available_at: "2026-07-30T12:00:00Z",
    summary: '{"secret":"dckr_pat_build_summary_never_render"}',
    version: 2,
    started_at: "2026-07-30T12:01:00Z",
    created_at: "2026-07-30T12:00:00Z",
    updated_at: now,
  },
  {
    id: "custom-build-partial-all",
    user_id: "user-never-render",
    variants: '["default","jvm","rust","codeql"]',
    push: false,
    platforms: '["linux/amd64"]',
    namespace: "",
    reserved_version: "2.0.1",
    state: "partial",
    actor: "security.admin@example.test",
    reason: "Weekly all-variant scanner refresh",
    idempotency_key: "idempotency-never-render",
    request_digest: "request-digest-never-render",
    worker_id: "worker-never-render",
    attempt: 1,
    max_attempts: 3,
    available_at: "2026-07-30T11:00:00Z",
    error_class: "variant_build_failed",
    error_detail: "authorization=dckr_pat_build_error_never_render",
    summary: '{"secret":"dckr_pat_build_summary_never_render"}',
    version: 4,
    started_at: "2026-07-30T11:01:00Z",
    completed_at: now,
    created_at: "2026-07-30T11:00:00Z",
    updated_at: now,
  },
];

const customBuildVariantRecords: Record<
  string,
  Array<Record<string, unknown>>
> = {
  "custom-build-running": [
    {
      id: "custom-build-running-default",
      build_id: "custom-build-running",
      variant: "default",
      ordinal: 0,
      state: "running",
      refs: "[]",
      loaded_locally: false,
      pushed: false,
      version: 2,
      started_at: "2026-07-30T12:01:00Z",
      created_at: "2026-07-30T12:00:00Z",
      updated_at: now,
    },
  ],
  "custom-build-partial-all": [
    {
      id: "custom-build-partial-default",
      build_id: "custom-build-partial-all",
      variant: "default",
      ordinal: 0,
      state: "completed",
      refs: '["docker.io/alphabravodevops/wolf-scanners:2.0.1"]',
      digest: "sha256:custom-default",
      loaded_locally: true,
      pushed: false,
      version: 2,
      completed_at: now,
    },
    {
      id: "custom-build-partial-jvm",
      build_id: "custom-build-partial-all",
      variant: "jvm",
      ordinal: 1,
      state: "completed",
      refs: '["docker.io/alphabravodevops/wolf-scanners-jvm:2.0.1"]',
      digest: "sha256:custom-jvm",
      loaded_locally: true,
      pushed: false,
      version: 2,
      completed_at: now,
    },
    {
      id: "custom-build-partial-rust",
      build_id: "custom-build-partial-all",
      variant: "rust",
      ordinal: 2,
      state: "completed",
      refs: '["docker.io/alphabravodevops/wolf-scanners-rust:2.0.1"]',
      digest: "sha256:custom-rust",
      loaded_locally: true,
      pushed: false,
      version: 2,
      completed_at: now,
    },
    {
      id: "custom-build-partial-codeql",
      build_id: "custom-build-partial-all",
      variant: "codeql",
      ordinal: 3,
      state: "failed",
      refs: "[]",
      loaded_locally: false,
      pushed: false,
      error_class: "buildx_unavailable",
      error_detail: "token=dckr_pat_variant_never_render",
      version: 2,
      completed_at: now,
    },
  ],
};

const registryImageEvidence = [
  {
    id: "registry-image-semgrep",
    job_id: "registry-job-completed",
    image_key: "semgrep",
    source_reference: "ghcr.io/wolf/scanners/semgrep@sha256:manifest-semgrep",
    destination_reference:
      "docker.io/alphabravodevops/semgrep@sha256:manifest-semgrep",
    expected_digest: "sha256:manifest-semgrep",
    source_digest: "sha256:manifest-semgrep",
    destination_digest: "sha256:manifest-semgrep",
    expected_signature_digest: "sha256:signature-semgrep",
    destination_signature_digest: "sha256:signature-semgrep",
    expected_provenance_digest: "sha256:provenance-semgrep",
    destination_provenance_digest: "sha256:provenance-semgrep",
    expected_sbom_digest: "sha256:sbom-semgrep",
    destination_sbom_digest: "sha256:sbom-semgrep",
    state: "verified",
    detail: '{"secret":"dckr_pat_registry_image_detail_never_render"}',
    checked_at: now,
    created_at: "2026-07-30T12:01:00Z",
    updated_at: now,
  },
];

const registryQuarantine = [
  {
    id: "quarantine-eligible",
    registry_target_id: "registry-mirror",
    repository: "alphabravodevops/wolf-scanners-partial",
    digest: "sha256:orphaned-partial",
    object_kind: "manifest",
    state: "orphaned",
    protected: false,
    retention_class: "quarantine",
    retain_until: "2026-07-29T12:00:00Z",
    discovered_at: "2026-07-25T12:00:00Z",
    last_referenced_at: "2026-07-25T12:00:00Z",
    metadata: '{"credential":"dckr_pat_quarantine_metadata_never_render"}',
    version: 2,
    created_at: "2026-07-25T12:00:00Z",
    updated_at: now,
  },
  {
    id: "quarantine-protected",
    registry_target_id: "registry-mirror",
    candidate_id: "candidate-1",
    repository: "alphabravodevops/wolf-scanners-retained",
    digest: "sha256:protected-release-evidence",
    object_kind: "manifest",
    state: "retained",
    protected: true,
    retention_class: "release",
    retain_until: "2027-07-30T12:00:00Z",
    discovered_at: "2026-07-30T11:00:00Z",
    last_referenced_at: now,
    version: 3,
    created_at: "2026-07-30T11:00:00Z",
    updated_at: now,
  },
];

const registryJobEventStream = [
  "id: 1",
  "event: scanner.registry_job.created",
  `data: ${JSON.stringify({
    id: "registry-event-1",
    aggregate_type: "registry_job",
    aggregate_id: "registry-job-completed",
    sequence: 1,
    event_type: "scanner.registry_job.created",
    new_state: "queued",
    actor: "security.admin@example.test",
    reason: "Restore the exact verified mirror closure",
    trace_id: "fedcba9876543210fedcba9876543210",
    operation_id: "op_registry_repair_0001",
    payload: {
      credential: "dckr_pat_registry_event_never_render",
    },
    created_at: "2026-07-30T12:00:00Z",
  })}`,
  "",
  "id: 2",
  "event: scanner.registry_job.completed",
  `data: ${JSON.stringify({
    id: "registry-event-2",
    aggregate_type: "registry_job",
    aggregate_id: "registry-job-completed",
    sequence: 2,
    event_type: "scanner.registry_job.completed",
    prior_state: "claimed",
    new_state: "completed",
    actor: "scanner-registry-worker",
    reason: "Exact destination closure verified",
    trace_id: "fedcba9876543210fedcba9876543210",
    operation_id: "op_registry_repair_0001",
    created_at: now,
  })}`,
  "",
  "",
].join("\n");

const signerProfiles: SignerFixture[] = [
  {
    id: "signer-aws-1",
    name: "Production AWS signer",
    provider: "aws_kms",
    algorithm: "ecdsa-p256-sha256",
    key_reference: "aws-kms://***",
    secret_reference_configured: false,
    workload_identity: true,
    identity: "arn:aws:iam::123456789012:role/wolf-release",
    issuer: "https://sts.amazonaws.com",
    subject: "arn:aws:iam::123456789012:role/wolf-release",
    trust_root_reference: "kubernetes://***",
    state: "active",
    revision: 1,
    created_by: "security.admin@example.test",
    created_at: "2026-07-30T10:00:00Z",
    updated_at: "2026-07-30T10:00:00Z",
  },
  {
    id: "managed-keyless",
    name: "Wolf managed keyless",
    provider: "managed_keyless",
    algorithm: "cosign-keyless",
    key_reference: "managed-keyless://***",
    secret_reference_configured: false,
    workload_identity: true,
    identity: "scanner-release@wolf.example",
    issuer: "https://token.actions.githubusercontent.com",
    subject: "repo:example/wolf:ref:refs/heads/main",
    trust_root_reference: "kubernetes://***",
    state: "active",
    revision: 8,
    created_by: "deployment",
    created_at: "2026-07-29T10:00:00Z",
    updated_at: "2026-07-30T10:00:00Z",
  },
];

function signerFromInput(
  input: SignerInputFixture,
  id: string,
  revision: number,
): SignerFixture {
  return {
    id,
    name: input.name,
    provider: input.provider,
    algorithm: input.algorithm,
    key_reference: maskReference(input.key_reference),
    secret_reference: input.secret_reference
      ? maskReference(input.secret_reference)
      : undefined,
    secret_reference_configured: Boolean(input.secret_reference),
    workload_identity: input.workload_identity,
    identity: input.identity,
    issuer: input.issuer,
    subject: input.subject,
    trust_root_reference: maskReference(input.trust_root_reference),
    state: "active",
    revision,
    created_by: "security.admin@example.test",
    created_at: now,
    updated_at: now,
  };
}

function maskReference(value: string): string {
  const protocol = value.indexOf("://");
  if (protocol >= 0) return `${value.slice(0, protocol + 3)}***`;
  const colon = value.indexOf(":");
  return colon >= 0 ? `${value.slice(0, colon + 1)}***` : "***";
}

const sourceScan = {
  id: "scan-source",
  user_id: "user-admin",
  repo_id: "repo-1",
  branch: "main",
  status: "completed",
  tools_selected: '["semgrep","trivy"]',
  tools_completed: '["semgrep","trivy"]',
  tools_running: "[]",
  tools_failed: "[]",
  finding_count: 3,
  scanner_release_id: "release-stable",
  release_manifest_digest: "sha256:manifest-stable",
  created_at: "2026-07-30T11:00:00Z",
  started_at: "2026-07-30T11:00:05Z",
  completed_at: "2026-07-30T11:02:00Z",
  updated_at: "2026-07-30T11:02:00Z",
  repo: {
    id: "repo-1",
    user_id: "user-admin",
    name: "payments-service",
  },
};

const apiCreatedRepository = {
  id: "repo-api-created",
  user_id: "user-admin",
  name: "API imported repository",
  source_type: "git",
  source_path: "https://git.example.test/platform/payments.git",
  default_branch: "main",
  detected_languages: '["python"]',
  detected_frameworks: "[]",
  detected_at: now,
  created_at: now,
  updated_at: now,
};

const apiCreatedScan = {
  id: "scan-api-created",
  user_id: "user-admin",
  repo_id: apiCreatedRepository.id,
  branch: "main",
  status: "pending",
  tools_selected: '["semgrep","trivy"]',
  tools_completed: '["semgrep"]',
  tools_running: '["trivy"]',
  tools_failed: "[]",
  finding_count: 1,
  scanner_release_id: "release-stable",
  release_manifest_digest: "sha256:manifest-stable",
  created_at: now,
  started_at: now,
  updated_at: now,
  repo: apiCreatedRepository,
};

const apiCreatedFinding = {
  id: "finding-api-created",
  scan_id: apiCreatedScan.id,
  repo_id: apiCreatedRepository.id,
  fingerprint: "finding-fingerprint-e2e",
  tool_name: "semgrep",
  category: "sast",
  severity: "high",
  tool_severity_score: 4,
  location_weight: 1,
  ai_context_score: 0,
  composite_score: 4,
  title: "Command injection from request input",
  description: "Untrusted input reaches a shell command.",
  file_path: "service/handler.py",
  line_start: 42,
  line_end: 42,
  code_snippet: "subprocess.run(user_input, shell=True)",
  status: "open",
  rule_id: "python.lang.security.audit.subprocess-shell-true",
  created_at: now,
  updated_at: now,
};

const legacyImportResult = {
  release: {
    id: "legacy-release-config",
    name: "legacy-config-e2e000000001",
    state: "published",
    manifest_digest: "sha256:legacyconfig0001",
    signer_identity: "legacy-unverified",
    published_at: now,
    legacy: true,
    imported: true,
    protected: true,
    rollback_eligible: false,
    retention_class: "legacy",
  },
  images: [
    {
      image_key: "default",
      repository: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
      digest: `sha256:${"a".repeat(64)}`,
      signature_status: "legacy_unverified",
    },
    {
      image_key: "wolf-semgrep",
      repository: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
      digest: `sha256:${"b".repeat(64)}`,
      signature_status: "legacy_unverified",
    },
  ],
  created: true,
  provenance_limitations: [
    "image signatures were not verified by the managed release pipeline",
    "SBOM and build provenance are unavailable",
    "the snapshot is historical evidence and is not rollout eligible",
  ],
  runtime_assignments_changed: false,
};

const releaseDetail = {
  ...release,
  tools: [
    {
      tool_key: "semgrep",
      version: "1.128.0",
      source_reference: "github:semgrep/semgrep@v1.128.0",
      source_digest: "sha256:source0001",
      checksum: "sha256:tool0001",
      parser_compatibility: "passed",
    },
  ],
  images: [
    {
      image_key: "default",
      repository: "ghcr.io/alphabravocompany/wolf-scanners",
      digest: "sha256:image0001",
      signature_status: "verified",
      provenance_digest: "sha256:provenance0001",
      sbom_digest: "sha256:sbom0001",
    },
  ],
  artifacts: [
    {
      id: "artifact-manifest",
      artifact_type: "manifest_diff",
      media_type: "text/x-diff",
      uri: "artifact://release-next/manifest.diff",
      digest: "sha256:manifestdiff0001",
      size_bytes: 512,
      protected: true,
    },
    {
      id: "artifact-lock",
      artifact_type: "lock_diff",
      media_type: "text/x-diff",
      uri: "artifact://release-next/lock.diff",
      digest: "sha256:lockdiff0001",
      size_bytes: 384,
      protected: true,
    },
  ],
  verification: {
    registry: { state: "verified", detail: "Digest parity verified" },
    signature: { state: "verified", detail: "Signer trusted" },
    provenance: { state: "verified", detail: "Builder trusted" },
    mirrors: { state: "verified", detail: "Mirror parity verified" },
  },
};

const rollout = {
  id: "rollout-1",
  target: "production",
  from_release_id: "release-stable",
  to_release_id: releaseId,
  strategy: "canary",
  state: "paused",
  version: 5,
  started_at: "2026-07-30T12:05:00Z",
  created_at: "2026-07-30T12:05:00Z",
  updated_at: now,
  health: {
    outcome: "investigate",
    samples: 3,
    minimum_samples: 10,
    signature_failures: 1,
    reasons: ["one worker reported a signature verification failure"],
  },
};

const rolloutDetail = {
  ...rollout,
  cohorts: [
    {
      id: "cohort-canary",
      name: "canary",
      desired_release_id: releaseId,
      observed_release_id: releaseId,
      state: "paused",
      total_workers: 4,
      ready_workers: 3,
      failed_workers: 1,
      deadline: "2026-07-30T13:00:00Z",
    },
    {
      id: "cohort-stable",
      name: "stable",
      desired_release_id: "release-stable",
      observed_release_id: "release-stable",
      state: "held",
      total_workers: 20,
      ready_workers: 20,
      failed_workers: 0,
    },
  ],
  events: [
    {
      id: "event-1",
      sequence: 1,
      event_type: "rollout.created",
      new_state: "pending",
      actor: "security.admin@example.test",
      reason: "Promote verified release",
      created_at: "2026-07-30T12:05:00Z",
    },
    {
      id: "event-2",
      sequence: 2,
      event_type: "rollout.paused",
      prior_state: "canary",
      new_state: "paused",
      actor: "scanner-rollout-controller",
      reason: "signature verification threshold",
      created_at: now,
    },
  ],
  synthetic_health: {
    corpus_id: "synthetic-corpus-internal-id-never-render",
    corpus_digest:
      "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    current: false,
    state: "passed",
    fixture_total: 24,
    fixture_passed: 24,
    fixture_failed: 0,
    failure_class: "",
    observed_at: now,
  },
  real_scan_health: {
    state: "degraded",
    candidate_samples: 12,
    stable_samples: 30,
    candidate_infrastructure_failures: 2,
    stable_infrastructure_failures: 0,
    parser_failures: 1,
    expected_finding_losses: 1,
    candidate_p95_duration_ms: 142_000,
    stable_p95_duration_ms: 118_000,
    workers_total: 4,
    workers_ready: 3,
    workers_failed: 1,
    observed_at: now,
  },
  maintenance_window: {
    open: true,
    name: "weekly scanner maintenance",
  },
  affected_workers: 4,
  recommendation:
    "Review the failed worker signature evidence before resuming or roll back.",
};

function notificationDeadLetter(state: string) {
  return {
    id: "notification-dead",
    event_id: "event-notification-dead",
    aggregate_type: "release",
    aggregate_id: releaseId,
    event_type: "scanner.release.health_issue",
    notification_type: "stable_release_health_issue",
    destination_type: "webhook",
    destination_ref: "security-operations",
    policy_id: "default",
    policy_revision: 7,
    state,
    payload:
      '{"html":"<script>alert(1)</script>","secret":"dckr_pat_never_render"}',
    attempt: state === "retry" ? 0 : 5,
    max_attempts: 5,
    available_at: now,
    dead_lettered_at: state === "dead_letter" ? now : undefined,
    error_class:
      state === "dead_letter" ? "destination_unavailable" : undefined,
    error_detail:
      state === "dead_letter"
        ? "Endpoint returned 503 after bounded retries."
        : undefined,
    version: state === "retry" ? 8 : 7,
    created_at: "2026-07-30T12:00:00Z",
    updated_at: now,
  };
}

const notificationUi = {
  id: "notification-ui",
  event_id: "event-notification-ui",
  aggregate_type: "candidate",
  aggregate_id: "candidate-1",
  event_type: "scanner.candidate.ready",
  notification_type: "candidate_ready_for_approval",
  destination_type: "ui",
  destination_ref: "wolf-ui",
  policy_id: "default",
  policy_revision: 7,
  state: "delivered",
  payload: '{"secret":"never render this value"}',
  attempt: 1,
  max_attempts: 5,
  available_at: now,
  delivered_at: now,
  version: 2,
  created_at: "2026-07-30T12:02:00Z",
  updated_at: now,
};

const scannerAlertOpen = {
  id: "alert-rollout",
  fingerprint: "fingerprint-rollout",
  kind: "rollout_failure",
  severity: "critical",
  state: "open",
  scope_type: "rollout_target",
  scope_id: "production",
  summary: "The latest production scanner rollout failed or rolled back.",
  evidence: {
    rollout_id: "rollout-1",
    state: "failed",
    secret: "dckr_pat_alert_never_render",
    nested: { html: "<script>alert(1)</script>" },
  },
  policy_id: "default",
  policy_revision: 7,
  trigger_count: 4,
  generation: 2,
  version: 5,
  first_triggered_at: "2026-07-30T10:00:00Z",
  last_triggered_at: now,
  created_at: "2026-07-30T10:00:00Z",
  updated_at: now,
};

const scannerAlertResolved = {
  id: "alert-discovery",
  fingerprint: "fingerprint-discovery",
  kind: "missed_discovery",
  severity: "warning",
  state: "resolved",
  scope_type: "policy_scope",
  scope_id: "global",
  summary: "Scanner discovery exceeded its configured maximum age.",
  evidence: {
    age_seconds: 90_000,
    threshold_seconds: 86_400,
    last_completed_at: "2026-07-29T10:00:00Z",
  },
  policy_id: "default",
  policy_revision: 7,
  trigger_count: 8,
  generation: 3,
  version: 9,
  first_triggered_at: "2026-07-28T10:00:00Z",
  last_triggered_at: "2026-07-30T11:45:00Z",
  resolved_at: now,
  created_at: "2026-07-28T10:00:00Z",
  updated_at: now,
};

function artifactDiff(kind: "manifest" | "lock") {
  const content =
    kind === "manifest"
      ? [
          "--- scanner-manifest.yaml",
          "+++ scanner-manifest.yaml",
          "@@ -4,3 +4,3 @@",
          "-  semgrep: 1.127.0",
          "+  semgrep: 1.128.0",
        ].join("\n")
      : [
          "--- scanner-lock.yaml",
          "+++ scanner-lock.yaml",
          "@@ -8,2 +8,2 @@",
          "-  checksum: sha256:old",
          "+  checksum: sha256:tool0001",
        ].join("\n");
  return {
    owner_type: "candidate",
    owner_id: "candidate-1",
    kind,
    format: "unified",
    available: true,
    content,
    truncated: kind === "manifest",
    total_bytes: kind === "manifest" ? 2_048 : content.length,
    returned_bytes: content.length,
    total_lines: kind === "manifest" ? 80 : 5,
    returned_lines: 5,
    digest: `sha256:${kind}diff0001`,
    media_type: "text/x-diff",
  };
}

const auditTraceId = "0123456789abcdef0123456789abcdef";
const auditEvents = [
  {
    id: "audit-build-complete",
    sequence: 42,
    aggregate_type: "candidate",
    aggregate_id: "candidate-1",
    trace_id: auditTraceId,
    operation_id: "op_build_release_0001",
    parent_operation_id: "op_candidate_root_0001",
    event_type: "scanner.build.completed",
    prior_state: "building",
    new_state: "completed",
    actor: "scanner-build-worker",
    reason: "All bounded release gates completed",
    payload: {
      credential: "dckr_pat_audit_never_render",
      repository_url: "https://credentials.invalid/private/repository",
    },
    created_at: "2026-07-30T12:05:00Z",
  },
  {
    id: "audit-candidate-created",
    sequence: 41,
    aggregate_type: "candidate",
    aggregate_id: "candidate-1",
    trace_id: auditTraceId,
    operation_id: "op_candidate_root_0001",
    event_type: "scanner.candidate.created",
    new_state: "queued",
    actor: "security.admin@example.test",
    reason: "Approved update selection entered the release factory",
    created_at: "2026-07-30T12:00:00Z",
  },
];

const scannerImages = [
  {
    image: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
    local_digest: "sha256:defaultlocal",
    remote_digest: "sha256:defaultremote",
    updates_available: true,
  },
  {
    image: "docker.io/alphabravodevops/wolf-scanners-jvm:2.0.0",
    local_digest: "sha256:jvm",
    remote_digest: "sha256:jvm",
    updates_available: false,
  },
  {
    image: "docker.io/alphabravodevops/wolf-scanners-rust:2.0.0",
    local_digest: "sha256:rust",
    remote_digest: "sha256:rust",
    updates_available: false,
  },
  {
    image: "docker.io/alphabravodevops/wolf-scanners-codeql:2.0.0",
    local_digest: "sha256:codeql",
    updates_available: false,
  },
];

const scannerTools = [
  {
    name: "semgrep",
    display_name: "Semgrep",
    category: "SAST",
    integration_tier: "default",
    pinned_version: "1.127.0",
    latest_version: "1.128.0",
    latest_reference: "v1.128.0",
    freshness_status: "update_available",
    version_checked_at: now,
    canonical_image: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
    configured_image: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
    image_present: true,
    overridden: false,
    uses_latest_tag: false,
  },
  {
    name: "trivy",
    display_name: "Trivy",
    category: "SCA",
    integration_tier: "default",
    pinned_version: "0.64.1",
    latest_version: "0.64.1",
    latest_reference: "v0.64.1",
    freshness_status: "current",
    version_checked_at: now,
    canonical_image: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
    configured_image: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
    image_present: true,
    overridden: false,
    uses_latest_tag: false,
  },
];

function queuedVariantRecords(
  build: Record<string, unknown>,
): Array<Record<string, unknown>> {
  let variants: string[] = [];
  try {
    variants =
      typeof build.variants === "string"
        ? (JSON.parse(build.variants) as string[])
        : [];
  } catch {
    variants = [];
  }
  return variants.map((variant, ordinal) => ({
    id: `${String(build.id)}-${variant}`,
    build_id: build.id,
    variant,
    ordinal,
    state: "queued",
    refs: "[]",
    loaded_locally: false,
    pushed: false,
    version: 1,
    created_at: now,
    updated_at: now,
  }));
}

function cloneFixture<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

async function json(
  route: Route,
  data: unknown,
  status = 200,
  headers?: Record<string, string>,
) {
  await route.fulfill({
    status,
    contentType: "application/json",
    headers,
    body: JSON.stringify({ data }),
  });
}

async function jsonError(
  route: Route,
  status: number,
  code: string,
  message: string,
) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify({ error: { code, message } }),
  });
}

export function recorded(
  state: ScannerApiState,
  method: string,
  pathname: string,
): RecordedRequest[] {
  return state.requests.filter(
    (request) => request.method === method && request.pathname === pathname,
  );
}
