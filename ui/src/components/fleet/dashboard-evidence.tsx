import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GaugeIcon, OctagonAlertIcon, RadarIcon, RefreshCwIcon, ShieldAlertIcon } from "lucide-react";
import type { Finding, Scan } from "@/lib/types";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import { useFleetPosture, type FleetPosture, type FleetPriority, type FleetCoverageRow, type FleetChangeRow } from "@/lib/fleet";
import { useFlag } from "@/lib/flags";
import { useFixJobs, isFixPaused, type FixJob } from "@/lib/fixes";
import { useIsAdmin } from "@/lib/me";
import { useRuntimeCapabilities } from "@/lib/runtime-capabilities";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MetricCard } from "@/components/ui/metric-card";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { SeverityBadge } from "@/components/severity-badge";
import { Section } from "@/components/ui/section";
import { NeedsAttention } from "@/components/fleet/needs-attention";
import { RecentActivity } from "@/components/fleet/recent-activity";

const linkClass = "font-medium text-primary hover:underline underline-offset-4";
const coverageLabels: Record<FleetCoverageRow["status"], string> = {
  fresh: "Complete", stale: "Stale", never_scanned: "Never scanned", failed: "Failed",
  incomplete: "Incomplete", pending: "Queued", running: "Running",
};

const WEEK_MS = 7 * 24 * 3600 * 1000;

// /scans has no date filter, so count the newest 100 root scans started in the
// last 7 days; "100+" means the page was full and the real count is higher.
function useScansThisWeek() {
  return useQuery({
    queryKey: ["scans", "this-week"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?per_page=100&roots=1&sort=started&order=desc");
      const n = (r.data ?? []).filter((s) => s.started_at && Date.now() - new Date(s.started_at).getTime() < WEEK_MS).length;
      return n >= 100 ? "100+" : n;
    },
    staleTime: 30_000,
  });
}

export function DashboardEvidence() {
  const q = useFleetPosture();
  const scansWeek = useScansThisWeek();
  const admin = useIsAdmin();
  const [priorityScope, setPriorityScope] = useState("all");
  const [coverageScope, setCoverageScope] = useState("all");
  if (q.isLoading) return <div role="status" aria-label="Loading dashboard" className="space-y-4"><Skeleton className="h-24" /><Skeleton className="h-80" /></div>;
  if (q.isError || !q.data) return <Card><CardContent><div role="alert" className="space-y-3"><p>Dashboard evidence could not be loaded. Coverage and risk are unknown.</p><button className={linkClass} onClick={() => void q.refetch()}>Retry dashboard</button></div></CardContent></Card>;
  const p = q.data;
  // /fleet/posture contains the complete authorized evidence set (no row cap).
  const sev = p.open_findings_by_severity;
  const critHigh = (sev.critical ?? 0) + (sev.high ?? 0);
  const delta = (p.week_over_week_delta.critical ?? 0) + (p.week_over_week_delta.high ?? 0);
  const wow = "critical" in p.week_over_week_delta || "high" in p.week_over_week_delta
    ? { trend: delta > 0 ? "up" as const : delta < 0 ? "down" as const : "flat" as const, trendValue: `${delta > 0 ? "+" : ""}${delta} vs last week` } : {};
  const attention = new Set(p.coverage.rows.filter((r) => r.status === "failed" || r.status === "incomplete").map((r) => r.repo_id)).size;
  return <div className="space-y-6">
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {[
        { title: "Open critical / high", value: critHigh, subtitle: `Critical ${sev.critical ?? 0} · High ${sev.high ?? 0}`, icon: <ShieldAlertIcon className="size-4" aria-hidden />, href: "#priority-findings", select: () => setPriorityScope("all"), ...wow },
        { title: "Scans this week", value: scansWeek.isError ? "—" : scansWeek.data ?? "…", subtitle: "Root scans started in the last 7 days", icon: <GaugeIcon className="size-4" aria-hidden />, href: "#work-in-progress" },
        { title: "Fresh scan coverage", value: `${p.coverage.fresh_repos} / ${p.repo_count}`, subtitle: `Complete scans in the last ${p.coverage.fresh_days} days`, icon: <RadarIcon className="size-4" aria-hidden />, href: "#scan-coverage", select: () => setCoverageScope("all") },
        { title: "Gates failing", value: p.gates_failing, subtitle: `${attention} ${attention === 1 ? "repository needs" : "repositories need"} attention (failed or incomplete)`, icon: <OctagonAlertIcon className="size-4" aria-hidden />, href: "#scan-coverage", select: () => setCoverageScope("attention") },
      ].map(({ href, select, ...metric }) => <a key={metric.title} href={href} onClick={select} className="block rounded-lg focus-visible:outline-2 focus-visible:outline-primary"><MetricCard {...metric} className="h-full" /></a>)}
    </div>
    <div className="grid grid-cols-1 xl:grid-cols-[1.15fr_1fr] gap-5 items-start">
      <PriorityFindings rows={p.priorities} scope={priorityScope} setScope={setPriorityScope} />
      <ScanCoverage posture={p} scope={coverageScope} setScope={setCoverageScope} />
    </div>
    <div className="grid grid-cols-1 gap-5 xl:grid-cols-2">
      <NeedsAttention />
      {admin && <RecentActivity />}
    </div>
    <WorkInProgress posture={p} />
    <QualityVolume />
    <ProgressEvidence posture={p} />
    <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
      <p>Updated {new Date(p.generated_at).toLocaleTimeString()} · refreshes every 30 seconds</p>
      <button className="inline-flex items-center gap-1.5 hover:text-foreground" disabled={q.isFetching} onClick={() => void q.refetch()}><RefreshCwIcon className="size-3" aria-hidden />Refresh evidence</button>
    </div>
  </div>;
}

function PriorityFindings({ rows, scope, setScope }: { rows: FleetPriority[]; scope: string; setScope: (scope: string) => void }) {
  const visible = scope === "recent" ? rows.filter((f) => f.state !== "existing") : rows;
  const columns: Column<FleetPriority>[] = [
    { key: "title", sortable: false, header: "Investigate", accessor: (f) => <div className="space-y-1.5 min-w-0">
      <div className="flex flex-wrap gap-2 items-center"><SeverityBadge severity={f.severity} size="sm" /><span className="text-xs capitalize text-muted-foreground">{f.state}</span></div>
      <Link to="/findings/$findingId" params={{ findingId: f.finding_id }} className={`${linkClass} block break-words`}>{f.title}</Link>
      <p className="text-xs text-muted-foreground break-all">{f.tool} · {f.file_path}{f.line > 0 ? `:${f.line}` : ""}</p>
      <p className="text-xs text-muted-foreground">Reported confidence: {f.confidence || "not recorded"}</p>
    </div> },
    { key: "repo", sortable: false, header: "Repository", accessor: (f) => <Link to="/repos/$repoId" params={{ repoId: f.repo_id }} className="hover:underline break-words">{f.name}</Link> },
  ];
  return <section id="priority-findings" aria-labelledby="priority-heading" className="scroll-mt-20 min-w-0"><Card>
    <CardHeader className="flex-wrap"><h2 id="priority-heading" className="font-semibold">Act on high-risk findings</h2><Select value={scope} onValueChange={setScope}><SelectTrigger aria-label="Finding priority scope" className="w-full sm:w-auto text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="recent">New / resurfaced · 7 days</SelectItem><SelectItem value="all">All open critical / high</SelectItem></SelectContent></Select></CardHeader>
    <CardContent className="space-y-3"><p className="text-xs text-muted-foreground">Security findings, ranked by severity. Open an issue to inspect evidence and start a fix. Partial scans retain earlier known findings.</p>
      <DataTable data={visible} columns={columns} keyExtractor={(r) => r.finding_id} searchable={false} columnVisibilityControl={false} pageSize={5} density="compact" emptyMessage={scope === "recent" ? `No new or resurfaced high-risk findings observed. ${rows.filter((f) => f.state === "existing").length} older high-risk findings remain; check coverage before drawing conclusions.` : "No open high-risk findings observed. Check scan coverage before drawing conclusions."} />
    </CardContent>
  </Card></section>;
}

function ScanCoverage({ posture: p, scope, setScope }: { posture: FleetPosture; scope: string; setScope: (scope: string) => void }) {
  const rows = p.coverage.rows.filter((r) => scope === "all" || r.status === "failed" || r.status === "incomplete");
  const columns: Column<FleetCoverageRow>[] = [
    { key: "repo", sortable: false, header: "Repository / branch", accessor: (r) => <div className="space-y-1"><Link className="font-medium hover:underline" to="/repos/$repoId" params={{ repoId: r.repo_id }}>{r.name}</Link><p className="text-xs text-muted-foreground break-all">{r.branch || "Working tree"}</p></div> },
    { key: "status", sortable: false, header: "Latest result", accessor: (r) => <div className="space-y-2"><div className="flex flex-wrap gap-1"><StatusBadge status={r.status === "fresh" ? "completed" : r.status} label={coverageLabels[r.status]} size="sm" />{r.gate_failing && <StatusBadge status="failed" label="Gate failing" size="sm" />}</div><p className="text-xs text-muted-foreground">{r.detail}</p><p className="text-xs text-muted-foreground">Last complete: {r.last_complete_at ? new Date(r.last_complete_at).toLocaleDateString() : "never"}</p>
      {r.scan_id ? <Link className={`${linkClass} text-xs`} to="/scans/$scanId" params={{ scanId: r.scan_id }}>Inspect scan →</Link> : <Link className={`${linkClass} text-xs`} to="/scans" search={{ repo_id: r.repo_id }}>Run first scan →</Link>}
    </div> },
  ];
  return <section id="scan-coverage" aria-labelledby="coverage-heading" className="scroll-mt-20 min-w-0"><Card>
    <CardHeader className="flex-wrap"><h2 id="coverage-heading" className="font-semibold">Latest scans & coverage</h2><Select value={scope} onValueChange={setScope}><SelectTrigger aria-label="Coverage scope" className="w-full sm:w-auto text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="attention">Failed / incomplete</SelectItem><SelectItem value="all">All repositories / branches</SelectItem></SelectContent></Select></CardHeader>
    <CardContent className="space-y-3"><p className="text-sm"><strong>{p.coverage.fresh_repos} of {p.repo_count}</strong> repositories have complete scans on every tracked branch in the last {p.coverage.fresh_days} days.</p>
      <progress aria-label="Repositories with fresh, complete coverage" value={p.coverage.fresh_repos} max={Math.max(1, p.repo_count)} className="h-2 w-full accent-primary" />
      <p className="text-xs text-muted-foreground">Complete means all selected tools ran. It does not imply every possible tool or path was selected.</p>
      <DataTable data={rows} columns={columns} keyExtractor={(r) => `${r.repo_id}/${r.branch}`} searchable={false} columnVisibilityControl={false} pageSize={4} density="compact" emptyMessage="No failed or incomplete latest scans in the tracked branches." />
    </CardContent>
  </Card></section>;
}

function ProgressEvidence({ posture: p }: { posture: FleetPosture }) {
  const c = p.changes;
  const comparable = c.compared_branches > 0;
  const columns: Column<FleetChangeRow>[] = [
    { key: "repo", sortable: false, header: "Repository / branch", accessor: (r) => <div><Link to="/repos/$repoId" params={{ repoId: r.repo_id }} className="font-medium hover:underline">{r.name}</Link><p className="text-xs text-muted-foreground break-all">{r.branch || "Working tree"}</p></div> },
    { key: "result", sortable: false, header: "What changed", accessor: (r) => r.reason ? <span className="text-xs text-muted-foreground">Not compared: {r.reason}</span> : <span className="text-xs">{r.new} new · {r.resurfaced} resurfaced · {r.resolved} verified resolved · {r.suppressed} set aside</span> },
    { key: "evidence", sortable: false, header: "Review evidence", accessor: (r) => <div className="flex flex-wrap gap-x-3 gap-y-2 text-xs">{r.scan_id && <Link className={linkClass} to="/scans/$scanId" params={{ scanId: r.scan_id }}>Latest scan</Link>}{r.baseline_scan_id && <Link className={linkClass} to="/scans/$scanId" params={{ scanId: r.baseline_scan_id }}>Baseline scan</Link>}{!r.scan_id && <Link className={linkClass} to="/scans" search={{ repo_id: r.repo_id }}>Run first scan</Link>}</div> },
  ];
  return <section aria-labelledby="progress-heading"><Card>
    <CardHeader className="flex-wrap"><h2 id="progress-heading" className="font-semibold">Are we improving?</h2><span className="text-xs text-muted-foreground">Last {c.window_days} days · {c.compared_branches} branches compared · {c.excluded_branches} excluded</span></CardHeader>
    <CardContent className="space-y-4">
      {!comparable && <p className="rounded-md border border-border bg-muted/30 p-3 text-sm">Progress is unknown. We need complete scans before and during this window with matching tools, scope, and scanner release.</p>}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        {[{ label: "New", value: c.new }, { label: "Resurfaced", value: c.resurfaced }, { label: "Verified resolved", value: c.resolved }, { label: "Suppressed / set aside", value: c.suppressed }].map((m) => <a href="#comparison-evidence" key={m.label} className="rounded-md border border-border p-3 hover:bg-muted/30"><div className="text-xs text-muted-foreground">{m.label}</div><div className="text-2xl font-semibold tabular-nums mt-1">{comparable ? m.value : "—"}</div></a>)}
      </div>
      <p className="text-xs text-muted-foreground">Distinct security issue signatures across comparable branches. Resolved means absent in a complete rescan and absent from other current branch observations. Suppressions and triage changes receive no resolution credit. First scans, changed scope, and incomplete coverage are excluded.</p>
      <div id="comparison-evidence" className="scroll-mt-20"><DataTable data={c.rows} columns={columns} keyExtractor={(r) => `${r.repo_id}/${r.branch}`} searchable={false} columnVisibilityControl={false} pageSize={5} density="compact" emptyMessage="No scan evidence to compare yet." /></div>
    </CardContent>
  </Card></section>;
}

function fixNextStep(job: FixJob): string {
  if (isFixPaused(job.status)) return "Review proposed changes";
  if (job.status === "succeeded") return "Verify on the source branch";
  if (job.status === "failed") return "Inspect failed fix";
  return "Follow fix progress";
}

function WorkInProgress({ posture: p }: { posture: FleetPosture }) {
  const autofix = useFlag("autofix_enabled");
  const fixes = useFixJobs(autofix.enabled);
  const jobs = (fixes.data ?? []).filter((j) => !["cancelled", "superseded"].includes(j.status));
  const columns: Column<FixJob>[] = [
    { key: "branch", sortable: false, header: "Your fix jobs", accessor: (j) => <div className="space-y-1"><Link className={linkClass} to="/agents/$agentId" params={{ agentId: j.id }}>{j.result_branch || j.target_branch || j.id.slice(0, 8)}</Link><div><StatusBadge status={j.status} label={j.status.replaceAll("_", " ")} size="sm" /></div></div> },
    { key: "next", sortable: false, header: "Next step", accessor: (j) => <div><Link className={`${linkClass} text-xs`} to="/agents/$agentId" params={{ agentId: j.id }}>{fixNextStep(j)} →</Link>{j.status === "succeeded" && <p className="text-xs text-muted-foreground mt-1">Agent completion alone does not verify a fix on the source branch.</p>}</div> },
  ];
  return <section id="work-in-progress" aria-labelledby="work-heading" className="scroll-mt-20"><Card>
    <CardHeader><h2 id="work-heading" className="font-semibold">Keep work moving</h2><Link to="/scans" className={`${linkClass} text-xs`}>All scans →</Link></CardHeader>
    <CardContent className="space-y-4">
      {p.work.length === 0 ? <p className="text-sm text-muted-foreground">No scans queued or running.</p> : <ul className="divide-y divide-border">{p.work.map((s) => <li key={s.scan_id} className="flex flex-wrap items-center justify-between gap-3 py-3"><div className="min-w-0"><Link className={linkClass} to="/scans/$scanId" params={{ scanId: s.scan_id }}>{s.name} · {s.branch || "default source"}</Link><p className="text-xs text-muted-foreground mt-1">{s.stalled ? "Worker lease expired. Inspect the scan and retry if needed." : s.phase || "Waiting for a worker"}</p></div><StatusBadge status={s.stalled ? "failed" : s.status} label={s.stalled ? "Lease expired" : s.status} /></li>)}</ul>}
      {autofix.enabled && <DataTable data={jobs} columns={columns} keyExtractor={(j) => j.id} searchable={false} columnVisibilityControl={false} pageSize={4} density="compact" loading={fixes.isLoading} isError={fixes.isError} onRetry={() => void fixes.refetch()} emptyMessage="You have no fix jobs awaiting action." />}
      <WorkerHealth />
    </CardContent>
  </Card></section>;
}

function WorkerHealth() {
  const admin = useIsAdmin();
  const runtime = useRuntimeCapabilities();
  const q = useQuery({
    queryKey: ["scanners", "workers"], enabled: admin && runtime.data?.queue_execution === true,
    queryFn: async () => (await api.get<{ id: string; status: string; active_scans: number; capacity: number }[]>("/scanners/workers")).data ?? [],
    refetchInterval: 30_000,
  });
  if (!admin) return null;
  if (runtime.isError) return <p className="text-xs text-muted-foreground">Worker execution mode is unavailable. <button className={linkClass} onClick={() => void runtime.refetch()}>Retry worker health</button></p>;
  if (!runtime.data?.queue_execution) return null;
  if (q.isLoading) return <p className="text-xs text-muted-foreground">Checking worker heartbeats…</p>;
  if (q.isError) return <p className="text-xs text-muted-foreground">Worker health is unknown. <button className={linkClass} onClick={() => void q.refetch()}>Retry worker health</button></p>;
  return <p className="border-t border-border pt-3 text-xs text-muted-foreground">{q.data?.length ? `${q.data.length} workers reported in the last minute · ${q.data.reduce((n, w) => n + w.active_scans, 0)} active scans / ${q.data.reduce((n, w) => n + w.capacity, 0)} capacity` : "No workers reported in the last minute. Queued scans may be waiting for a worker."} <Link to="/settings" search={{ tab: "scanners" }} className={linkClass}>Inspect scanner settings →</Link></p>;
}

function QualityVolume() {
  const q = useQuery({
    queryKey: ["findings", "home-quality"],
    queryFn: async () => {
      const r = await api.get<Finding[]>("/findings?category=quality&status=open&per_page=5&sort=created_at&order=desc");
      if (typeof r.meta?.total !== "number") throw new Error("Quality total unavailable");
      return { rows: r.data ?? [], total: r.meta.total };
    },
    refetchInterval: 30_000,
  });
  if (q.isPending) return <p role="status" className="text-xs text-muted-foreground">Loading code-quality volume…</p>;
  if (q.isError) return <p role="alert" className="text-xs text-muted-foreground">Code-quality volume is unknown. <button className={linkClass} onClick={() => void q.refetch()}>Retry quality count</button></p>;
  return <Section className="text-sm" triggerClassName="font-normal text-muted-foreground" title={<>Code quality · {q.data.total.toLocaleString()} open {q.data.total === 1 ? "issue" : "issues"}</>}>
    <p className="mt-3 text-xs text-muted-foreground">Quality checks are separate from security risk. Showing {q.data.rows.length} of {q.data.total} current issues.</p>
    <ul className="mt-2 divide-y divide-border">{q.data.rows.map((f) => <li key={f.id} className="py-2"><Link to="/findings/$findingId" params={{ findingId: f.id }} className={linkClass}>{f.title}</Link><p className="mt-1 text-xs text-muted-foreground break-all">{f.file_path}</p></li>)}</ul>
  </Section>;
}
