import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { FixJob } from "@/lib/fixes";
import type { FleetChangeRow, FleetPriority } from "@/lib/fleet";
import type { Scan } from "@/lib/types";
import { ageBuckets, critHighTrend, fixFunnel, fixStage, toolHealth, topRepos, trendRows, weeklyChanges } from "./dashboard-data";

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
  it("KPI trend compares critical + high only, against seven days earlier", () => {
    // Live shape: medium dominates the totals; the delta must ignore it.
    const e = (i: number, critical: number, high: number) => ({ date: `2026-10-${String(i).padStart(2, "0")}`, counts: { critical, high, medium: 9000, low: 800, info: 100, total: critical + high + 9900 } });
    const entries = Array.from({ length: 10 }, (_, i) => e(i + 1, i < 3 ? 69 : 12, i < 3 ? 400 : 212));
    expect(critHighTrend(entries)).toEqual({ days: [469, 469, 469, 224, 224, 224, 224, 224, 224, 224], delta: -245 });
    expect(critHighTrend(entries.slice(0, 7)).delta).toBeUndefined();
  });
});

// WCAG relative luminance contrast for #rrggbb.
const lum = (hex: string) => {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a: string, b: string) => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };

describe("severity chart fills", () => {
  const css = readFileSync("src/styles/globals.css", "utf8"); // vitest runs from ui/
  const [light, dark] = css.split(/^\.dark \{/m);
  const fills = (block: string) => [...block.matchAll(/--chart-sev-(\w+): (#[0-9a-f]{6});/g)].map((m) => [m[1], m[2]] as const);
  it.each([["light", light, "#f7f8fa"], ["dark", dark, "#15191d"]])("%s fills are >= 3:1 on the panel and critical stands apart from high", (_, block, panel) => {
    const f = fills(block);
    expect(f.map(([k]) => k)).toEqual(["critical", "high", "medium", "low", "info"]);
    for (const [, hex] of f) expect(contrast(hex, panel)).toBeGreaterThanOrEqual(3);
    // Critical is the most prominent step: highest contrast against the panel.
    expect(Math.max(...f.map(([, hex]) => contrast(hex, panel)))).toBe(contrast(f[0][1], panel));
    expect(contrast(f[0][1], f[1][1])).toBeGreaterThanOrEqual(1.5);
  });
});
