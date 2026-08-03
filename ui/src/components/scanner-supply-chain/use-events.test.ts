import { describe, expect, it } from "vitest";
import { parseFrame } from "./use-events";

describe("scanner event SSE parsing", () => {
  it("admits only bounded safe scalar fields and drops hostile payloads", () => {
    const parsed = parseFrame(
      [
        "id: 9",
        "event: scanner.release.updated",
        `data: ${JSON.stringify({
          event_type: "scanner.release.updated\u0000",
          reason: `token=dckr_pat_never_render ${"x".repeat(4_000)}`,
          actor: "operator\u007f@example.test",
          payload: { secret: "github_pat_never_render" },
          created_at: "2026-07-30T12:00:00Z",
        })}`,
      ].join("\n"),
    );

    expect(parsed?.type).toBe("event");
    if (!parsed || parsed.type !== "event") return;
    expect(parsed.event.reason).toContain("[REDACTED]");
    expect(parsed.event.reason).not.toContain("dckr_pat_never_render");
    expect(parsed.event.reason?.length).toBeLessThanOrEqual(2_048);
    expect(parsed.event.actor).toBe("operator@example.test");
    expect(parsed.event.event_type).toBe("scanner.release.updated");
    expect(parsed.event.payload).toBeUndefined();
    expect(JSON.stringify(parsed.event)).not.toContain(
      "github_pat_never_render",
    );
  });
});
