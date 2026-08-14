import { createFileRoute, Link } from "@tanstack/react-router";
import { BotIcon, BugIcon, GaugeIcon } from "lucide-react";
import { useFlag } from "@/lib/flags";
import { useFixJobs } from "@/lib/fixes";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { FixerSetupBanner } from "@/components/fixes/fixer-setup-banner";
import { StatusBadge } from "@/components/fixes/status-badge";

export const Route = createFileRoute("/_authed/agents/")({
  component: AgentsPage,
});

function AgentsPage() {
  const { enabled, isLoading: flagLoading } = useFlag("autofix_enabled");
  const q = useFixJobs(enabled);

  return (
    <div className="page stack">
      <header className="page-header">
        <div>
          <h1 className="page-title">Agents</h1>
          <p className="page-subtitle">
            Live fixer jobs. Queue one from a scan or finding with Fix, then
            watch the first pass and follow-up rounds here.
          </p>
        </div>
        {enabled ? (
          <div className="page-actions">
            <Link to="/scans" className="btn btn-outline">
              <GaugeIcon /> Open scans
            </Link>
            <Link to="/findings" className="btn btn-outline">
              <BugIcon /> Open findings
            </Link>
          </div>
        ) : null}
      </header>

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
          {q.isLoading ? (
            <TableSkeleton rows={6} />
          ) : !q.data || q.data.length === 0 ? (
            <EmptyState
              icon={BotIcon}
              title="No agents yet"
              description="Open a scan or finding and click Fix. Wolf opens a branch, applies fixes, rescans, and can pause for review before push."
              cta={{ label: "Go to scans", to: "/scans" }}
            />
          ) : (
            <div className="glass-card overflow-hidden">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Status</th>
                    <th>Engine</th>
                    <th>Branch</th>
                    <th className="text-right">Round</th>
                    <th className="text-right">Created</th>
                  </tr>
                </thead>
                <tbody>
                  {q.data.map((job) => (
                    <tr key={job.id}>
                      <td>
                        <Link
                          to="/agents/$agentId"
                          params={{ agentId: job.id }}
                          className="inline-flex items-center gap-2 hover:underline"
                        >
                          <StatusBadge status={job.status} />
                          {job.status === "queued" ? (
                            <span className="text-[11px] text-muted-foreground">
                              {job.queued_behind
                                ? `behind ${job.queued_behind.id.slice(0, 8)}`
                                : "in queue"}
                            </span>
                          ) : job.status === "running" ||
                            job.status === "claimed" ? (
                            <span className="text-[11px] text-sky-300">
                              live
                            </span>
                          ) : null}
                        </Link>
                      </td>
                      <td className="mono text-xs">
                        {[job.engine, job.model].filter(Boolean).join(" · ") ||
                          "—"}
                      </td>
                      <td className="mono text-xs">
                        {job.result_branch || job.target_branch || "—"}
                      </td>
                      <td className="text-right tabular-nums">
                        {job.current_loop || 0}/{job.max_loops || 1}
                      </td>
                      <td className="text-right text-muted-foreground">
                        {new Date(job.created_at).toLocaleString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </div>
  );
}
