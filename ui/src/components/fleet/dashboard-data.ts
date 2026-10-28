// Pure aggregations behind the Home charts. Every input is an existing,
// bounded API response (posture, trends, by-repo, newest 100 scans, newest 200
// fix jobs); nothing here pages through unbounded history.
import type { FixJob } from "@/lib/fixes";
import type { FindingsByRepoRow, FleetChangeRow, FleetPriority } from "@/lib/fleet";
import { parseToolList, type Scan } from "@/lib/types";

export const SEVERITIES = ["critical", "high", "medium", "low", "info"] as const;
export type SeverityKey = (typeof SEVERITIES)[number];
export const severityFill: Record<SeverityKey, string> = {
  critical: "var(--chart-sev-critical)", high: "var(--chart-sev-high)", medium: "var(--chart-sev-medium)",
  low: "var(--chart-sev-low)", info: "var(--chart-sev-info)",
};

const DAY = 24 * 3600 * 1000;

export type TrendEntry = { date: string; counts: Record<SeverityKey | "total", number>; repos?: number; incomplete_scans?: number };
export type TrendRow = { date: string } & Record<SeverityKey, number>;

/** The last `days` daily snapshots (the API returns up to 90). */
export function trendRows(entries: TrendEntry[], days: number): TrendRow[] {
  return entries.slice(-days).map((e) => ({ date: e.date, ...Object.fromEntries(SEVERITIES.map((s) => [s, e.counts?.[s] ?? 0])) }) as TrendRow);
}

/** Critical + high per day for the KPI: 30-day sparkline and change vs the snapshot seven days earlier. */
export function critHighTrend(entries: TrendEntry[]): { days: number[]; delta?: number } {
  const days = entries.slice(-30).map((e) => (e.counts?.critical ?? 0) + (e.counts?.high ?? 0));
  return { days, delta: days.length > 7 ? days[days.length - 1] - days[days.length - 8] : undefined };
}

export const AGE_BUCKETS = [
  { key: "0-7", label: "0–7 days", max: 7 },
  { key: "8-30", label: "8–30 days", max: 30 },
  { key: "31-90", label: "31–90 days", max: 90 },
  { key: "90+", label: "Over 90 days", max: Infinity },
] as const;
export type AgeRow = { key: string; label: string; critical: number; high: number };

/** Open critical/high security findings by days since first observation. */
export function ageBuckets(rows: FleetPriority[], now: number): AgeRow[] {
  const out = AGE_BUCKETS.map((b) => ({ key: b.key, label: b.label, critical: 0, high: 0 }));
  for (const f of rows) {
    if (f.severity !== "critical" && f.severity !== "high") continue;
    const seen = f.first_seen_at ? Date.parse(f.first_seen_at) : NaN;
    // A missing timestamp is not "new": count it in the oldest bucket.
    const days = Number.isNaN(seen) || seen <= 0 ? Infinity : Math.floor((now - seen) / DAY);
    out[AGE_BUCKETS.findIndex((b) => days <= b.max)][f.severity]++;
  }
  return out;
}

export type ChangeBar = { key: string; label: string; repo_id: string; scan_id?: string; baseline_scan_id?: string; new: number; resolved: number };

/** Comparable branches from the seven-day comparison, busiest first. New includes resurfaced. */
export function weeklyChanges(rows: FleetChangeRow[], limit = 8): ChangeBar[] {
  return rows
    .filter((r) => !r.reason)
    .map((r) => ({ key: `${r.repo_id}/${r.branch}`, label: r.branch ? `${r.name} · ${r.branch}` : r.name, repo_id: r.repo_id, scan_id: r.scan_id, baseline_scan_id: r.baseline_scan_id, new: r.new + r.resurfaced, resolved: r.resolved }))
    .sort((a, b) => b.new + b.resolved - (a.new + a.resolved) || a.label.localeCompare(b.label))
    .slice(0, limit);
}

/** Riskiest repositories: most critical, then high, then total. */
export function topRepos(rows: FindingsByRepoRow[], limit = 10): FindingsByRepoRow[] {
  return [...rows].sort((a, b) => b.critical - a.critical || b.high - a.high || b.total - a.total || a.name.localeCompare(b.name)).slice(0, limit);
}

export type ToolHealth = { tool: string; runs: number; failed: number; rate: number };

/** Per-tool success rate over finished root scans (completed or failed). */
export function toolHealth(scans: Scan[]): ToolHealth[] {
  const stats = new Map<string, { runs: number; failed: number }>();
  for (const s of scans) {
    if (s.status !== "completed" && s.status !== "failed") continue;
    const failed = new Set(parseToolList(s.tools_failed));
    for (const tool of new Set([...parseToolList(s.tools_completed), ...failed])) {
      const t = stats.get(tool) ?? { runs: 0, failed: 0 };
      t.runs++;
      if (failed.has(tool)) t.failed++;
      stats.set(tool, t);
    }
  }
  return [...stats].map(([tool, t]) => ({ tool, ...t, rate: Math.round(((t.runs - t.failed) / t.runs) * 100) }))
    .sort((a, b) => a.rate - b.rate || b.runs - a.runs || a.tool.localeCompare(b.tool));
}

export const FIX_STAGES = [
  { key: "queued", label: "Queued" },
  { key: "started", label: "Started" },
  { key: "verified", label: "Changes verified" },
  { key: "pushed", label: "Branch pushed" },
] as const;
export type FixStage = (typeof FIX_STAGES)[number]["key"];

/** Furthest stage a fix job reached; superseded jobs are not counted. */
export function fixStage(job: FixJob): FixStage | null {
  if (job.status === "superseded") return null;
  if (job.pushed) return "pushed";
  switch (job.status) {
    case "awaiting_review": case "awaiting_push": case "push_failed": case "succeeded": return "verified";
    case "running": return "started";
    case "failed": case "cancelled": return job.started_at ? "started" : "queued";
    default: return "queued";
  }
}

/** Funnel: jobs that reached at least each stage, and jobs currently at it. */
export function fixFunnel(jobs: FixJob[]) {
  const order = FIX_STAGES.map((s) => s.key);
  const at = Object.fromEntries(order.map((k) => [k, 0])) as Record<FixStage, number>;
  for (const j of jobs) {
    const stage = fixStage(j);
    if (stage) at[stage]++;
  }
  return FIX_STAGES.map((s, i) => ({ ...s, at: at[s.key], reached: order.slice(i).reduce((n, k) => n + at[k], 0) }));
}
