import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { BotIcon, BugIcon, GaugeIcon } from "lucide-react";
import { useMemo } from "react";
import { FIX_STAGES, fixStage, type FixStage } from "@/components/fleet/dashboard-data";
import { useFlag } from "@/lib/flags";
import { isFixPaused, useFixJobs, type FixJob } from "@/lib/fixes";
import { EmptyState, ErrorState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { MetricCard } from "@/components/ui/metric-card";
import { RowActions } from "@/components/ui/row-actions";
import { FixerSetupBanner } from "@/components/fixes/fixer-setup-banner";
import { StatusBadge } from "@/components/fixes/status-badge";

export const Route = createFileRoute("/_authed/agents/")({
  // ?stage= narrows to jobs at one fix-pipeline stage (Home funnel drill-down).
  validateSearch: (s: Record<string, unknown>): { stage?: FixStage } => ({
    stage: FIX_STAGES.some((f) => f.key === s.stage) ? (s.stage as FixStage) : undefined,
  }),
  component: AgentsPage,
});

// Stable empty array — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_JOBS: FixJob[] = [];

function AgentsPage() {
  const navigate = useNavigate();
  const { enabled, isLoading: flagLoading, isError: flagError, refetch: retryFlag } = useFlag("autofix_enabled");
  const q = useFixJobs(enabled);
  const jobs = q.data ?? EMPTY_JOBS;
  const { stage } = Route.useSearch();
  const shown = useMemo(() => (stage ? jobs.filter((j) => fixStage(j) === stage) : jobs), [jobs, stage]);

  const columns: Column<FixJob>[] = [
    {
      key: "status",
      header: "Status",
      sortAccessor: (job) => job.status,
      filter: { label: "Status" },
      accessor: (job) => (
        <span className="inline-flex items-center gap-2">
          <StatusBadge status={job.status} />
          {job.status === "queued" ? (
            <span className="text-[11px] text-muted-foreground">
              {job.queued_behind ? `behind ${job.queued_behind.id.slice(0, 8)}` : "in queue"}
            </span>
          ) : job.status === "running" || job.status === "claimed" ? (
            <span className="text-[11px] text-status-info">live</span>
          ) : null}
        </span>
      ),
    },
    {
      key: "engine",
      header: "Engine",
      sortAccessor: (job) => [job.engine, job.model].filter(Boolean).join(" · "),
      filter: { label: "Engine" },
      accessor: (job) => (
        <span className="font-mono text-xs">
          {[job.engine, job.model].filter(Boolean).join(" · ") || "—"}
        </span>
      ),
    },
    {
      key: "branch",
      header: "Branch",
      sortAccessor: (job) => job.result_branch || job.target_branch || "",
      accessor: (job) => (
        <span className="font-mono text-xs">
          {job.result_branch || job.target_branch || "—"}
        </span>
      ),
    },
    {
      key: "round",
      header: "Round",
      align: "right",
      sortAccessor: (job) => job.current_loop || 0,
      accessor: (job) => (
        <span className="tabular-nums">
          {job.current_loop || 0}/{job.max_loops || 1}
        </span>
      ),
    },
    {
      key: "created",
      header: "Created",
      align: "right",
      sortAccessor: (job) => job.created_at,
      accessor: (job) => (
        <span className="text-muted-foreground">
          {new Date(job.created_at).toLocaleString()}
        </span>
      ),
    },
    {
      key: "actions",
      header: "Actions",
      hideHeader: true,
      sortable: false,
      align: "right",
      width: "3rem",
      accessor: (job) => (
        <RowActions label={`Actions for agent ${job.id.slice(0, 8)}`}>
          <DropdownMenuItem onSelect={() => void navigate({ to: "/agents/$agentId", params: { agentId: job.id } })}>Open agent</DropdownMenuItem>
          {job.scan_id && <DropdownMenuItem onSelect={() => void navigate({ to: "/scans/$scanId", params: { scanId: job.scan_id! } })}>Open scan</DropdownMenuItem>}
        </RowActions>
      ),
    },
  ];
  const running = jobs.filter((j) => j.status === "running" || j.status === "claimed").length;

  return (
    <PageShell>
      <PageHeader
        title="Agents"
        description="Live fixer jobs. Queue one from a scan or finding with Fix, then watch the first pass and follow-up rounds here."
        actions={
          enabled ? (
            <>
              <Button asChild variant="outline" size="sm">
                <Link to="/scans"><GaugeIcon aria-hidden /> Open scans</Link>
              </Button>
              <Button asChild variant="outline" size="sm">
                <Link to="/findings"><BugIcon aria-hidden /> Open findings</Link>
              </Button>
            </>
          ) : null
        }
      />

      {flagError ? (
        <ErrorState title="Unable to load fixing settings" onRetry={() => void retryFlag()} />
      ) : !flagLoading && !enabled ? (
        <FixerSetupBanner />
      ) : (
        <>
          <FixerSetupBanner />
          {jobs.length > 0 && (
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              <MetricCard title="Agents" value={jobs.length} subtitle="All fixer jobs" />
              <MetricCard title="Running" value={running} subtitle="Working on a branch now" />
              <MetricCard title="Queued" value={jobs.filter((j) => j.status === "queued").length} subtitle="Waiting for a runner" />
              <MetricCard title="Needs review" value={jobs.filter((j) => isFixPaused(j.status)).length} subtitle="Paused before push" />
            </div>
          )}
          {!q.isLoading && !q.isError && jobs.length === 0 ? (
            <Card><EmptyState
              icon={BotIcon}
              title="No agents yet"
              description="Open a scan or finding and click Fix. Wolf opens a branch, applies fixes, rescans, and can pause for review before push."
              cta={{ label: "Go to scans", to: "/scans" }}
            /></Card>
          ) : (
            <Card><CardContent className="space-y-3 p-4">{stage && <p className="text-sm">Showing jobs at stage <strong>{FIX_STAGES.find((f) => f.key === stage)?.label}</strong>. <Link to="/agents" className="font-medium text-primary underline-offset-4 hover:underline">Show all</Link></p>}<DataTable
              data={shown}
              columns={columns}
              keyExtractor={(job) => job.id}
              persistKey="agents"
              density="compact"
              loading={q.isLoading}
              isError={q.isError}
              onRetry={() => void q.refetch()}
              searchPlaceholder="Search agents..."
              emptyMessage="No agents match your filters"
              onRowClick={(job) =>
                navigate({ to: "/agents/$agentId", params: { agentId: job.id } })
              }
            /></CardContent></Card>
          )}
        </>
      )}
    </PageShell>
  );
}
