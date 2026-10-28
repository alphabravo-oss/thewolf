// "Security posture" report: GET /fleet/report (live) and Enterprise saved
// snapshots (/enterprise/reports) share this one shape.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { FindingsByRepoRow, FleetPosture } from "./fleet";
import type { TrendEntry } from "@/components/fleet/dashboard-data";

export type SecurityReport = {
  generated_at: string;
  collection?: { id: string; name: string };
  open_by_severity: Record<string, number>;
  trend: TrendEntry[];
  changes: Omit<FleetPosture["changes"], "rows">;
  repo_count: number;
  coverage: Omit<FleetPosture["coverage"], "rows">;
  gates_failing: number;
  fixes_pushed: number;
  /** Open critical/high first observed more than `days` ago (cumulative). */
  aging: { days: number; critical: number; high: number }[];
  top_repos: FindingsByRepoRow[];
};

export type SavedReport = {
  id: string;
  title?: string;
  scope?: { collection_id?: string; name?: string };
  generated_at?: string;
  source?: "manual" | "schedule";
  schedule_id?: string;
  data?: SecurityReport;
};

export type ReportSchedule = {
  id: string;
  name?: string;
  collection_id?: string;
  collection_name?: string;
  cadence?: "weekly" | "monthly";
  integration_kind?: string;
  integration_id?: string;
  integration_name?: string;
  enabled?: boolean;
  link_base?: string;
  last_run_at?: string;
  last_report_id?: string;
  last_error?: string;
};

export const scopeName = (name?: string) => name || "All repositories";

export function useSecurityReport(collectionId?: string) {
  return useQuery({
    queryKey: ["fleet", "report", collectionId ?? null],
    queryFn: async () => (await api.get<SecurityReport>(`/fleet/report${collectionId ? `?collection_id=${encodeURIComponent(collectionId)}` : ""}`)).data,
    staleTime: 30_000,
  });
}
