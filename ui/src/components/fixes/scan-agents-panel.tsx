import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2Icon, UploadIcon } from "lucide-react";
import { toast } from "sonner";
import { StatusBadge } from "@/components/fixes/status-badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { RemediateScanButton } from "@/components/fixes/remediate-scan-button";
import {
  githubBranchUrl,
  isFixPaused,
  pushFailureHint,
  resumeFix,
  useAutofixEnabled,
  useRepoFixable,
  useScanLineage,
  type FixJobStatus,
  type LineageRun,
} from "@/lib/fixes";

export function ScanAgentsPanel({
  scanId,
  repoId,
  originFindings,
  github,
  sourcePath,
}: {
  scanId: string;
  repoId?: string;
  originFindings?: number;
  github?: boolean;
  sourcePath?: string;
}) {
  const qc = useQueryClient();
  const lineageQ = useScanLineage(scanId);
  const { enabled } = useAutofixEnabled();
  const fixableQ = useRepoFixable(repoId, enabled && !!repoId);
  const lin = lineageQ.data;
  const runs = lin?.runs ?? [];
  const rem = lin?.remediation;
  const busy = runs.some((r) =>
    ["queued", "claimed", "running"].includes(r.status),
  );
  const pushable = [...runs]
    .reverse()
    .find(
      (r) =>
        !r.pushed &&
        (r.status === "awaiting_push" ||
          r.status === "push_failed" ||
          (r.status === "failed" && !!r.result_branch)),
    );
  const canPush = Boolean(
    github && rem?.state === "open" && pushable && fixableQ.data?.can_push,
  );
  const published = [...runs].reverse().find((r) => r.pushed);
  const lastFail = [...runs]
    .reverse()
    .find((r) => (r.status === "failed" || r.status === "push_failed") && r.error);

  const push = useMutation({
    mutationFn: () => resumeFix(pushable!.job_id, "push"),
    onSuccess: () => {
      toast.message("Pushing branch to GitHub…");
      qc.invalidateQueries({ queryKey: ["scan-lineage", scanId] });
      qc.invalidateQueries({ queryKey: ["fix-jobs"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Push failed"),
  });

  const show =
    (lin &&
      (runs.length > 0 ||
        (lin.children?.length ?? 0) > 0 ||
        rem ||
        repoId)) ||
    repoId;
  if (!show) return null;

  const baseline =
    lin?.origin?.finding_count ?? originFindings ?? 0;

  return (
    <section className="glass-card overflow-hidden">
      <div className="px-5 py-3 flex items-center justify-between gap-3 border-b border-border/30">
        <div className="min-w-0">
          <h2 className="text-sm font-medium">Agents</h2>
          {rem ? (
            <p className="text-[11px] text-muted-foreground truncate">
              <span className="font-mono">{rem.branch}</span>
              {" · "}
              {rem.state}
            </p>
          ) : (
            <p className="text-[11px] text-muted-foreground">
              Baseline {baseline.toLocaleString()} findings
            </p>
          )}
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {canPush && pushable ? (
            <button
              type="button"
              disabled={push.isPending || busy}
              onClick={() => push.mutate()}
              className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
            >
              {push.isPending ? (
                <Loader2Icon className="size-4 animate-spin" />
              ) : (
                <UploadIcon className="size-4" />
              )}
              {pushable?.status === "failed" || pushable?.status === "push_failed"
                ? "Retry push"
                : "Push to GitHub"}
            </button>
          ) : null}
          {repoId ? (
            <RemediateScanButton
              scanId={scanId}
              repoId={repoId}
              frozen={rem?.state === "frozen"}
              frozenBranch={rem?.branch}
              busy={busy}
            />
          ) : null}
        </div>
      </div>

      {published ? (
        <div className="px-5 py-3 text-sm border-b border-status-success/30 bg-status-success/10 text-status-success">
          Pushed{" "}
          {githubBranchUrl(sourcePath, published.result_branch) ? (
            <a
              href={githubBranchUrl(sourcePath, published.result_branch)!}
              target="_blank"
              rel="noreferrer"
              className="font-mono underline"
            >
              {published.result_branch}
            </a>
          ) : (
            <span className="font-mono">{published.result_branch}</span>
          )}
          {published.push_sha ? (
            <span className="font-mono"> @ {published.push_sha.slice(0, 7)}</span>
          ) : null}
        </div>
      ) : lastFail?.error ? (
        <div className="px-5 py-3 text-sm border-b border-status-error/30 bg-status-error/10 text-status-error">
          {pushFailureHint(lastFail.error)}
        </div>
      ) : null}

      {runs.length === 0 ? (
        <p className="px-5 py-4 text-sm text-muted-foreground">
          No agent runs yet. Start one to fix findings on a shared{" "}
          <span className="font-mono">wolf-fix/…</span> branch.
        </p>
      ) : (
        <DataTable
          data={runs}
          columns={runColumns}
          keyExtractor={(r) => r.job_id}
          density="compact"
          searchable={false}
          pageSize={25}
          emptyMessage="No agent runs yet"
        />
      )}
    </section>
  );
}

// Columns for the run-lineage table. Declared at module scope so the array
// identity is stable across renders.
const runColumns: Column<LineageRun>[] = [
  {
    key: "run",
    header: "Run",
    sortAccessor: (run) => run.run_index,
    accessor: (run) => (
      <Link
        to="/agents/$agentId"
        params={{ agentId: run.job_id }}
        className="font-mono text-xs hover:underline"
        onClick={(e) => e.stopPropagation()}
      >
        {run.run_index}/{run.planned_runs} · {run.job_id.slice(0, 8)}
      </Link>
    ),
  },
  {
    key: "status",
    header: "Status",
    sortAccessor: (run) => run.status,
    accessor: (run) => <StatusBadge status={run.status as FixJobStatus} />,
  },
  {
    key: "in",
    header: "In",
    align: "right",
    sortAccessor: (run) => run.input_findings,
    accessor: (run) => (
      <span className="tabular-nums">{run.input_findings.toLocaleString()}</span>
    ),
  },
  {
    key: "out",
    header: "Out",
    align: "right",
    sortAccessor: (run) => run.output_findings ?? -1,
    accessor: (run) => (
      <span className="tabular-nums">
        {run.output_findings != null ? run.output_findings.toLocaleString() : "—"}
      </span>
    ),
  },
  {
    key: "delta",
    header: "Δ",
    align: "right",
    sortAccessor: (run) => run.delta ?? 0,
    accessor: (run) => (
      <span
        className={
          "tabular-nums " +
          (run.delta == null
            ? "text-muted-foreground"
            : run.delta < 0
              ? "text-status-success"
              : run.delta > 0
                ? "text-status-warning"
                : "")
        }
      >
        {fmtDelta(run.delta)}
      </span>
    ),
  },
  {
    key: "fixed",
    header: "Fixed",
    align: "right",
    sortAccessor: (run) => run.kept ?? 0,
    accessor: (run) => (
      <span className="tabular-nums text-status-success">{run.kept || "—"}</span>
    ),
  },
  {
    key: "muted",
    header: "Muted",
    align: "right",
    sortAccessor: (run) => run.muted ?? 0,
    accessor: (run) => <span className="tabular-nums">{run.muted || "—"}</span>,
  },
  {
    key: "after",
    header: "After",
    sortable: false,
    accessor: (run) => {
      const waitingChild =
        isFixPaused(run.status as FixJobStatus) &&
        run.child_scan_id &&
        run.child_status &&
        run.child_status !== "completed";
      if (waitingChild) return <span className="text-xs text-muted-foreground">rescanning…</span>;
      if (!run.child_scan_id) return <span className="text-xs text-muted-foreground">—</span>;
      return (
        <Link
          to="/scans/$scanId"
          params={{ scanId: run.child_scan_id }}
          className="font-mono text-xs hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          {run.child_scan_id.slice(0, 8)}
        </Link>
      );
    },
  },
];

function fmtDelta(d: number | null | undefined): string {
  if (d == null) return "—";
  if (d > 0) return `+${d.toLocaleString()}`;
  return d.toLocaleString();
}
