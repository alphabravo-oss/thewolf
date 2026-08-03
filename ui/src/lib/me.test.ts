import { describe, expect, it } from "vitest";
import { scannerSupplyChainPermissions, type Me } from "./me";

describe("scannerSupplyChainPermissions", () => {
  const identity = (scopes: string[], role = "user"): Me => ({
    id: "user-1",
    email: "user@example.test",
    role,
    scopes,
  });

  it.each([
    [
      "viewer",
      ["read:scanner-supply-chain"],
      {
        read: true,
        operate: false,
        approve: false,
        manageRegistries: false,
        administer: false,
      },
    ],
    [
      "scanner operator",
      ["read:scanner-supply-chain", "operate:scanner-supply-chain"],
      {
        read: true,
        operate: true,
        approve: false,
        manageRegistries: false,
        administer: false,
      },
    ],
    [
      "release approver",
      ["read:scanner-supply-chain", "approve:scanner-releases"],
      {
        read: true,
        operate: false,
        approve: true,
        manageRegistries: false,
        administer: false,
      },
    ],
    [
      "registry administrator",
      ["read:scanner-supply-chain", "manage:scanner-registries"],
      {
        read: true,
        operate: false,
        approve: false,
        manageRegistries: true,
        administer: false,
      },
    ],
    [
      "supply-chain administrator",
      ["admin:scanner-supply-chain"],
      {
        read: true,
        operate: true,
        approve: true,
        manageRegistries: true,
        administer: true,
      },
    ],
    [
      "auditor",
      ["read:scanner-supply-chain"],
      {
        read: true,
        operate: false,
        approve: false,
        manageRegistries: false,
        administer: false,
      },
    ],
  ] as const)(
    "derives %s boundaries from server scopes",
    (_name, scopes, expected) => {
      expect(
        scannerSupplyChainPermissions(identity([...scopes])),
      ).toMatchObject(expected);
    },
  );

  it("keeps system administrators implicit full", () => {
    expect(scannerSupplyChainPermissions(identity([], "admin"))).toEqual({
      read: true,
      operate: true,
      approve: true,
      manageRegistries: true,
      administer: true,
      systemAdmin: true,
    });
  });
});
