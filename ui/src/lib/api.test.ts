import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("API error responses", () => {
  it("preserves a proxy failure's HTTP status without exposing HTML", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<html>private proxy detail</html>", { status: 503, statusText: "Service Unavailable" })));
    await expect(api.get("/scanners/runtime-capabilities")).rejects.toMatchObject({ name: "ApiError", status: 503, code: "HTTP_ERROR", message: "Service Unavailable" });
  });

  it("handles a null error body without throwing a TypeError", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("null", { status: 403, statusText: "Forbidden" })));
    await expect(api.get("/scanners/images")).rejects.toMatchObject({ status: 403, message: "Forbidden" });
  });

  it("rejects a successful response containing invalid JSON", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<html>login page</html>", { status: 200 })));
    await expect(api.get("/scans")).rejects.toBeInstanceOf(ApiError);
  });
});
