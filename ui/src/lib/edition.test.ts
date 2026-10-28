import { describe, expect, it } from "vitest";
import { editionProduct, isEnterprise, shortRev } from "./edition";

describe("shortRev", () => {
  it("keeps short tokens", () => {
    expect(shortRev("dev")).toBe("dev");
    expect(shortRev("abc")).toBe("abc");
  });
  it("truncates git shas", () => {
    expect(shortRev("66d694a47ed63bf90d3741fad62034290bda0417")).toBe("66d694a");
  });
});

describe("isEnterprise", () => {
  it("detects overlay or edition name", () => {
    expect(isEnterprise({ edition: "community" })).toBe(false);
    expect(isEnterprise({ edition: "enterprise" })).toBe(true);
    expect(isEnterprise({ overlay: { commit: "abc" } })).toBe(true);
  });
});

describe("editionProduct", () => {
  it("prefers the API product and never labels Enterprise as Community", () => {
    expect(editionProduct({ product: "Wolf Enterprise", edition: "enterprise" })).toBe("Wolf Enterprise");
    expect(editionProduct({ edition: "enterprise" })).toBe("Wolf Enterprise");
    expect(editionProduct({ edition: "community" })).toBe("Wolf Community");
    expect(editionProduct(undefined)).toBe("Wolf Community");
  });
});
