// Current-user hook for RBAC gating. /auth/me returns the authenticated user
// including their role (admin | user). The whole admin surface (Settings, user/
// node management, scanner builds) and cross-owner modification gate on this.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export interface Me {
  id: string;
  email: string;
  role: string;
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
  return q.data?.role === "admin";
}

// canModify mirrors the server's ownership rule: admins can modify anything; a
// regular user only what they created (empty owner = legacy/system, allowed).
export function canModify(me: Me | undefined, ownerUserId: string | undefined): boolean {
  if (!me) return false;
  if (me.role === "admin") return true;
  return !ownerUserId || ownerUserId === me.id;
}
