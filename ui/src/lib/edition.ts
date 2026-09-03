import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

export type Rev = { version?: string; commit?: string };

export type VersionInfo = {
  version?: string;
  commit?: string;
  edition?: string;
  product?: string;
  license?: string;
  community?: Rev;
  overlay?: Rev;
};

export type EditionInfo = VersionInfo & {
  licensed?: boolean;
  ui_routes?: { path: string; title: string }[];
  entitlements?: Record<string, boolean>;
  modules?: string[];
  mcp?: { enabled?: boolean };
  limits?: {
    repos?: number;
    users?: number;
    workers?: number;
    source?: string;
    enforced?: boolean;
  };
};

export function shortRev(s?: string) {
  if (!s) return "";
  if (s === "dev" || s === "unknown") return s;
  return s.length > 12 ? s.slice(0, 7) : s;
}

export function useVersion() {
  return useQuery({
    queryKey: ["version"],
    queryFn: async () => (await api.get<VersionInfo>("/version")).data,
    staleTime: 5 * 60 * 1000,
  });
}

export function useEdition() {
  return useQuery({
    queryKey: ["edition"],
    queryFn: async () => (await api.get<EditionInfo>("/edition")).data,
    staleTime: 5 * 60 * 1000,
  });
}

export function isEnterprise(ed?: { edition?: string; overlay?: Rev }) {
  return ed?.edition === "enterprise" || !!ed?.overlay;
}
