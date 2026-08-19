import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { BotIcon, BugIcon, GaugeIcon } from "lucide-react";
import { useFlag } from "@/lib/flags";
import { useFixJobs, type FixJob } from "@/lib/fixes";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { FixerSetupBanner } from "@/components/fixes/fixer-setup-banner";
import { StatusBadge } from "@/components/fixes/status-badge";

export const Route = createFileRoute("/_authed/agents/")({
  component: AgentsPage,
});

// Stable empty array — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_JOBS: FixJob[] = [];

function AgentsPage() {
  const navigate = useNavigate();
  const { enabled, isLoading: flagLoading } = useFlag("autofix_enabled");
  const q = useFixJobs(enabled);
  const jobs = q.data ?? EMPTY_JOBS;

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
  ];

  return (
    <PageShell>
      <PageHeader
        title="Agents"
        description="Live fixer jobs. Queue one from a scan or finding with Fix, then watch the first pass and follow-up rounds here."
        actions={
          enabled ? (
            <>
              <Link to="/scans" className="btn btn-outline">
                <GaugeIcon /> Open scans
              </Link>
              <Link to="/findings" className="btn btn-outline">
                <BugIcon /> Open findings
              </Link>
            </>
          ) : null
        }
      />

      {!flagLoading && !enabled ? (
        <EmptyState
          icon={BotIcon}
          title="Autonomous fixing is off"
          description="Turn on Autonomous fixing in Settings → General, then queue a fix from a scan or finding."
          cta={{ label: "Open Settings", to: "/settings" }}
        />
      ) : (
        <>
          <FixerSetupBanner />
          {!q.isLoading && jobs.length === 0 ? (
            <EmptyState
              icon={BotIcon}
              title="No agents yet"
              description="Open a scan or finding and click Fix. Wolf opens a branch, applies fixes, rescans, and can pause for review before push."
              cta={{ label: "Go to scans", to: "/scans" }}
            />
          ) : (
            <DataTable
              data={jobs}
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
            />
          )}
        </>
      )}
    </PageShell>
  );
}
