// ui/src/lib/fleet.ts
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export type FleetPosture = {
  open_findings_by_severity: Record<string, number>;
  week_over_week_delta: Record<string, number>;
  repo_count: number;
  gates_failing: number;
};

export function useFleetPosture() {
  return useQuery({
    queryKey: ["fleet", "posture"],
    queryFn: async () => {
      const { data } = await api.get<{ data: FleetPosture }>("/fleet/posture");
      return data.data;
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
      const { data } = await api.get<{ data: FleetInventory }>("/fleet/inventory");
      return data.data;
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
      const { data } = await api.get<{ data: NeedsAttentionRow[] }>("/fleet/needs-attention");
      return data.data;
    },
    staleTime: 30_000,
  });
}

export type AggregateRow = { key: string; repos: number; findings: number };

export function useTopVulnerableRules(limit = 10) {
  return useQuery({
    queryKey: ["findings", "aggregate", "rule_id", limit],
    queryFn: async () => {
      const { data } = await api.get<{ data: AggregateRow[] }>(
        `/findings/aggregate?group_by=rule_id&limit=${limit}`,
      );
      return data.data;
    },
    staleTime: 60_000,
  });
}
