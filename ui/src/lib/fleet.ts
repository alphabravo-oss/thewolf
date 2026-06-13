// ui/src/lib/fleet.ts
//
// Wolf's api.get<T>() already unwraps the API envelope and returns
// `{ data: T, meta?, error? }` to the caller. Earlier versions of this
// file passed `<{ data: FleetPosture }>` to api.get, which double-wraps
// the type and makes `data.data` undefined at runtime — every fleet
// component then returned null, the dashboard rendered blank cards.
// Type the value type directly and read `.data` once.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export type FleetPosture = {
  open_findings_by_severity: Record<string, number>;
  week_over_week_delta: Record<string, number>;
  repo_count: number;
  gates_failing: number;
};

export function useFleetPosture(collectionId?: string) {
  return useQuery({
    queryKey: ["fleet", "posture", collectionId ?? null],
    queryFn: async () => {
      const url = collectionId
        ? `/fleet/posture?collection_id=${encodeURIComponent(collectionId)}`
        : "/fleet/posture";
      const r = await api.get<FleetPosture>(url);
      return r.data;
    },
    staleTime: 30_000,
  });
}

export type FleetInventory = {
  by_source_type: Record<string, number>;
  by_collection: Record<string, number>;
  by_language: Record<string, number>;
};

export function useFleetInventory() {
  return useQuery({
    queryKey: ["fleet", "inventory"],
    queryFn: async () => {
      const r = await api.get<FleetInventory>("/fleet/inventory");
      return r.data;
    },
    staleTime: 60_000,
  });
}

export type NeedsAttentionRow = {
  repo_id: string;
  name: string;
  reason: "gate_failing" | "stale" | "new_high" | "scan_failed";
  detail: string;
  score: number;
};

export function useNeedsAttention() {
  return useQuery({
    queryKey: ["fleet", "needs-attention"],
    queryFn: async () => {
      const r = await api.get<NeedsAttentionRow[]>("/fleet/needs-attention");
      return r.data;
    },
    staleTime: 30_000,
  });
}

export type AggregateRow = { key: string; repos: number; findings: number };

export function useTopVulnerableRules(limit = 10) {
  return useQuery({
    queryKey: ["findings", "aggregate", "rule_id", limit],
    queryFn: async () => {
      const r = await api.get<AggregateRow[]>(
        `/findings/aggregate?group_by=rule_id&limit=${limit}`,
      );
      return r.data;
    },
    staleTime: 60_000,
  });
}
