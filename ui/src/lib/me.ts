// Current-user hook for RBAC gating. /auth/me returns the authenticated user
// including their role (admin | user). The whole admin surface (Settings, user/
// node management, scanner builds) and cross-owner modification gate on this.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export interface Me {
  id: string;
  email: string;
  role: string;
  display_name?: string;
  scopes?: string[];
  scanner_supply_chain_personas?: string[];
}

export interface ScannerSupplyChainPermissions {
  read: boolean;
  operate: boolean;
  approve: boolean;
  manageRegistries: boolean;
  administer: boolean;
  systemAdmin: boolean;
}

export const NO_SCANNER_SUPPLY_CHAIN_PERMISSIONS: ScannerSupplyChainPermissions =
  {
    read: false,
    operate: false,
    approve: false,
    manageRegistries: false,
    administer: false,
    systemAdmin: false,
  };

// The UI mirrors server scope implications for presentation only. Every API
// mutation remains server-authoritative, including separation-of-duties.
export function scannerSupplyChainPermissions(
  me: Me | undefined,
): ScannerSupplyChainPermissions {
  if (!me) return NO_SCANNER_SUPPLY_CHAIN_PERMISSIONS;
  const scopes = new Set(me.scopes ?? []);
  const systemAdmin = me.role === "admin" || scopes.has("admin");
  const administer = systemAdmin || scopes.has("admin:scanner-supply-chain");
  const read =
    administer ||
    scopes.has("read:scanner-supply-chain") ||
    scopes.has("operate:scanner-supply-chain") ||
    scopes.has("approve:scanner-releases") ||
    scopes.has("manage:scanner-registries");
  return {
    read,
    operate: administer || scopes.has("operate:scanner-supply-chain"),
    approve: administer || scopes.has("approve:scanner-releases"),
    manageRegistries: administer || scopes.has("manage:scanner-registries"),
    administer,
    systemAdmin,
  };
}

// displayLabel is the name to show for a user in the UI: their display name if
// set, otherwise their email.
export function displayLabel(
  me: { display_name?: string; email: string } | undefined,
): string {
  if (!me) return "";
  return me.display_name?.trim() || me.email;
}

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async () => (await api.get<Me>("/auth/me")).data,
    staleTime: 60_000,
  });
}

// useIsAdmin returns whether the current user is an admin. Defaults to false
// while loading, so admin-only UI stays hidden until confirmed.
export function useIsAdmin(): boolean {
  const q = useMe();
  return q.data?.role === "admin" || q.data?.scopes?.includes("admin") === true;
}

// canModify mirrors the server's ownership rule: admins can modify anything; a
// regular user only what they created (empty owner = legacy/system, allowed).
export function canModify(
  me: Me | undefined,
  ownerUserId: string | undefined,
): boolean {
  if (!me) return false;
  if (me.role === "admin") return true;
  return !ownerUserId || ownerUserId === me.id;
}
