import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import {
  scannerCustomBuildApi,
  validateCustomBuildInput,
} from "./scanner-custom-build";

afterEach(() => vi.restoreAllMocks());

describe("scanner custom-build client", () => {
  it("captures only validated create-response correlation headers", async () => {
    const post = vi.spyOn(api, "postWithMetadata").mockResolvedValue({
      response: {
        data: {
          id: "build-correlated",
          state: "queued",
          status_url: "/api/v1/scanners/custom-builds/build-correlated",
        },
      },
      headers: new Headers({
        "X-Wolf-Operation-ID": "op_custom_build_0001",
        "X-Wolf-Trace-ID": "0123456789abcdef0123456789abcdef",
      }),
      status: 202,
    });

    await expect(
      scannerCustomBuildApi.create({
        variants: ["default"],
        push: false,
        platforms: ["linux/amd64"],
        reason: "Refresh scanner",
      }),
    ).resolves.toEqual({
      id: "build-correlated",
      state: "queued",
      status_url: "/api/v1/scanners/custom-builds/build-correlated",
      operation_id: "op_custom_build_0001",
      trace_id: "0123456789abcdef0123456789abcdef",
    });
    expect(post).toHaveBeenCalledWith(
      "/v1/scanners/custom-builds",
      {
        variants: ["default"],
        push: false,
        platforms: ["linux/amd64"],
        reason: "Refresh scanner",
      },
      {
        headers: {
          "Idempotency-Key": expect.stringMatching(/^wolf-ui-/),
        },
      },
    );
  });

  it("normalizes allowlisted detail fields and drops raw sensitive fields", async () => {
    vi.spyOn(api, "getWithMetadata").mockResolvedValue({
      response: {
        data: {
          build: {
            id: "build-1",
            user_id: "user-secret",
            variants: '["default","unknown","codeql"]',
            push: false,
            platforms: '["linux/amd64","windows/amd64"]',
            state: "partial",
            actor: "admin@example.test",
            reason: "Refresh scanners",
            idempotency_key: "idempotency-never-expose",
            request_digest: "request-never-expose",
            worker_id: "worker-never-expose",
            error_class: "build_failed",
            error_detail: "token=dckr_pat_never_expose",
            summary: '{"secret":"never-expose"}',
            attempt: 1,
            max_attempts: 3,
            version: 4,
            created_at: "2026-07-30T12:00:00Z",
            updated_at: "2026-07-30T12:05:00Z",
          },
          variants: [
            {
              id: "variant-1",
              build_id: "build-1",
              variant: "default",
              ordinal: 0,
              state: "completed",
              refs: '["docker.io/wolf/default:2.0.1","bad\\u0000ref"]',
              digest: "sha256:safe",
              loaded_locally: true,
              pushed: false,
              error_detail: "raw-never-expose",
              version: 2,
            },
          ],
        },
      },
      headers: new Headers({ ETag: '"4"' }),
      status: 200,
    });

    const detail = await scannerCustomBuildApi.detail("build/1");

    expect(detail).toEqual({
      build: expect.objectContaining({
        id: "build-1",
        variants: ["default", "codeql"],
        platforms: ["linux/amd64"],
        error_class: "build_failed",
      }),
      variants: [
        expect.objectContaining({
          refs: ["docker.io/wolf/default:2.0.1", "badref"],
          digest: "sha256:safe",
        }),
      ],
      etag: '"4"',
    });
    expect(detail.build).not.toHaveProperty("user_id");
    expect(detail.build).not.toHaveProperty("idempotency_key");
    expect(detail.build).not.toHaveProperty("request_digest");
    expect(detail.build).not.toHaveProperty("worker_id");
    expect(detail.build).not.toHaveProperty("error_detail");
    expect(detail.build).not.toHaveProperty("summary");
    expect(detail.variants[0]).not.toHaveProperty("error_detail");
    expect(api.getWithMetadata).toHaveBeenCalledWith(
      "/v1/scanners/custom-builds/build%2F1",
    );
  });

  it("sends ETag concurrency and fresh idempotency headers for cancel and retry", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({
      data: {
        id: "build-1",
        variants: '["default"]',
        platforms: '["linux/amd64"]',
        state: "cancelled",
        reason: "Stop it",
        attempt: 1,
        max_attempts: 3,
        version: 6,
        created_at: "2026-07-30T12:00:00Z",
        updated_at: "2026-07-30T12:05:00Z",
      },
    });

    await scannerCustomBuildApi.cancel("build/1", "Stop it", '"5"');
    await scannerCustomBuildApi.retry("build/1", "Try safely", 6);

    expect(post).toHaveBeenNthCalledWith(
      1,
      "/v1/scanners/custom-builds/build%2F1/cancel",
      { reason: "Stop it" },
      {
        headers: {
          "Idempotency-Key": expect.stringMatching(/^wolf-ui-/),
          "If-Match": '"5"',
        },
      },
    );
    expect(post).toHaveBeenNthCalledWith(
      2,
      "/v1/scanners/custom-builds/build%2F1/retry",
      { reason: "Try safely" },
      {
        headers: {
          "Idempotency-Key": expect.stringMatching(/^wolf-ui-/),
          "If-Match": "6",
        },
      },
    );
  });

  it("rejects CodeQL push-all and multi-platform local requests before submit", () => {
    expect(
      validateCustomBuildInput({
        variants: ["all"],
        push: true,
        platforms: ["linux/amd64", "linux/arm64"],
        reason: "Weekly refresh",
      }),
    ).toContain(
      "Build all cannot be pushed because CodeQL is local-only. Run a local all-variant build, or push Default, JVM, and Rust separately.",
    );
    expect(
      validateCustomBuildInput({
        variants: ["codeql"],
        push: false,
        platforms: ["linux/amd64", "linux/arm64"],
        reason: "Local analysis",
      }),
    ).toContain(
      "Local builds support one platform because the result is loaded into this host.",
    );
  });
});
