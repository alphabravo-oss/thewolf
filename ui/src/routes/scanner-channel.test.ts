import { describe, expect, it } from "vitest";
import { describeScannerChannel } from "./_authed.settings";

describe("describeScannerChannel", () => {
  it("labels the moving release channels", () => {
    for (const tag of ["stable", "latest"]) {
      const c = describeScannerChannel(`ghcr.io/alphabravo-oss/wolf-scanners:${tag}`);
      expect(c.label).toBe(tag);
      expect(c.detail).toMatch(/approved release set/i);
    }
  });

  it("distinguishes candidate from stable", () => {
    const c = describeScannerChannel("ghcr.io/alphabravo-oss/wolf-scanners:candidate");
    expect(c.label).toBe("candidate");
    expect(c.detail).toMatch(/not yet promoted/i);
  });

  it("recognises an immutable release set as pinned", () => {
    const c = describeScannerChannel(
      "ghcr.io/alphabravo-oss/wolf-scanners:scanner-set-2026.34.1",
    );
    expect(c.label).toBe("scanner-set-2026.34.1");
    expect(c.detail).toMatch(/never moves/i);
  });

  it("treats a digest reference as pinned", () => {
    const c = describeScannerChannel(
      "ghcr.io/alphabravo-oss/wolf-scanners@sha256:" + "a".repeat(64),
    );
    expect(c.label).toBe("Pinned digest");
  });

  // A registry host may carry a port; that colon is not a tag separator.
  it("does not mistake a registry port for a tag", () => {
    const c = describeScannerChannel("registry.internal:5000/wolf-scanners");
    expect(c.label).toBe("Unpinned");
  });

  it("keeps a port and a real tag straight", () => {
    const c = describeScannerChannel("registry.internal:5000/wolf-scanners:stable");
    expect(c.label).toBe("stable");
  });

  it("falls back for locally built tags", () => {
    const c = describeScannerChannel("wolf-scanners:dev");
    expect(c.label).toBe("dev");
    expect(c.detail).toMatch(/custom or locally built/i);
  });
});
