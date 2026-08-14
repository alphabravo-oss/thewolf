import { describe, expect, it } from "vitest";
import { canModify, displayLabel, type Me } from "./me";

describe("me helpers", () => {
  const admin: Me = {
    id: "admin-1",
    email: "admin@example.test",
    role: "admin",
  };
  const user: Me = {
    id: "user-1",
    email: "user@example.test",
    role: "user",
    display_name: "Ada",
  };

  it("lets admins modify any record and owners modify their own", () => {
    expect(canModify(admin, "user-1")).toBe(true);
    expect(canModify(user, "user-1")).toBe(true);
    expect(canModify(user, "user-2")).toBe(false);
    expect(canModify(undefined, "user-1")).toBe(false);
  });

  it("prefers a display name when set", () => {
    expect(displayLabel(user)).toBe("Ada");
    expect(displayLabel(admin)).toBe("admin@example.test");
    expect(displayLabel(undefined)).toBe("");
  });
});
