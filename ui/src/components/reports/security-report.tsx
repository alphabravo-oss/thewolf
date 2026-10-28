// The printable "Security posture" report. Renders live (/fleet/report) and
// saved snapshots from the same JSON; "Download PDF" is the browser's print
// dialog with the @media print rules in styles/globals.css.
import { GitPullRequestIcon, OctagonAlertIcon, RadarIcon, ShieldAlertIcon, TrendingUpIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Bar, BarChart, ResponsiveContainer, XAxis, YAxis } from "recharts";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { MetricCard } from "@/components/ui/metric-card";
import { Section } from "@/components/ui/section";
import { axis, count, DataTableText, gap, grid, label, legend, tip } from "@/components/fleet/dashboard-charts";
import { critHighTrend, SEVERITIES, severityFill, trendRows } from "@/components/fleet/dashboard-data";
import { scopeName, type SecurityReport } from "@/lib/reports";

export const OBSERVED_NOTE = "Observed, not audit-grade: figures are what Wolf's scans observed at the time shown, using each finding's current triage state.";

export function SecurityReportView({ report: r, title = "Security posture" }: { report: SecurityReport; title?: string }) {
  const sev = r.open_by_severity ?? {};
  const critHigh = (sev.critical ?? 0) + (sev.high ?? 0);
  const { days, delta } = critHighTrend(r.trend ?? []);
  const rows = trendRows(r.trend ?? [], 30);
  const c = r.changes;
  const comparable = c.compared_branches > 0;
  const coverage = r.repo_count ? Math.round((r.coverage.fresh_repos / r.repo_count) * 100) : 0;
  const trend = delta === undefined ? {} : { trend: delta > 0 ? "up" as const : delta < 0 ? "down" as const : "flat" as const, trendValue: `${delta > 0 ? "+" : ""}${delta} vs 7 days earlier` };
  return <article className="report space-y-5" aria-label={title}>
    <header className="report-header space-y-1">
      <h2 className="text-xl font-semibold tracking-tight">{title}</h2>
      <p className="text-sm text-muted-foreground">{scopeName(r.collection?.name)} · generated {new Date(r.generated_at).toLocaleString()}</p>
      <p className="text-xs text-muted-foreground">{OBSERVED_NOTE}</p>
    </header>

    <section aria-label="Summary" className="report-section grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
      <MetricCard title="Open critical / high" value={critHigh} subtitle={`Critical ${sev.critical ?? 0} · High ${sev.high ?? 0}`} icon={<ShieldAlertIcon className="size-4" aria-hidden />} sparkline={days.length > 1 ? days : undefined} {...trend} />
      <MetricCard title="New vs resolved this week" value={comparable ? `${c.new + c.resurfaced} / ${c.resolved}` : "—"} subtitle={comparable ? `New (incl. resurfaced) / verified resolved on ${c.compared_branches} comparable ${c.compared_branches === 1 ? "branch" : "branches"}` : "Unknown: no comparable scans this week"} icon={<TrendingUpIcon className="size-4" aria-hidden />} />
      <MetricCard title="Scan coverage" value={`${coverage}%`} percentage={coverage} subtitle={`${r.coverage.fresh_repos} of ${r.repo_count} repositories complete in ${r.coverage.fresh_days} days`} icon={<RadarIcon className="size-4" aria-hidden />} />
      <MetricCard title="Gates failing" value={r.gates_failing} subtitle={`${r.gates_failing === 1 ? "Repository" : "Repositories"} failing on the latest scan`} icon={<OctagonAlertIcon className="size-4" aria-hidden />} />
      <MetricCard title="Fix branches pushed" value={r.fixes_pushed} subtitle="To date, in scope. PR and merge state is not tracked" icon={<GitPullRequestIcon className="size-4" aria-hidden />} />
    </section>

    <ReportCard title="Open critical / high, last 30 days" description="Daily snapshots of distinct open locations.">
      {rows.length === 0 ? <p className="text-sm text-muted-foreground">No scans in this period.</p> : <>
        <div className="h-56" aria-hidden><ResponsiveContainer width="100%" height="100%">
          <BarChart accessibilityLayer={false} maxBarSize={28} data={rows} margin={{ top: 4, right: 8, bottom: 0, left: 0 }} barCategoryGap={1}>
            {grid}
            <XAxis dataKey="date" {...axis} minTickGap={24} tickFormatter={(d: string) => d.slice(5)} />
            <YAxis {...axis} {...count} axisLine={false} width={36} />
            {tip}{legend}
            {(["critical", "high"] as const).map((s) => <Bar key={s} dataKey={s} name={label(s)} stackId="sev" fill={severityFill[s]} {...gap} isAnimationActive={false} />)}
          </BarChart>
        </ResponsiveContainer></div>
        <Section variant="plain" className="print-hidden text-xs" title="Data table"><DataTableText caption="Open critical and high findings per day" head={["Day", "Critical", "High"]} rows={[...rows].reverse().map((d) => [d.date, d.critical, d.high])} /></Section>
      </>}
    </ReportCard>

    <div className="grid grid-cols-1 gap-5 xl:grid-cols-2">
      <ReportCard title="Age of open critical / high" description="Days since each open critical or high security issue was first observed. Each row includes the older rows.">
        <DataTableText caption="Open critical and high findings by age" head={["Age", "Critical", "High"]} rows={(r.aging ?? []).map((a) => [`Over ${a.days} days`, a.critical, a.high])} />
      </ReportCard>
      <ReportCard title="Top 10 riskiest repositories" description="By open critical, then high findings.">
        {(r.top_repos ?? []).length === 0 ? <p className="text-sm text-muted-foreground">No open findings in any repository.</p>
          : <DataTableText caption="Open findings per repository by severity" head={["Repository", ...SEVERITIES.map(label), "Total"]} rows={r.top_repos.map((t) => [t.name, ...SEVERITIES.map((s) => t[s]), t.total])} />}
      </ReportCard>
    </div>
  </article>;
}

function ReportCard({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return <Card className="report-section min-w-0">
    <CardHeader className="block space-y-1"><h3 className="font-semibold">{title}</h3><p className="text-xs text-muted-foreground">{description}</p></CardHeader>
    <CardContent>{children}</CardContent>
  </Card>;
}
