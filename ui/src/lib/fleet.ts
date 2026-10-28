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
import type { Severity } from "./types";

export type FleetCoverageRow = {
  repo_id: string; name: string; branch: string; scan_id?: string;
  status: "fresh" | "stale" | "never_scanned" | "failed" | "incomplete" | "pending" | "running";
  detail: string; last_complete_at?: string; gate_failing: boolean;
};

export type FleetPriority = {
  finding_id: string; repo_id: string; name: string; title: string;
  severity: Severity; tool: string; file_path: string; line: number;
  state: "new" | "resurfaced" | "existing"; confidence: string;
  /** Earliest scan observing the issue (older servers omit it). */
  first_seen_at?: string;
};

export type FleetChangeRow = {
  repo_id: string; name: string; branch: string; scan_id?: string; baseline_scan_id?: string;
  reason?: string; new: number; resurfaced: number; resolved: number; suppressed: number;
};

export type FleetScanWork = {
  scan_id: string; repo_id: string; name: string; branch: string;
  status: string; phase: string; stalled: boolean;
};

export type FleetPosture = {
  open_findings_by_severity: Record<string, number>;
  week_over_week_delta: Record<string, number>;
  repo_count: number;
  gates_failing: number;
  generated_at: string;
  coverage: { fresh_days: number; fresh_repos: number; rows: FleetCoverageRow[] };
  changes: {
    window_days: number; compared_branches: number; excluded_branches: number;
    new: number; resurfaced: number; resolved: number; suppressed: number; rows: FleetChangeRow[];
  };
  priorities: FleetPriority[];
  work: FleetScanWork[];
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
    refetchInterval: 30_000,
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
  reason: "gate_failing" | "stale" | "new_high" | "scan_failed" | "incomplete";
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

export type AggregateRow = {
  key: string;
  repos: number;
  findings: number;
  tool?: string;
  title?: string;
  severity?: string;
  repo_ids?: string[];
  repo_names?: string[];
};

export type FindingsByRepoRow = {
  repo_id: string;
  name: string;
  total: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
};

export function useFindingsByRepo() {
  return useQuery({
    queryKey: ["findings", "by-repo"],
    queryFn: async () => {
      const r = await api.get<FindingsByRepoRow[]>("/findings/by-repo");
      return r.data ?? [];
    },
    staleTime: 30_000,
  });
}

export function useTopVulnerableRules(limit = 10, securityOnly = false) {
  return useQuery({
    queryKey: ["findings", "aggregate", "rule_id", securityOnly, limit],
    queryFn: async () => {
      const r = await api.get<AggregateRow[]>(
        `/findings/aggregate?group_by=rule_id&security_only=${securityOnly ? 1 : 0}&limit=${limit}`,
      );
      return r.data ?? [];
    },
    staleTime: 60_000,
  });
}
