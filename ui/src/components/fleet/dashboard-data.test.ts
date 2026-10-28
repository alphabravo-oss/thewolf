import { describe, expect, it } from "vitest";
import type { FixJob } from "@/lib/fixes";
import type { FleetChangeRow, FleetPriority } from "@/lib/fleet";
import type { Scan } from "@/lib/types";
import { ageBuckets, fixFunnel, fixStage, toolHealth, topRepos, trendRows, weeklyChanges } from "./dashboard-data";

const NOW = Date.parse("2026-10-28T12:00:00Z");
const daysAgo = (d: number) => new Date(NOW - d * 86_400_000).toISOString();
const prio = (severity: string, first_seen_at?: string) => ({ severity, first_seen_at }) as FleetPriority;

describe("ageBuckets", () => {
  it("buckets critical/high by days since first seen, edges inclusive", () => {
    const rows = ageBuckets([
      prio("critical", daysAgo(0)), prio("high", daysAgo(7)), prio("high", daysAgo(8)),
      prio("critical", daysAgo(30)), prio("high", daysAgo(90)), prio("critical", daysAgo(91)),
      prio("high"), prio("medium", daysAgo(1)),
    ], NOW);
    expect(rows.map((r) => [r.key, r.critical, r.high])).toEqual([
      ["0-7", 1, 1], ["8-30", 1, 1], ["31-90", 0, 1], ["90+", 1, 1],
    ]);
  });
});

describe("fixFunnel", () => {
  const job = (status: string, extra: Partial<FixJob> = {}) => ({ status, pushed: false, ...extra }) as FixJob;
  it("maps statuses to the furthest stage reached", () => {
    expect(fixStage(job("queued"))).toBe("queued");
    expect(fixStage(job("running"))).toBe("started");
    expect(fixStage(job("failed"))).toBe("queued");
    expect(fixStage(job("failed", { started_at: daysAgo(1) }))).toBe("started");
    expect(fixStage(job("awaiting_review"))).toBe("verified");
    expect(fixStage(job("succeeded", { pushed: true }))).toBe("pushed");
    expect(fixStage(job("superseded"))).toBeNull();
  });
  it("counts jobs reaching each stage cumulatively", () => {
    const rows = fixFunnel([job("queued"), job("running"), job("awaiting_push"), job("succeeded", { pushed: true }), job("superseded")]);
    expect(rows.map((r) => [r.key, r.reached, r.at])).toEqual([["queued", 4, 1], ["started", 3, 1], ["verified", 2, 1], ["pushed", 1, 1]]);
  });
});

describe("weeklyChanges", () => {
  it("keeps comparable branches, folds resurfaced into new, busiest first", () => {
    const row = (name: string, n: number, resurfaced: number, resolved: number, reason?: string) =>
      ({ repo_id: name, name, branch: "main", scan_id: `${name}-scan`, new: n, resurfaced, resolved, suppressed: 0, reason }) as FleetChangeRow;
    const rows = weeklyChanges([row("a", 1, 0, 0), row("b", 2, 1, 4), row("c", 9, 0, 9, "Needs a recent, complete scan.")]);
    expect(rows.map((r) => [r.label, r.new, r.resolved, r.scan_id])).toEqual([["b · main", 3, 4, "b-scan"], ["a · main", 1, 0, "a-scan"]]);
  });
});

describe("chart helpers", () => {
  it("tool health counts finished runs only, least reliable first", () => {
    const scan = (status: string, completed: string[], failed: string[]) => ({ status, tools_completed: JSON.stringify(completed), tools_failed: JSON.stringify(failed) }) as Scan;
    const rows = toolHealth([scan("completed", ["semgrep", "trivy"], []), scan("failed", ["semgrep"], ["trivy"]), scan("running", [], ["semgrep"])]);
    expect(rows).toEqual([{ tool: "trivy", runs: 2, failed: 1, rate: 50 }, { tool: "semgrep", runs: 2, failed: 0, rate: 100 }]);
  });
  it("top repos rank critical, then high, then total", () => {
    const r = (name: string, critical: number, high: number, total: number) => ({ repo_id: name, name, critical, high, total, medium: 0, low: 0, info: 0 });
    expect(topRepos([r("a", 0, 9, 50), r("b", 1, 0, 1), r("c", 0, 9, 60)]).map((x) => x.name)).toEqual(["b", "c", "a"]);
  });
  it("trend rows keep the last N days", () => {
    const e = (date: string, critical: number) => ({ date, counts: { critical, high: 0, medium: 0, low: 0, info: 0, total: critical } });
    expect(trendRows([e("2026-10-01", 1), e("2026-10-02", 2), e("2026-10-03", 3)], 2)).toEqual([
      { date: "2026-10-02", critical: 2, high: 0, medium: 0, low: 0, info: 0 },
      { date: "2026-10-03", critical: 3, high: 0, medium: 0, low: 0, info: 0 },
    ]);
  });
});
