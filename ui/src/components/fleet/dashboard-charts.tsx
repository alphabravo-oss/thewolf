// Home charts. Each card loads on its own query (shared react-query keys with
// the list pages), so a slow or failing endpoint only affects its own card.
// Every chart has a title, a one-line meaning, a data table with keyboard
// drill-down links, and click-through on its marks.
import { useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { InfoIcon } from "lucide-react";
import { Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api } from "@/lib/api";
import { useFindingsByRepo, useTopVulnerableRules, type FleetPosture } from "@/lib/fleet";
import { useFixJobs } from "@/lib/fixes";
import type { Scan } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Section } from "@/components/ui/section";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip as Hint, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import {
  SEVERITIES, severityFill, trendRows, ageBuckets, weeklyChanges, topRepos, toolHealth, fixFunnel,
  type SeverityKey, type TrendEntry,
} from "./dashboard-data";

const linkClass = "font-medium text-primary hover:underline underline-offset-4";
const label = (s: string) => s[0].toUpperCase() + s.slice(1);
const short = (s: string, n = 22) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);
const axis = { stroke: "var(--color-muted-foreground)", fontSize: 11, tickLine: false } as const;
// Count axes: whole numbers, compact ticks (60K) so labels fit the axis.
const count = { allowDecimals: false, tickFormatter: new Intl.NumberFormat("en", { notation: "compact" }).format } as const;
const grid = <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" />;
// Keep series in severity order (Recharts sorts tooltip and legend by name).
const tip = <Tooltip itemSorter={(i) => SEVERITIES.indexOf(String(i.dataKey) as SeverityKey)} cursor={{ fill: "var(--color-muted)" }} contentStyle={{ background: "var(--color-popover)", border: "1px solid var(--color-border)", borderRadius: 4, fontSize: 12, color: "var(--color-popover-foreground)" }} />;
const legend = <Legend itemSorter={null} wrapperStyle={{ fontSize: 12 }} iconType="square" formatter={(v: string) => <span className="text-foreground">{v}</span>} />;
// 2px card-coloured gap between adjacent fills.
const gap = { stroke: "var(--color-card)", strokeWidth: 1 } as const;

// Category-axis width and label length for horizontal bars; phones get a narrower axis.
const narrowQuery = "(max-width: 640px)";
function useCategoryAxis(wide: number, chars: number) {
  const narrow = useSyncExternalStore((cb) => {
    const m = window.matchMedia(narrowQuery);
    m.addEventListener("change", cb);
    return () => m.removeEventListener("change", cb);
  }, () => window.matchMedia(narrowQuery).matches);
  return narrow ? { width: 96, tickFormatter: (v: string) => short(v, 12) } : { width: wide, tickFormatter: (v: string) => short(v, chars) };
}

type Loadable = { isPending: boolean; isError: boolean; refetch: () => unknown; errorUpdateCount: number };

export function useFindingTrends() {
  return useQuery({
    queryKey: ["findings", "trends"],
    queryFn: async () => (await api.get<TrendEntry[]>("/findings/trends")).data ?? [],
    staleTime: 60_000,
    retry: 3,
  });
}

// Newest 100 root scans; /scans has no date filter, so this is the bounded window.
export function useRecentScans() {
  return useQuery({
    queryKey: ["scans", "recent-roots"],
    queryFn: async () => (await api.get<Scan[]>("/scans?per_page=100&roots=1&sort=started&order=desc")).data ?? [],
    staleTime: 30_000,
    retry: 3,
  });
}

/** Card shell: title, meaning, per-card loading/error/empty, data table. */
export function ChartCard({ id, title, info, description, q, empty, table, actions, className, children }: {
  id: string; title: string; info?: string; description: string; q: Loadable;
  empty?: string | false; table: ReactNode; actions?: ReactNode; className?: string; children: ReactNode;
}) {
  // Endpoints can 500 briefly after a restart: retry a failed card on its own.
  useEffect(() => {
    if (!q.isError || q.errorUpdateCount > 4) return;
    const t = setTimeout(() => void q.refetch(), 10_000);
    return () => clearTimeout(t);
  }, [q.isError, q.errorUpdateCount, q]);
  const headingId = `${id}-title`;
  return <section id={id} aria-labelledby={headingId} className={cn("min-w-0 scroll-mt-20", className)}><Card className="h-full">
    <CardHeader className="flex-wrap items-start gap-2">
      <div className="min-w-0 flex-1 basis-64 space-y-1"><div className="flex items-center gap-1.5">
        <h2 id={headingId} className="font-semibold">{title}</h2>
        {info && <TooltipProvider delayDuration={150}><Hint><TooltipTrigger asChild>
          <button type="button" aria-label={`About ${title}`} className="rounded text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-primary"><InfoIcon className="size-3.5" aria-hidden /></button>
        </TooltipTrigger><TooltipContent className="max-w-xs text-xs">{info}</TooltipContent></Hint></TooltipProvider>}
      </div>
      <p className="text-xs text-muted-foreground">{description}</p></div>
      {actions}
    </CardHeader>
    <CardContent className="space-y-2">
      {q.isPending ? <Skeleton role="status" aria-label={`Loading ${title}`} className="h-56 w-full" />
        : q.isError ? <div role="alert" className="flex h-56 flex-col items-center justify-center gap-3 rounded-md border border-dashed border-border text-center text-sm">
          <p>{title} could not be loaded.</p>
          <Button size="sm" variant="outline" onClick={() => void q.refetch()}>Retry</Button>
        </div>
        : empty ? <p className="grid h-40 place-items-center text-center text-sm text-muted-foreground">{empty}</p>
        : <>
          <div className="h-60" aria-hidden>{children}</div>
          <Section variant="plain" className="text-xs" title="Data table">{table}</Section>
        </>}
    </CardContent>
  </Card></section>;
}

function DataTableText({ caption, head, rows }: { caption: string; head: string[]; rows: ReactNode[][] }) {
  return <div className="mt-2 overflow-x-auto"><table className="data-table w-full text-left text-xs">
    <caption className="sr-only">{caption}</caption>
    <thead><tr>{head.map((h, i) => <th key={h} scope="col" className={cn("border-b border-border py-1.5 pr-3 font-medium text-muted-foreground", i > 0 && "text-right")}>{h}</th>)}</tr></thead>
    <tbody>{rows.map((r, i) => <tr key={i} className="border-b border-border/60 last:border-0">{r.map((c, j) => j === 0 ? <th key={j} scope="row" className="py-1.5 pr-3 text-left font-normal">{c}</th> : <td key={j} className="py-1.5 pr-3 text-right tabular-nums">{c}</td>)}</tr>)}</tbody>
  </table></div>;
}

// Recharts passes the clicked row on `payload`.
type Clicked<T> = { payload?: T };

// 1. Observed open findings (daily), stacked by severity.
export function FindingsOverTime() {
  const q = useFindingTrends();
  const navigate = useNavigate();
  const [days, setDays] = useState<30 | 90>(30);
  const rows = trendRows(q.data ?? [], days);
  const open = (severity: SeverityKey) => void navigate({ to: "/findings", search: { view: "list", scope: "all", severity } });
  return <ChartCard id="chart-trend" className="xl:col-span-2" title="Observed open findings (daily)" q={q}
    info="Daily snapshots of distinct open locations. Snapshots use each finding's current triage state, so this is not an audit-grade history."
    description={`Open findings each day over the last ${days} days. Click a severity to open those findings (the list has no date filter, so it shows today's).`}
    empty={rows.length === 0 && "No scans yet. Run scans over time to see the trend."}
    actions={<div className="flex gap-1" role="group" aria-label="Trend window">
      {([30, 90] as const).map((d) => <Button key={d} size="sm" variant={days === d ? "default" : "outline"} aria-pressed={days === d} className="h-7 px-2 text-xs" onClick={() => setDays(d)}>{d} days</Button>)}
    </div>}
    table={<DataTableText caption="Observed open findings per day" head={["Day", ...SEVERITIES.map(label)]} rows={[...rows].reverse().map((r) => [r.date, ...SEVERITIES.map((s) => r[s] > 0 ? <Link key={s} to="/findings" search={{ view: "list", scope: "all", severity: s }} className={linkClass}>{r[s]}</Link> : 0)])} />}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} margin={{ top: 4, right: 8, bottom: 0, left: 0 }} barCategoryGap={1}>
        {grid}
        <XAxis dataKey="date" {...axis} minTickGap={24} tickFormatter={(d: string) => d.slice(5)} />
        <YAxis {...axis} {...count} axisLine={false} width={36} />
        {tip}{legend}
        {SEVERITIES.map((s) => <Bar key={s} dataKey={s} name={label(s)} stackId="sev" fill={severityFill[s]} {...gap} className="cursor-pointer" onClick={() => open(s)} />)}
      </BarChart>
    </ResponsiveContainer>
  </ChartCard>;
}



// 2. New vs resolved: the seven-day comparison per comparable branch. The API
// exposes one comparison window, not a weekly history.
export function NewVsResolved({ q }: { q: Loadable & { data?: FleetPosture } }) {
  const navigate = useNavigate();
  const cat = useCategoryAxis(160, 24);
  const c = q.data?.changes;
  const rows = weeklyChanges(c?.rows ?? []);
  const open = (r?: { scan_id?: string; repo_id: string }) => {
    if (r?.scan_id) void navigate({ to: "/scans/$scanId", params: { scanId: r.scan_id } });
    else if (r) void navigate({ to: "/repos/$repoId", params: { repoId: r.repo_id } });
  };
  return <ChartCard id="chart-changes" title="New vs resolved this week" q={q}
    description={c ? `Last ${c.window_days} days on ${c.compared_branches} comparable ${c.compared_branches === 1 ? "branch" : "branches"} (${c.excluded_branches} excluded). New includes resurfaced; resolved means verified absent in a complete rescan. Click a bar for the scan evidence.` : "Seven-day comparison of complete, comparable rescans."}
    empty={rows.length === 0 && "Progress is unknown: no branch has a comparable complete scan before and during this week."}
    table={<DataTableText caption="New and verified resolved findings per branch this week" head={["Branch", "New", "Resolved", "Evidence"]} rows={rows.map((r) => [r.label, r.new, r.resolved,
      <span key="e" className="inline-flex gap-2">{r.scan_id && <Link to="/scans/$scanId" params={{ scanId: r.scan_id }} className={linkClass}>Latest scan</Link>}{r.baseline_scan_id && <Link to="/scans/$scanId" params={{ scanId: r.baseline_scan_id }} className={linkClass}>Baseline</Link>}</span>])} />}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} layout="vertical" margin={{ top: 4, right: 12, bottom: 0, left: 0 }} barGap={2}>
        {grid}
        <XAxis type="number" {...axis} {...count} />
        <YAxis type="category" dataKey="label" {...axis} {...cat} />
        {tip}{legend}
        <Bar dataKey="new" name="New" fill="var(--chart-2)" radius={[0, 4, 4, 0]} className="cursor-pointer" onClick={(d: Clicked<(typeof rows)[number]>) => open(d.payload)} />
        <Bar dataKey="resolved" name="Resolved" fill="var(--chart-1)" radius={[0, 4, 4, 0]} className="cursor-pointer" onClick={(d: Clicked<(typeof rows)[number]>) => open(d.payload)} />
      </BarChart>
    </ResponsiveContainer>
  </ChartCard>;
}

// 3. Riskiest repositories, stacked by severity.
export function RiskiestRepos() {
  const q = useFindingsByRepo();
  const navigate = useNavigate();
  const cat = useCategoryAxis(160, 24);
  const rows = topRepos(q.data ?? []);
  return <ChartCard id="chart-repos" title="Riskiest repositories" q={q}
    description="Top 10 by open critical, then high findings. Click a segment to open that repository filtered to the severity."
    empty={rows.length === 0 && "No open findings in any repository."}
    table={<DataTableText caption="Open findings per repository by severity" head={["Repository", ...SEVERITIES.map(label), "Total"]} rows={rows.map((r) => [<Link key="r" to="/repos/$repoId" params={{ repoId: r.repo_id }} search={{ scope: "all" }} className={linkClass}>{r.name}</Link>, ...SEVERITIES.map((s) => r[s]), r.total])} />}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} layout="vertical" margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
        {grid}
        <XAxis type="number" {...axis} {...count} />
        <YAxis type="category" dataKey="name" {...axis} {...cat} />
        {tip}{legend}
        {SEVERITIES.map((s) => <Bar key={s} dataKey={s} name={label(s)} stackId="sev" fill={severityFill[s]} {...gap} className="cursor-pointer"
          onClick={(d: Clicked<(typeof rows)[number]>) => d.payload && void navigate({ to: "/repos/$repoId", params: { repoId: d.payload.repo_id }, search: { scope: "all", severity: s } })} />)}
      </BarChart>
    </ResponsiveContainer>
  </ChartCard>;
}

// 4. Top rules / CWE among open security findings.
export function TopRules() {
  const q = useTopVulnerableRules(10, true);
  const navigate = useNavigate();
  const cat = useCategoryAxis(170, 22);
  const rows = (q.data ?? []).slice(0, 10).map((r) => ({ ...r, name: r.title || r.key }));
  return <ChartCard id="chart-rules" title="Top rules" q={q}
    description="Security rules and CVEs with the most open findings. Click a bar to see that rule across repositories."
    empty={rows.length === 0 && "No open security findings."}
    table={<DataTableText caption="Open findings per rule" head={["Rule", "Findings", "Repositories"]} rows={rows.map((r) => [<Link key="r" to="/findings" search={{ view: "rule", q: r.key }} className={linkClass}>{r.name}<span className="ml-1 font-mono text-muted-foreground">{r.key !== r.name ? r.key : ""}</span></Link>, r.findings, r.repos])} />}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} layout="vertical" margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
        {grid}
        <XAxis type="number" {...axis} {...count} />
        <YAxis type="category" dataKey="name" {...axis} {...cat} />
        {tip}
        <Bar dataKey="findings" name="Open findings" fill="var(--chart-1)" radius={[0, 4, 4, 0]} className="cursor-pointer"
          onClick={(d: Clicked<(typeof rows)[number]>) => d.payload && void navigate({ to: "/findings", search: { view: "rule", q: d.payload.key } })} />
      </BarChart>
    </ResponsiveContainer>
  </ChartCard>;
}

const oldestFirst = (severity = "critical,high") => ({ view: "list" as const, scope: "all" as const, severity, sort: "first_seen", order: "asc" as const });

// 5. Age of open critical/high security findings.
export function FindingAge({ q }: { q: Loadable & { data?: FleetPosture } }) {
  const navigate = useNavigate();
  // Pinned at mount so the buckets don't shift between 30-second refreshes.
  const [now] = useState(() => Date.now());
  const rows = ageBuckets(q.data?.priorities ?? [], now);
  const total = rows.reduce((n, r) => n + r.critical + r.high, 0);
  return <ChartCard id="chart-age" title="Age of open critical / high" q={q}
    description="Days since each open critical or high security issue was first observed. Click to list them oldest first."
    empty={total === 0 && "No open critical or high security findings."}
    table={<DataTableText caption="Open critical and high findings by age" head={["Age", "Critical", "High"]} rows={rows.map((r) => [r.label,
      <Link key="c" to="/findings" search={oldestFirst("critical")} className={linkClass}>{r.critical}</Link>, <Link key="h" to="/findings" search={oldestFirst("high")} className={linkClass}>{r.high}</Link>])} />}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
        {grid}
        <XAxis dataKey="label" {...axis} />
        <YAxis {...axis} {...count} axisLine={false} width={36} />
        {tip}{legend}
        {(["critical", "high"] as const).map((s) => <Bar key={s} dataKey={s} name={label(s)} stackId="sev" fill={severityFill[s]} {...gap} className="cursor-pointer"
          onClick={() => void navigate({ to: "/findings", search: oldestFirst(s) })} />)}
      </BarChart>
    </ResponsiveContainer>
  </ChartCard>;
}

// 6. Scanner health: per-tool success over the newest 100 root scans.
export function ScannerHealth() {
  const q = useRecentScans();
  const navigate = useNavigate();
  const cat = useCategoryAxis(110, 16);
  const rows = toolHealth(q.data ?? []).slice(0, 10);
  return <ChartCard id="chart-scanners" title="Scanner health" q={q}
    description="Share of finished runs each tool completed, over the newest 100 scans; least reliable first. Click for scanner coverage."
    empty={rows.length === 0 && "No finished scans yet."}
    table={<DataTableText caption="Scanner success rate" head={["Tool", "Success", "Runs", "Failed"]} rows={rows.map((r) => [<Link key="t" to="/coverage" className={linkClass}>{r.tool}</Link>, `${r.rate}%`, r.runs, r.failed])} />}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} layout="vertical" margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
        {grid}
        <XAxis type="number" {...axis} domain={[0, 100]} unit="%" />
        <YAxis type="category" dataKey="tool" {...axis} {...cat} />
        {tip}
        <Bar dataKey="rate" name="Success rate (%)" fill="var(--chart-1)" radius={[0, 4, 4, 0]} className="cursor-pointer" onClick={() => void navigate({ to: "/coverage" })} />
      </BarChart>
    </ResponsiveContainer>
  </ChartCard>;
}

// 7. Fix pipeline funnel (autofix only).
export function FixFunnel() {
  const q = useFixJobs(true);
  const navigate = useNavigate();
  const cat = useCategoryAxis(120, 18);
  const rows = fixFunnel(q.data ?? []);
  return <ChartCard id="chart-fixes" title="Fix pipeline" q={q}
    description="Your newest 200 fix jobs by the furthest stage reached. Pull-request and merge state is not recorded. Click a stage for the jobs at it."
    empty={rows[0].reached === 0 && "No fix jobs yet."}
    table={<DataTableText caption="Fix jobs by stage" head={["Stage", "Reached", "At this stage now"]} rows={rows.map((r) => [r.label, r.reached, <Link key="s" to="/agents" search={{ stage: r.key }} className={linkClass}>{r.at}</Link>])} />}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} layout="vertical" margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
        {grid}
        <XAxis type="number" {...axis} {...count} />
        <YAxis type="category" dataKey="label" {...axis} {...cat} />
        {tip}
        <Bar dataKey="reached" name="Jobs reaching stage" fill="var(--chart-1)" radius={[0, 4, 4, 0]} className="cursor-pointer"
          onClick={(d: Clicked<(typeof rows)[number]>) => d.payload && void navigate({ to: "/agents", search: { stage: d.payload.key } })} />
      </BarChart>
    </ResponsiveContainer>
  </ChartCard>;
}
