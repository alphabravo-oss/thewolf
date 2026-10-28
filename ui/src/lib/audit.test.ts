import { describe, expect, it } from "vitest";
import { describeAudit } from "./audit";

const row = (method: string, path: string, status_code = 200, event_type?: string) => ({ method, path, status_code, event_type });

describe("describeAudit", () => {
  it("prefers server-classified events", () => {
    expect(describeAudit(row("POST", "/api/v1/auth/login", 200, "auth.login")).text).toBe("Signed in");
    expect(describeAudit(row("DELETE", "/api/v1/users/u1", 200, "user.deleted")).text).toBe("Deleted a user");
  });

  it("reads known request shapes", () => {
    expect(describeAudit(row("POST", "/api/v1/scans", 202, "scans.created")).text).toBe("Started a scan");
    expect(describeAudit(row("PUT", "/api/v1/findings/f1/status", 200, "findings.updated")).text).toBe("Updated finding status");
  });

  it("falls back to verb and noun from the path", () => {
    expect(describeAudit(row("DELETE", "/api/repos/r1")).text).toBe("Deleted a repository");
    expect(describeAudit(row("POST", "/api/v1/collections")).text).toBe("Created a collection");
    expect(describeAudit(row("PUT", "/api/v1/ai-providers/x")).text).toBe("Updated an ai provider");
    expect(describeAudit(row("POST", "/api/v1/widgets")).text).toBe("Created a widget");
  });

  it("reads sign-in from the path when the event type is unfamiliar", () => {
    expect(describeAudit(row("POST", "/api/v1/auth/login", 200, "auth.login_ok")).text).toBe("Signed in");
    expect(describeAudit(row("POST", "/api/v1/auth/login", 401, "auth.login_failed")).text).toBe("Failed sign-in attempt");
  });

  it("flags failed requests without changing the label", () => {
    expect(describeAudit(row("POST", "/api/v1/auth/login", 401, "auth.login.failed"))).toEqual({ text: "Failed sign-in attempt", failed: true });
    expect(describeAudit(row("POST", "/api/v1/scans", 201)).failed).toBe(false);
  });
});
