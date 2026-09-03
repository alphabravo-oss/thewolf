import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import {
  COMMUNITY_LIMIT_COPY,
  isCommunityLimit,
  safeBackendFailureMessage,
  safeDisplayText,
  safeErrorMessage,
  safeEvidenceHref,
} from "./safe-display";

describe("safeDisplayText", () => {
  it("removes unsafe controls, masks credential forms, and bounds output", () => {
    const value =
      "start\u0000 Authorization: Bearer topsecret123\u007f " +
      "dckr_pat_never_render " +
      "x".repeat(200);
    const rendered = safeDisplayText(value, 96);

    expect(rendered).not.toContain("\u0000");
    expect(rendered).not.toContain("\u007f");
    expect(rendered).not.toContain("topsecret123");
    expect(rendered).not.toContain("dckr_pat_never_render");
    expect(rendered).toContain("[REDACTED]");
    expect(rendered.endsWith("… [truncated]")).toBe(true);
    expect(rendered.length).toBeLessThanOrEqual(96);
  });

  it("preserves safe newlines and tabs", () => {
    expect(safeDisplayText("first\n\tsecond", 100)).toBe("first\n\tsecond");
  });

  it("masks JSON-shaped credential and private-key fields", () => {
    const rendered = safeDisplayText(
      '{"credential":"unstructured-value","private_key":"-----BEGIN KEY-----"}',
    );
    expect(rendered).not.toContain("unstructured-value");
    expect(rendered).not.toContain("BEGIN KEY");
    expect(rendered).toContain("[REDACTED]");
  });

  it("points Community evaluation limits at Settings → License", () => {
    const error = new ApiError(
      409,
      "community_limit",
      "Community evaluation limit: at most 5 repositories",
    );
    expect(safeErrorMessage(error)).toBe(COMMUNITY_LIMIT_COPY);
    expect(isCommunityLimit(error)).toBe(true);
  });

  it("never presents raw API error messages", () => {
    const error = new ApiError(
      503,
      "UPSTREAM_FAILED",
      "authorization=dckr_pat_raw_backend_never_render",
    );
    expect(safeErrorMessage(error, "Fallback")).toBe(
      "The service could not complete this operation. Retry or review service health.",
    );
    expect(safeErrorMessage(error, "Fallback")).not.toContain("dckr_pat");
    expect(
      safeBackendFailureMessage("registry_unavailable"),
    ).toContain("Registry Unavailable");
  });

  it("allows only same-origin or allowlisted HTTPS evidence links", () => {
    const options = { baseUrl: "https://wolf.example/scanners" };
    expect(safeEvidenceHref("/api/evidence/1", options)).toBe(
      "/api/evidence/1",
    );
    expect(
      safeEvidenceHref("https://github.com/semgrep/semgrep", options),
    ).toBe("https://github.com/semgrep/semgrep");
    expect(
      safeEvidenceHref("https://evidence.corp.example/item/1", {
        ...options,
        additionalHosts: ["corp.example"],
      }),
    ).toBe("https://evidence.corp.example/item/1");

    for (const unsafe of [
      "javascript:alert(1)",
      "data:text/html,payload",
      "http://github.com/semgrep/semgrep",
      "https://github.com.attacker.example/evidence",
      "https://user:password@github.com/private",
      "//attacker.example/evidence",
    ]) {
      expect(safeEvidenceHref(unsafe, options), unsafe).toBeUndefined();
    }
  });
});
