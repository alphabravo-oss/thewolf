import { afterEach, describe, expect, it, vi } from "vitest";
import {
  errorKind,
  isValidScannerOperationId,
  isValidScannerTraceId,
  normalizePage,
  parseJson,
  scannerSupplyChainApi,
} from "./scanner-supply-chain";
import { ApiError, api } from "./api";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("scanner supply-chain client adapters", () => {
  it("normalizes the cursor envelope emitted by list endpoints", () => {
    expect(
      normalizePage({
        data: [{ id: "release-1" }],
        meta: { next_cursor: "cursor-2", total: 12 },
      }),
    ).toEqual({
      items: [{ id: "release-1" }],
      next_cursor: "cursor-2",
      total: 12,
    });
  });

  it("accepts direct arrays and canonical pages during rolling upgrades", () => {
    expect(normalizePage([{ id: "one" }])).toEqual({
      items: [{ id: "one" }],
    });
    expect(
      normalizePage({ items: [{ id: "two" }], next_cursor: "next" }),
    ).toEqual({
      items: [{ id: "two" }],
      next_cursor: "next",
    });
  });

  it("safely parses persisted JSON fields without hiding malformed data", () => {
    expect(parseJson('{"compatible":true}', { compatible: false })).toEqual({
      compatible: true,
    });
    expect(parseJson("{broken", { compatible: false })).toEqual({
      compatible: false,
    });
  });

  it("classifies permission, availability, and stale-resource failures", () => {
    expect(errorKind(new ApiError(403, "FORBIDDEN", "no"))).toBe("forbidden");
    expect(errorKind(new ApiError(503, "UNAVAILABLE", "down"))).toBe(
      "unavailable",
    );
    expect(errorKind(new ApiError(412, "STALE_REVISION", "stale"))).toBe(
      "stale",
    );
  });

  it("accepts only bounded exact scanner correlation identifiers", () => {
    expect(
      isValidScannerTraceId("0123456789abcdef0123456789abcdef"),
    ).toBe(true);
    expect(
      isValidScannerTraceId("00000000000000000000000000000000"),
    ).toBe(false);
    expect(isValidScannerTraceId("0123456789ABCDEF")).toBe(false);
    expect(isValidScannerOperationId("op_build_release_0001")).toBe(true);
    expect(isValidScannerOperationId("short")).toBe(false);
    expect(isValidScannerOperationId("op_invalid?credential=value")).toBe(
      false,
    );
  });

  it("normalizes top-level durable operation receipts through the shared client", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 202,
        json: async () => ({
          id: "operation-1",
          state: "queued",
          status_url: "/operations/operation-1",
        }),
      }),
    );

    await expect(api.post("/v1/scanner-supply-chain/discovery-runs", {})).resolves.toEqual({
      data: {
        id: "operation-1",
        state: "queued",
        status_url: "/operations/operation-1",
      },
    });
  });

  it("exposes response metadata without changing the normalized API envelope", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        headers: new Headers({ ETag: '"resource-version-7"' }),
        json: async () => ({
          data: { id: "notification-1", version: 7 },
        }),
      }),
    );

    const result = await api.getWithMetadata<{
      id: string;
      version: number;
    }>("/v1/scanner-supply-chain/notifications/notification-1");

    expect(result.response).toEqual({
      data: { id: "notification-1", version: 7 },
    });
    expect(result.headers.get("ETag")).toBe('"resource-version-7"');
    expect(result.status).toBe(200);
  });

  it("preserves cursor metadata from the backend list envelope", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      data: [
        {
          id: "release-1",
          name: "scanner-set-1",
          state: "published",
          published_at: "2026-07-30T12:00:00Z",
        },
      ],
      meta: { next_cursor: "release-1" },
    });

    await expect(scannerSupplyChainApi.releases({ limit: 1 })).resolves.toEqual({
      items: [
        {
          id: "release-1",
          name: "scanner-set-1",
          state: "published",
          published_at: "2026-07-30T12:00:00Z",
        },
      ],
      next_cursor: "release-1",
    });
  });

  it("applies bounded notification filters and preserves cursor metadata", async () => {
    const notification = {
      id: "notification-1",
      event_id: "event-1",
      aggregate_type: "candidate",
      aggregate_id: "candidate-1",
      event_type: "scanner.candidate.ready",
      notification_type: "candidate_ready_for_approval",
      destination_type: "ui" as const,
      destination_ref: "wolf-ui",
      state: "delivered" as const,
      payload: "{\"secret\":\"never-render\"}",
      attempt: 1,
      max_attempts: 5,
      available_at: "2026-07-30T12:00:00Z",
      version: 4,
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:01:00Z",
    };
    const get = vi.spyOn(api, "get").mockResolvedValue({
      data: [notification],
      meta: { next_cursor: "notification-1" },
    });

    await expect(
      scannerSupplyChainApi.notifications({
        state: "delivered",
        destination_type: "ui",
        notification_type: "candidate_ready_for_approval",
        cursor: "cursor-1",
        limit: 25,
      }),
    ).resolves.toEqual({
      items: [notification],
      next_cursor: "notification-1",
    });
    expect(get).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/notifications?state=delivered&destination_type=ui&notification_type=candidate_ready_for_approval&cursor=cursor-1&limit=25",
    );
  });

  it("uses the exact response ETag and a fresh idempotency key for notification retry", async () => {
    const notification = {
      id: "notification/dead",
      event_id: "event-1",
      aggregate_type: "release",
      aggregate_id: "release-1",
      event_type: "scanner.release.health_issue",
      notification_type: "stable_release_health_issue",
      destination_type: "webhook" as const,
      destination_ref: "security-operations",
      state: "dead_letter" as const,
      payload: "{}",
      attempt: 5,
      max_attempts: 5,
      available_at: "2026-07-30T12:00:00Z",
      version: 7,
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:01:00Z",
    };
    const get = vi.spyOn(api, "getWithMetadata").mockResolvedValue({
      response: { data: notification },
      headers: new Headers({ ETag: '"notification-version-7"' }),
      status: 200,
    });
    const post = vi.spyOn(api, "post").mockResolvedValue({
      data: { ...notification, state: "retry", version: 8 },
    });

    await expect(
      scannerSupplyChainApi.notification("notification/dead"),
    ).resolves.toEqual({
      notification,
      etag: '"notification-version-7"',
    });
    await scannerSupplyChainApi.retryNotification(
      "notification/dead",
      "routing repaired",
      '"notification-version-7"',
    );

    expect(get).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/notifications/notification%2Fdead",
    );
    expect(post).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/notifications/notification%2Fdead/retry",
      { reason: "routing repaired" },
      {
        headers: {
          "Idempotency-Key": expect.stringMatching(/^wolf-ui-/),
          "If-Match": '"notification-version-7"',
        },
      },
    );
  });

  it("previews legacy configuration and preserves the caller idempotency key during import", async () => {
    const get = vi.spyOn(api, "get").mockImplementation(async (path) => {
      if (path === "/scanners/config") {
        return {
          data: {
            image: "registry.example/wolf-scanners:2.0.0",
            image_overrides: {
              semgrep: "registry.example/wolf-semgrep@sha256:abc",
            },
          },
        };
      }
      if (path === "/scanners/tools") {
        return {
          data: [
            {
              name: "trivy",
              integration_tier: "upstream",
              configured_image: "aquasec/trivy:0.64.1",
            },
          ],
        };
      }
      throw new Error(`unexpected GET ${path}`);
    });
    const result = {
      release: {
        id: "legacy-release-1",
        name: "legacy-config-abc",
        state: "published",
        manifest_digest: `sha256:${"a".repeat(64)}`,
        signer_identity: "legacy-unverified",
        published_at: "2026-07-30T12:00:00Z",
        legacy: true,
        imported: true,
        protected: true,
        rollback_eligible: false,
      },
      images: [],
      created: true,
      provenance_limitations: ["SBOM and build provenance are unavailable"],
      runtime_assignments_changed: false as const,
    };
    const post = vi.spyOn(api, "post").mockResolvedValue({ data: result });

    await expect(scannerSupplyChainApi.legacyConfiguration()).resolves.toEqual({
      config: {
        image: "registry.example/wolf-scanners:2.0.0",
        image_overrides: {
          semgrep: "registry.example/wolf-semgrep@sha256:abc",
        },
      },
      tools: [
        {
          name: "trivy",
          integration_tier: "upstream",
          configured_image: "aquasec/trivy:0.64.1",
        },
      ],
    });
    expect(get).toHaveBeenCalledWith("/scanners/config");
    expect(get).toHaveBeenCalledWith("/scanners/tools");

    await expect(
      scannerSupplyChainApi.importLegacyConfiguration(
        {
          reason: "archive deployment state",
          resolved_digests: { default: `sha256:${"b".repeat(64)}` },
        },
        "legacy-import-key-1",
      ),
    ).resolves.toEqual(result);
    expect(post).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/legacy-release-imports",
      {
        reason: "archive deployment state",
        resolved_digests: { default: `sha256:${"b".repeat(64)}` },
      },
      { headers: { "Idempotency-Key": "legacy-import-key-1" } },
    );
  });

  it("creates an explicit release re-scan without changing the supplied idempotency key", async () => {
    const created = {
      id: "scan-new",
      user_id: "admin-1",
      repo_id: "repo-1",
      branch: "main",
      status: "pending" as const,
      tools_selected: "[]",
      tools_completed: "[]",
      tools_running: "[]",
      tools_failed: "[]",
      finding_count: 0,
      scanner_release_id: "release-next",
      release_manifest_digest: `sha256:${"c".repeat(64)}`,
      rescan_of_scan_id: "scan-source",
      release_selection_reason: "compare approved rules",
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:00:00Z",
    };
    const post = vi.spyOn(api, "post").mockResolvedValue({ data: created });

    await expect(
      scannerSupplyChainApi.createReleaseRescan(
        "scan/source",
        {
          release_id: "release-next",
          reason: "compare approved rules",
        },
        "release-rescan-key-1",
      ),
    ).resolves.toEqual(created);
    expect(post).toHaveBeenCalledWith(
      "/scans/scan%2Fsource/release-rescans",
      {
        release_id: "release-next",
        reason: "compare approved rules",
      },
      { headers: { "Idempotency-Key": "release-rescan-key-1" } },
    );
  });

  it("uses masked signer reads with exact ETag and stable command headers", async () => {
    const signer = {
      id: "signer/aws",
      name: "Production signer",
      provider: "aws_kms" as const,
      algorithm: "ecdsa-p256-sha256",
      key_reference: "aws-kms://***",
      secret_reference_configured: false,
      workload_identity: true,
      identity: "arn:aws:iam::123456789012:role/wolf-release",
      issuer: "https://sts.amazonaws.com",
      subject: "arn:aws:iam::123456789012:role/wolf-release",
      trust_root_reference: "kubernetes://***",
      state: "active" as const,
      revision: 4,
      created_by: "admin",
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:00:00Z",
    };
    const get = vi.spyOn(api, "getWithMetadata").mockResolvedValue({
      response: { data: signer },
      headers: new Headers({ ETag: '"4"' }),
      status: 200,
    });
    const post = vi.spyOn(api, "post").mockResolvedValue({ data: signer });
    const input = {
      name: "Production signer",
      provider: "aws_kms" as const,
      algorithm: "ecdsa-p256-sha256",
      key_reference:
        "aws-kms://us-east-1/123456789012/alias/wolf-release-next",
      workload_identity: true,
      identity: "arn:aws:iam::123456789012:role/wolf-release",
      issuer: "https://sts.amazonaws.com",
      subject: "arn:aws:iam::123456789012:role/wolf-release",
      trust_root_reference: "kubernetes://wolf-system/aws-kms-roots",
    };

    await expect(scannerSupplyChainApi.signer("signer/aws")).resolves.toEqual({
      signer,
      etag: '"4"',
    });
    expect(get).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/signers/signer%2Faws",
    );

    await scannerSupplyChainApi.rotateSigner(
      "signer/aws",
      input,
      '"4"',
      "signer-rotation-key",
    );
    expect(post).toHaveBeenLastCalledWith(
      "/v1/scanner-supply-chain/signers/signer%2Faws/rotate",
      input,
      {
        headers: {
          "Idempotency-Key": "signer-rotation-key",
          "If-Match": '"4"',
        },
      },
    );

    await scannerSupplyChainApi.revokeSigner(
      "signer/aws",
      "key retired",
      '"4"',
      "signer-revoke-key",
    );
    expect(post).toHaveBeenLastCalledWith(
      "/v1/scanner-supply-chain/signers/signer%2Faws/revoke",
      { reason: "key retired" },
      {
        headers: {
          "Idempotency-Key": "signer-revoke-key",
          "If-Match": '"4"',
        },
      },
    );
  });

  it("applies bounded alert lifecycle filters and reads alert detail safely", async () => {
    const alert = {
      id: "alert/critical",
      fingerprint: "fingerprint-1",
      kind: "rollout_failure" as const,
      severity: "critical" as const,
      state: "open" as const,
      scope_type: "rollout_target",
      scope_id: "production",
      summary: "The latest rollout failed.",
      evidence: { rollout_id: "rollout-1", state: "failed" },
      policy_id: "default",
      policy_revision: 7,
      trigger_count: 3,
      generation: 2,
      version: 4,
      first_triggered_at: "2026-07-30T10:00:00Z",
      last_triggered_at: "2026-07-30T12:00:00Z",
      created_at: "2026-07-30T10:00:00Z",
      updated_at: "2026-07-30T12:00:00Z",
    };
    const get = vi.spyOn(api, "get");
    get.mockResolvedValueOnce({
      data: [alert],
      meta: { next_cursor: "alert-2" },
    });
    get.mockResolvedValueOnce({ data: alert });

    await expect(
      scannerSupplyChainApi.alerts({
        state: "all",
        kind: "rollout_failure",
        severity: "critical",
        cursor: "cursor-1",
        limit: 25,
      }),
    ).resolves.toEqual({
      items: [alert],
      next_cursor: "alert-2",
    });
    await expect(
      scannerSupplyChainApi.alert("alert/critical"),
    ).resolves.toEqual(alert);

    expect(get).toHaveBeenNthCalledWith(
      1,
      "/v1/scanner-supply-chain/alerts?state=all&kind=rollout_failure&severity=critical&cursor=cursor-1&limit=25",
    );
    expect(get).toHaveBeenNthCalledWith(
      2,
      "/v1/scanner-supply-chain/alerts/alert%2Fcritical",
    );
  });

  it("uses the backend selected_items command field for candidate creation", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({
      data: { id: "candidate-1", state: "queued" },
    });

    await scannerSupplyChainApi.createCandidate(
      ["update-1", "update-2"],
      "approved selection",
      "discovery-1",
    );

    expect(post).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/candidates",
      {
        selected_items: ["update-1", "update-2"],
        reason: "approved selection",
        discovery_run_id: "discovery-1",
      },
      {
        headers: expect.objectContaining({
          "Idempotency-Key": expect.stringMatching(/^wolf-ui-/),
        }),
      },
    );
  });

  it("requests bounded diff content from the resource-scoped authenticated route", async () => {
    const get = vi.spyOn(api, "get").mockResolvedValue({
      data: {
        owner_type: "candidate",
        owner_id: "candidate/one",
        kind: "manifest",
        format: "unified",
        available: true,
        content: "+safe change\n",
        truncated: false,
        total_bytes: 13,
        returned_bytes: 13,
        total_lines: 1,
        returned_lines: 1,
      },
    });

    await expect(
      scannerSupplyChainApi.artifactDiff(
        "candidate",
        "candidate/one",
        "manifest",
      ),
    ).resolves.toMatchObject({
      owner_type: "candidate",
      kind: "manifest",
      content: "+safe change\n",
    });
    expect(get).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/candidates/candidate%2Fone/diffs/manifest",
    );
  });

  it("consumes the bounded release-factory health JSON instead of raw metrics", async () => {
    const releaseFactory = {
      status: "degraded",
      ready: false,
      database: "ok",
      uptime_ms: 120_000,
      components: [
        {
          component: "build",
          enabled: true,
          status: "stale_or_stuck",
          ready: false,
          stuck_work: { expired_lease: 1 },
        },
      ],
    };
    const get = vi.spyOn(api, "get").mockResolvedValue({
      data: {
        status: "ok",
        uptime_ms: 240_000,
        release_factory: releaseFactory,
      },
    });

    await expect(scannerSupplyChainApi.releaseFactoryHealth()).resolves.toEqual(
      releaseFactory,
    );
    expect(get).toHaveBeenCalledWith("/v1/health");
    expect(get).not.toHaveBeenCalledWith("/v1/metrics");
  });

  it("uses bounded registry filters, exact ETags, and idempotent durable commands", async () => {
    const job = {
      id: "registry/job-1",
      registry_target_id: "registry/mirror",
      release_id: "release-1",
      kind: "repair" as const,
      re_sign_policy: "preserve" as const,
      state: "dead_letter" as const,
      actor: "admin",
      reason: "repair drift",
      attempt: 5,
      max_attempts: 5,
      available_at: "2026-07-30T12:00:00Z",
      version: 7,
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:01:00Z",
    };
    const get = vi.spyOn(api, "get").mockResolvedValue({ data: [job] });
    const getDetail = vi.spyOn(api, "getWithMetadata").mockResolvedValue({
      response: {
        data: {
          job,
          images: [],
          events_url:
            "/v1/scanner-supply-chain/registry-jobs/registry%2Fjob-1/events",
        },
      },
      headers: new Headers({ ETag: '"7"' }),
      status: 200,
    });
    const post = vi.spyOn(api, "post").mockResolvedValue({
      data: { id: job.id, state: "queued" },
    });

    await scannerSupplyChainApi.registryJobs({
      registry_target_id: "registry/mirror",
      state: "dead_letter",
      kind: "repair",
      release_id: "release-1",
      limit: 25,
    });
    expect(get).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/registry-jobs?registry_target_id=registry%2Fmirror&release_id=release-1&state=dead_letter&kind=repair&limit=25",
    );

    await expect(
      scannerSupplyChainApi.registryJob(job.id),
    ).resolves.toMatchObject({ job, etag: '"7"' });
    expect(getDetail).toHaveBeenCalledWith(
      "/v1/scanner-supply-chain/registry-jobs/registry%2Fjob-1",
    );

    await scannerSupplyChainApi.createRegistryJob("registry/mirror", {
      kind: "repair",
      release_id: "release-1",
      source_registry_id: "registry/primary",
      re_sign_policy: "preserve",
      reason: "restore exact closure",
      max_attempts: 5,
    });
    expect(post).toHaveBeenLastCalledWith(
      "/v1/scanner-supply-chain/registries/registry%2Fmirror/jobs",
      {
        kind: "repair",
        release_id: "release-1",
        source_registry_id: "registry/primary",
        re_sign_policy: "preserve",
        reason: "restore exact closure",
        max_attempts: 5,
      },
      {
        headers: expect.objectContaining({
          "Idempotency-Key": expect.stringMatching(/^wolf-ui-/),
        }),
      },
    );

    await scannerSupplyChainApi.retryRegistryJob(
      job.id,
      "registry permission repaired",
      '"7"',
    );
    expect(post).toHaveBeenLastCalledWith(
      "/v1/scanner-supply-chain/registry-jobs/registry%2Fjob-1/retry",
      { reason: "registry permission repaired" },
      {
        headers: {
          "Idempotency-Key": expect.stringMatching(/^wolf-ui-/),
          "If-Match": '"7"',
        },
      },
    );
  });
});
