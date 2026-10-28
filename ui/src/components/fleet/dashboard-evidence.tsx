import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { GitPullRequestIcon, OctagonAlertIcon, RadarIcon, RefreshCwIcon, ShieldAlertIcon, TrendingUpIcon } from "lucide-react";
import { useFleetPosture, type FleetPosture, type FleetChangeRow } from "@/lib/fleet";
import { useFlag } from "@/lib/flags";
import { useFixJobs } from "@/lib/fixes";
import { useIsAdmin } from "@/lib/me";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MetricCard } from "@/components/ui/metric-card";
import { Skeleton } from "@/components/ui/skeleton";
import { SeverityBadge } from "@/components/severity-badge";
import { Section } from "@/components/ui/section";
import { RecentActivity } from "@/components/fleet/recent-activity";
import { FindingAge, FindingsOverTime, FixFunnel, NewVsResolved, RiskiestRepos, ScannerHealth, TopRules, useFindingTrends } from "@/components/fleet/dashboard-charts";
import { fixStage } from "@/components/fleet/dashboard-data";

const linkClass = "font-medium text-primary hover:underline underline-offset-4";
const critHighList = { view: "list", scope: "all", severity: "critical,high" } as const;
const kpiClass = "block rounded-lg focus-visible:outline-2 focus-visible:outline-primary";

export function DashboardEvidence() {
  const q = useFleetPosture();
  const admin = useIsAdmin();
  const autofix = useFlag("autofix_enabled");
  const [progressOpen, setProgressOpen] = useState(false);
  const p = q.data;
  return <div className="space-y-6">
    {q.isError && <Card><CardContent><div role="alert" className="flex flex-wrap items-center justify-between gap-3 text-sm"><p>Dashboard evidence could not be loaded. Coverage and risk are unknown.</p><Button size="sm" variant="outline" disabled={q.isFetching} onClick={() => void q.refetch()}>Retry dashboard</Button></div></CardContent></Card>}
    <Kpis posture={p} loading={q.isPending} autofix={autofix.enabled} openProgress={() => setProgressOpen(true)} />
    <div className="grid grid-cols-1 gap-5 xl:grid-cols-2">
      <FindingsOverTime />
      <NewVsResolved q={q} />
      <FindingAge q={q} />
      <RiskiestRepos />
      <TopRules />
      <ScannerHealth />
      {autofix.enabled && <FixFunnel />}
    </div>
    <div className="grid grid-cols-1 gap-5 xl:grid-cols-2">
      <ActOnNow posture={p} loading={q.isPending} />
      {admin && <RecentActivity />}
    </div>
    {p && <ProgressEvidence posture={p} open={progressOpen} onOpenChange={setProgressOpen} />}
    {p && <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
      <p>
        {p.work.length ? <Link to="/scans" className={linkClass}>{p.work.length} {p.work.length === 1 ? "scan" : "scans"} queued or running{p.work.some((w) => w.stalled) ? " (worker lease expired)" : ""}</Link> : "No scans queued or running."}
        {" · "}Updated {new Date(p.generated_at).toLocaleTimeString()} · refreshes every 30 seconds
      </p>
      <button className="inline-flex items-center gap-1.5 hover:text-foreground" disabled={q.isFetching} onClick={() => void q.refetch()}><RefreshCwIcon className="size-3" aria-hidden />Refresh evidence</button>
    </div>}
  </div>;
}

function Kpis({ posture: p, loading, autofix, openProgress }: { posture?: FleetPosture; loading: boolean; autofix: boolean; openProgress: () => void }) {
  const trends = useFindingTrends();
  const fixes = useFixJobs(autofix);
  if (!p) return loading ? <div role="status" aria-label="Loading dashboard" className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-28" />)}</div> : null;
  const sev = p.open_findings_by_severity;
  const critHigh = (sev.critical ?? 0) + (sev.high ?? 0);
  // 30-day sparkline and week-over-week change from the observed daily series.
  const days = (trends.data ?? []).slice(-30).map((d) => (d.counts.critical ?? 0) + (d.counts.high ?? 0));
  const delta = days.length > 7 ? days[days.length - 1] - days[days.length - 8] : undefined;
  const c = p.changes;
  const comparable = c.compared_branches > 0;
  const coverage = p.repo_count ? Math.round((p.coverage.fresh_repos / p.repo_count) * 100) : 0;
  const jobs = fixes.data ?? [];
  const pushed = jobs.filter((j) => fixStage(j) === "pushed").length;
  const verified = jobs.filter((j) => fixStage(j) === "verified").length + pushed;
  const trend = delta === undefined ? {} : { trend: delta > 0 ? "up" as const : delta < 0 ? "down" as const : "flat" as const, trendValue: `${delta > 0 ? "+" : ""}${delta} vs 7 days ago` };
  // Five tiles fill two full rows (3 + 2) instead of leaving a hole.
  const top = autofix ? `${kpiClass} lg:col-span-2` : kpiClass;
  const bottom = autofix ? `${kpiClass} lg:col-span-3` : kpiClass;
  return <div className={`grid grid-cols-1 gap-3 sm:grid-cols-2 ${autofix ? "lg:grid-cols-6" : "xl:grid-cols-4"}`}>
    <Link to="/findings" search={critHighList} className={top}><MetricCard className="h-full" title="Open critical / high" value={critHigh} subtitle={`Critical ${sev.critical ?? 0} · High ${sev.high ?? 0}`} icon={<ShieldAlertIcon className="size-4" aria-hidden />} sparkline={days.length > 1 ? days : undefined} {...trend} /></Link>
    <a href="#are-we-improving" onClick={openProgress} className={top}><MetricCard className="h-full" title="New vs resolved this week" value={comparable ? `${c.new + c.resurfaced} / ${c.resolved}` : "—"} subtitle={comparable ? `New (incl. resurfaced) / verified resolved on ${c.compared_branches} comparable ${c.compared_branches === 1 ? "branch" : "branches"}` : "Unknown: no comparable scans this week"} icon={<TrendingUpIcon className="size-4" aria-hidden />} /></a>
    <Link to="/repos" className={top}><MetricCard className="h-full" title="Scan coverage" value={`${coverage}%`} percentage={coverage} subtitle={`${p.coverage.fresh_repos} of ${p.repo_count} repositories complete in ${p.coverage.fresh_days} days`} icon={<RadarIcon className="size-4" aria-hidden />} /></Link>
    <Link to="/repos" className={bottom}><MetricCard className="h-full" title="Gates failing" value={p.gates_failing} subtitle={`${p.gates_failing === 1 ? "Repository" : "Repositories"} failing on the latest scan`} icon={<OctagonAlertIcon className="size-4" aria-hidden />} /></Link>
    {autofix && <Link to="/agents" search={{ stage: "pushed" }} className={bottom}><MetricCard className="h-full" title="Fix branches pushed" value={fixes.isError ? "—" : fixes.isPending ? "…" : `${pushed} / ${verified}`} subtitle="Pushed / verified fixes. PR and merge state is not tracked" icon={<GitPullRequestIcon className="size-4" aria-hidden />} /></Link>}
  </div>;
}

function ActOnNow({ posture: p, loading }: { posture?: FleetPosture; loading: boolean }): ReactNode {
  const rows = (p?.priorities ?? []).slice(0, 5);
  return <section id="priority-findings" aria-labelledby="priority-heading" className="min-w-0 scroll-mt-20"><Card className="h-full">
    <CardHeader><h2 id="priority-heading" className="font-semibold">Act on now</h2><Link to="/findings" search={critHighList} className={`${linkClass} text-xs`}>All critical / high →</Link></CardHeader>
    <CardContent>
      {loading ? <Skeleton className="h-48 w-full" /> : !p ? <p className="text-sm text-muted-foreground">High-risk findings are unknown until dashboard evidence loads.</p>
        : rows.length === 0 ? <p className="text-sm text-muted-foreground">No open high-risk findings observed. Check scan coverage before drawing conclusions.</p>
        : <ul className="divide-y divide-border">{rows.map((f) => <li key={f.finding_id} className="space-y-1 py-2.5">
          <div className="flex flex-wrap items-center gap-2"><SeverityBadge severity={f.severity} size="sm" /><span className="text-xs capitalize text-muted-foreground">{f.state}</span><Link to="/repos/$repoId" params={{ repoId: f.repo_id }} className="ml-auto text-xs hover:underline">{f.name}</Link></div>
          <Link to="/findings/$findingId" params={{ findingId: f.finding_id }} className={`${linkClass} block break-words`}>{f.title}</Link>
          <p className="break-all text-xs text-muted-foreground">{f.tool} · {f.file_path}{f.line > 0 ? `:${f.line}` : ""}</p>
        </li>)}</ul>}
    </CardContent>
  </Card></section>;
}

function ProgressEvidence({ posture: p, open, onOpenChange }: { posture: FleetPosture; open: boolean; onOpenChange: (open: boolean) => void }) {
  const c = p.changes;
  const comparable = c.compared_branches > 0;
  const columns: Column<FleetChangeRow>[] = [
    { key: "repo", sortable: false, header: "Repository / branch", accessor: (r) => <div><Link to="/repos/$repoId" params={{ repoId: r.repo_id }} className="font-medium hover:underline">{r.name}</Link><p className="text-xs text-muted-foreground break-all">{r.branch || "Working tree"}</p></div> },
    { key: "result", sortable: false, header: "What changed", accessor: (r) => r.reason ? <span className="text-xs text-muted-foreground">Not compared: {r.reason}</span> : <span className="text-xs">{r.new} new · {r.resurfaced} resurfaced · {r.resolved} verified resolved · {r.suppressed} set aside</span> },
    { key: "evidence", sortable: false, header: "Review evidence", accessor: (r) => <div className="flex flex-wrap gap-x-3 gap-y-2 text-xs">{r.scan_id && <Link className={linkClass} to="/scans/$scanId" params={{ scanId: r.scan_id }}>Latest scan</Link>}{r.baseline_scan_id && <Link className={linkClass} to="/scans/$scanId" params={{ scanId: r.baseline_scan_id }}>Baseline scan</Link>}{!r.scan_id && <Link className={linkClass} to="/scans" search={{ repo_id: r.repo_id }}>Run first scan</Link>}</div> },
  ];
  return <Section id="are-we-improving" headingLevel={2} className="scroll-mt-20 bg-card" triggerClassName="px-5 py-3.5 text-base font-semibold" title="Are we improving?" open={open} onOpenChange={onOpenChange}
    summary={comparable ? `${c.new} new · ${c.resolved} resolved · last ${c.window_days} days` : "Not enough comparable scans"}>
    <div className="space-y-4 px-5 pb-5 pt-1">
      <p className="text-xs text-muted-foreground">Last {c.window_days} days · {c.compared_branches} branches compared · {c.excluded_branches} excluded</p>
      {!comparable && <p className="rounded-md border border-border bg-muted/30 p-3 text-sm">Progress is unknown. We need complete scans before and during this window with matching tools, scope, and scanner release.</p>}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        {[{ label: "New", value: c.new }, { label: "Resurfaced", value: c.resurfaced }, { label: "Verified resolved", value: c.resolved }, { label: "Suppressed / set aside", value: c.suppressed }].map((m) => <a href="#comparison-evidence" key={m.label} className="rounded-md border border-border p-3 hover:bg-muted/30"><div className="text-xs text-muted-foreground">{m.label}</div><div className="text-2xl font-semibold tabular-nums mt-1">{comparable ? m.value : "—"}</div></a>)}
      </div>
      <p className="text-xs text-muted-foreground">Distinct security issue signatures across comparable branches. Resolved means absent in a complete rescan and absent from other current branch observations. Suppressions and triage changes receive no resolution credit. First scans, changed scope, and incomplete coverage are excluded.</p>
      <div id="comparison-evidence" className="scroll-mt-20"><DataTable data={c.rows} columns={columns} keyExtractor={(r) => `${r.repo_id}/${r.branch}`} searchable={false} columnVisibilityControl={false} pageSize={5} density="compact" emptyMessage="No scan evidence to compare yet." /></div>
    </div>
  </Section>;
}
