// Fixes list — autonomous fix engine (v1: dry-run, per-finding, verified,
// branch-only). The whole surface is gated on autofix_enabled: when off we show
// a disabled-state hint pointing at Settings → General instead of the job list.
import { createFileRoute, Link } from "@tanstack/react-router";
import { PowerOffIcon, WrenchIcon } from "lucide-react";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import {
  isFixTerminal,
  useAutofixEnabled,
  useFixJobs,
  type FixJobStatus,
} from "@/lib/fixes";

export const Route = createFileRoute("/_authed/fixes/")({
  component: FixesPage,
});

function FixesPage() {
  const { enabled, isLoading: flagLoading } = useAutofixEnabled();
  const q = useFixJobs(enabled);

  return (
    <div className="page stack">
      <h1 className="text-2xl font-semibold tracking-tight">Fixes</h1>

      {flagLoading ? (
        <TableSkeleton rows={6} />
      ) : !enabled ? (
        <AutofixDisabledHint />
      ) : q.isLoading ? (
        <TableSkeleton rows={8} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={WrenchIcon}
          title="No fixes yet"
          description="Queue a dry-run fix from a finding's detail page. v1 produces a branch + diff for review — it never pushes or opens a PR."
        />
      ) : (
        <div className="glass-card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="text-left px-4 py-2">Status</th>
                <th className="text-left px-4 py-2">Engine</th>
                <th className="text-left px-4 py-2">Branch</th>
                <th className="text-right px-4 py-2">Findings</th>
                <th className="text-right px-4 py-2">Created</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((f) => (
                <tr key={f.id} className="border-t border-border/30 table-row-hover">
                  <td className="px-4 py-2">
                    <Link
                      to="/fixes/$fixId"
                      params={{ fixId: f.id }}
                      className="inline-flex items-center gap-2 hover:underline"
                    >
                      <StatusBadge status={f.status} />
                    </Link>
                  </td>
                  <td className="px-4 py-2 font-mono text-xs">{f.engine}</td>
                  <td className="px-4 py-2 font-mono text-xs">
                    {f.result_branch || f.target_branch || "—"}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums">
                    {f.finding_ids?.length ?? 0}
                  </td>
                  <td className="px-4 py-2 text-right text-muted-foreground tabular-nums">
                    {new Date(f.created_at).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function AutofixDisabledHint() {
  return (
    <div className="glass-card p-6 flex items-start gap-4">
      <div className="size-10 grid place-items-center rounded-md bg-muted/30 text-muted-foreground shrink-0">
        <PowerOffIcon className="size-5" />
      </div>
      <div className="space-y-1">
        <h2 className="text-sm font-medium">Autonomous fixing is disabled</h2>
        <p className="text-sm text-muted-foreground max-w-prose">
          The fix engine is off by default. v1 is dry-run, per-finding, verified,
          and branch-only — it produces a fix branch + diff for review and never
          pushes or opens a PR. Enable the master switch in{" "}
          <Link
            to="/settings"
            search={{ tab: "general" }}
            className="text-primary hover:underline"
          >
            Settings → General
          </Link>{" "}
          to use it.
        </p>
      </div>
    </div>
  );
}

export function StatusBadge({ status }: { status: FixJobStatus }) {
  const cls = isFixTerminal(status)
    ? status === "succeeded"
      ? "text-emerald-300 bg-emerald-500/10 border-emerald-500/30"
      : status === "failed"
        ? "text-rose-300 bg-rose-500/10 border-rose-500/30"
        : "text-muted-foreground bg-muted/20 border-border/30"
    : "text-sky-300 bg-sky-500/10 border-sky-500/30";
  return (
    <span
      className={`text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 ${cls}`}
    >
      {status}
    </span>
  );
}
