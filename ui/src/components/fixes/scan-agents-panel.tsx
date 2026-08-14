import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2Icon, UploadIcon } from "lucide-react";
import { toast } from "sonner";
import { StatusBadge } from "@/components/fixes/status-badge";
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
        <div className="px-5 py-3 text-sm border-b border-emerald-500/30 bg-emerald-500/10 text-emerald-100">
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
        <div className="px-5 py-3 text-sm border-b border-rose-500/30 bg-rose-500/10 text-rose-100">
          {pushFailureHint(lastFail.error)}
        </div>
      ) : null}

      {runs.length === 0 ? (
        <p className="px-5 py-4 text-sm text-muted-foreground">
          No agent runs yet. Start one to fix findings on a shared{" "}
          <span className="font-mono">wolf-fix/…</span> branch.
        </p>
      ) : (
        <table className="w-full text-sm">
          <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
            <tr>
              <th className="text-left px-4 py-2">Run</th>
              <th className="text-left px-4 py-2">Status</th>
              <th className="text-right px-4 py-2">In</th>
              <th className="text-right px-4 py-2">Out</th>
              <th className="text-right px-4 py-2">Δ</th>
              <th className="text-right px-4 py-2">Fixed</th>
              <th className="text-right px-4 py-2">Muted</th>
              <th className="text-left px-4 py-2">After</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <RunRow key={r.job_id} run={r} />
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function RunRow({ run }: { run: LineageRun }) {
  const waitingChild =
    isFixPaused(run.status as FixJobStatus) &&
    run.child_scan_id &&
    run.child_status &&
    run.child_status !== "completed";
  return (
    <tr className="border-t border-border/20">
      <td className="px-4 py-2">
        <Link
          to="/agents/$agentId"
          params={{ agentId: run.job_id }}
          className="font-mono text-xs hover:underline"
        >
          {run.run_index}/{run.planned_runs} · {run.job_id.slice(0, 8)}
        </Link>
      </td>
      <td className="px-4 py-2">
        <StatusBadge status={run.status as FixJobStatus} />
      </td>
      <td className="px-4 py-2 text-right tabular-nums">
        {run.input_findings.toLocaleString()}
      </td>
      <td className="px-4 py-2 text-right tabular-nums">
        {run.output_findings != null
          ? run.output_findings.toLocaleString()
          : "—"}
      </td>
      <td
        className={`px-4 py-2 text-right tabular-nums ${
          run.delta == null
            ? "text-muted-foreground"
            : run.delta < 0
              ? "text-emerald-300"
              : run.delta > 0
                ? "text-amber-300"
                : ""
        }`}
      >
        {fmtDelta(run.delta)}
      </td>
      <td className="px-4 py-2 text-right tabular-nums text-emerald-300">
        {run.kept || "—"}
      </td>
      <td className="px-4 py-2 text-right tabular-nums">
        {run.muted || "—"}
      </td>
      <td className="px-4 py-2 text-xs text-muted-foreground">
        {waitingChild ? (
          <span>rescanning…</span>
        ) : run.child_scan_id ? (
          <Link
            to="/scans/$scanId"
            params={{ scanId: run.child_scan_id }}
            className="font-mono hover:underline"
          >
            {run.child_scan_id.slice(0, 8)}
          </Link>
        ) : (
          "—"
        )}
      </td>
    </tr>
  );
}

function fmtDelta(d: number | null | undefined): string {
  if (d == null) return "—";
  if (d > 0) return `+${d.toLocaleString()}`;
  return d.toLocaleString();
}
