import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon, ChevronDownIcon, Loader2Icon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { CardSkeleton } from "@/components/skeleton";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { AttemptsTable } from "@/components/fixes/attempts-table";
import { FixLogConsole } from "@/components/fixes/fix-log-console";
import { StatusBadge } from "@/components/fixes/status-badge";
import {
  acceptRemediation,
  cancelFix,
  githubBranchUrl,
  githubCommitUrl,
  isFixPaused,
  isFixTerminal,
  pushFailureHint,
  resumeFix,
  useAutofixEnabled,
  useFixJob,
  useRepoFixable,
  useRepoName,
  useScanLineage,
  useScanSummary,
} from "@/lib/fixes";

export function FixJobView({
  fixId,
  backTo,
}: {
  fixId: string;
  backTo: "/agents" | "/fixes";
}) {
  const qc = useQueryClient();
  const { enabled, isLoading: flagLoading } = useAutofixEnabled();
  const q = useFixJob(fixId, enabled);
  const repoQ = useRepoName(q.data?.repo_id);
  const scanQ = useScanSummary(q.data?.scan_id);
  const lineageQ = useScanLineage(q.data?.scan_id);
  const fixableQ = useRepoFixable(q.data?.repo_id, enabled);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const waitingPush = useRef(false);

  const cancel = useMutation({
    mutationFn: () => cancelFix(fixId),
    onSuccess: () => {
      setConfirmCancel(false);
      toast.success("Agent cancelled — branch work discarded");
      qc.invalidateQueries({ queryKey: ["fix-job", fixId] });
      qc.invalidateQueries({ queryKey: ["fix-jobs"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Cancel failed"),
  });
  const resume = useMutation({
    mutationFn: (action: "continue" | "push") => resumeFix(fixId, action),
    onSuccess: (job) => {
      if (job.resume_action === "push") {
        waitingPush.current = true;
        toast.message("Pushing branch to GitHub…");
      } else {
        toast.success("Continuing");
      }
      qc.invalidateQueries({ queryKey: ["fix-job", fixId] });
      qc.invalidateQueries({ queryKey: ["fix-jobs"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Resume failed"),
  });
  const accept = useMutation({
    mutationFn: () => acceptRemediation(q.data?.remediation_id || ""),
    onSuccess: () => {
      toast.success("Branch handed off — this scan is frozen");
      qc.invalidateQueries({ queryKey: ["fix-job", fixId] });
      qc.invalidateQueries({ queryKey: ["scan-lineage"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Accept failed"),
  });

  useEffect(() => {
    const job = q.data;
    if (!job || !waitingPush.current) return;
    if (job.pushed) {
      waitingPush.current = false;
      toast.success(
        job.push_sha
          ? `Pushed ${job.result_branch || "branch"} @ ${job.push_sha.slice(0, 7)}`
          : "Pushed to GitHub",
      );
      return;
    }
    if (job.status === "failed") {
      waitingPush.current = false;
      toast.error(pushFailureHint(job.error) || "Push failed");
    }
  }, [q.data]);

  if (flagLoading || (enabled && (q.isLoading || !q.data))) {
    return (
      <div className="page stack page--mid">
        <CardSkeleton />
        <CardSkeleton />
      </div>
    );
  }

  if (!enabled) {
    return (
      <div className="page stack page--mid">
        <BackLink to={backTo} />
        <div className="glass-card p-6 text-sm text-muted-foreground">
          Autonomous fixing is disabled. Enable it in{" "}
          <Link
            to="/settings"
            search={{ tab: "general" }}
            className="text-primary hover:underline"
          >
            Settings → General
          </Link>
          .
        </div>
      </div>
    );
  }

  const f = q.data!;
  const repoName =
    repoQ.data?.name ||
    scanQ.data?.repo?.name ||
    repoQ.data?.source_path ||
    scanQ.data?.source_path ||
    "";
  const scanBranch = scanQ.data?.branch;
  const scanFindings = scanQ.data?.finding_count;
  const terminal = isFixTerminal(f.status);
  const paused = isFixPaused(f.status);
  const summary: {
    rounds?: { round: number; kept: number; remaining: number }[];
    tools?: ToolRow[];
    open?: OpenNote[];
    report_markdown?: string;
  } = (() => {
    try {
      return f.summary ? JSON.parse(f.summary) : {};
    } catch {
      return {};
    }
  })();

  return (
    <div className="page stack page--mid">
      <div className="flex items-center gap-3">
        <BackLink to={backTo} />
        <div className="flex items-center gap-2 flex-1 min-w-0">
          <StatusBadge status={f.status} />
          <div className="min-w-0">
            <h1 className="text-xl font-semibold truncate">
              {repoName || f.result_branch || `Agent ${f.id.slice(0, 8)}`}
            </h1>
            <p className="text-xs text-muted-foreground truncate">
              {f.repo_id ? (
                <Link
                  to="/repos/$repoId"
                  params={{ repoId: f.repo_id }}
                  className="hover:underline"
                >
                  {repoName || "Repo"}
                </Link>
              ) : (
                "Repo"
              )}
              {f.scan_id ? (
                <>
                  {" · "}
                  <Link
                    to="/scans/$scanId"
                    params={{ scanId: f.scan_id }}
                    className="hover:underline"
                  >
                    Scan {f.scan_id.slice(0, 8)}
                    {scanBranch ? ` (${scanBranch})` : ""}
                    {typeof scanFindings === "number"
                      ? ` · ${scanFindings} findings`
                      : ""}
                  </Link>
                </>
              ) : null}
              {f.result_branch ? (
                <span className="font-mono"> · {f.result_branch}</span>
              ) : null}
              {lineageQ.data?.children?.length ? (
                <>
                  {" · "}
                  <Link
                    to="/scans/$scanId"
                    params={{
                      scanId:
                        lineageQ.data.children[lineageQ.data.children.length - 1]
                          .id,
                    }}
                    className="hover:underline"
                  >
                    Child scan{" "}
                    {lineageQ.data.children[
                      lineageQ.data.children.length - 1
                    ].id.slice(0, 8)}
                  </Link>
                </>
              ) : null}
            </p>
          </div>
        </div>
        {(paused || (f.status === "failed" && !!f.result_branch && !f.pushed)) && (
          <div className="flex items-center gap-2">
            {f.status === "awaiting_review" && (
              <button
                type="button"
                onClick={() => resume.mutate("continue")}
                disabled={resume.isPending}
                className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border/60 text-sm hover:bg-muted/40 disabled:opacity-50"
              >
                Continue
              </button>
            )}
            {fixableQ.data?.can_push ? (
              <button
                type="button"
                onClick={() => resume.mutate("push")}
                disabled={resume.isPending}
                className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
              >
                {resume.isPending ? (
                  <Loader2Icon className="size-4 animate-spin" />
                ) : null}
                {f.status === "failed" || f.status === "push_failed"
                  ? "Retry push"
                  : "Push branch for review"}
              </button>
            ) : (
              <button
                type="button"
                onClick={() => accept.mutate()}
                disabled={accept.isPending || !f.remediation_id}
                className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
              >
                Accept branch
              </button>
            )}
          </div>
        )}
        {terminal &&
          lineageQ.data?.remediation?.state === "open" &&
          !fixableQ.data?.can_push &&
          f.remediation_id && (
            <button
              type="button"
              onClick={() => accept.mutate()}
              disabled={accept.isPending}
              className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border/60 text-sm hover:bg-muted/40 disabled:opacity-50"
            >
              Accept branch
            </button>
          )}
        {!terminal && (
          <button
            type="button"
            onClick={() => setConfirmCancel(true)}
            disabled={cancel.isPending}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border/60 text-sm hover:bg-destructive/10 text-destructive disabled:opacity-50"
          >
            {cancel.isPending ? (
              <Loader2Icon className="size-4 animate-spin" />
            ) : (
              <XIcon className="size-4" />
            )}
            Cancel
          </button>
        )}
      </div>

      <ConfirmDialog
        open={confirmCancel}
        onOpenChange={setConfirmCancel}
        title="Cancel this agent?"
        description={
          f.result_branch ? (
            <>
              This discards the fix branch{" "}
              <code className="font-mono">{f.result_branch}</code> and any
              work on it. The job will be cancelled — it will not stay waiting
              to push.
            </>
          ) : (
            "Stop this agent. Any branch work it started will be discarded, and it will not stay waiting to push."
          )
        }
        confirmLabel="Discard branch and cancel"
        cancelLabel="Keep working"
        pending={cancel.isPending}
        onConfirm={() => cancel.mutate()}
      />

      {f.pause_reason && (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
          {f.pause_reason}
        </div>
      )}
      <PushResult
        pushed={f.pushed}
        sha={f.push_sha}
        branch={f.result_branch}
        status={f.status}
        error={f.error}
        sourcePath={repoQ.data?.source_path || scanQ.data?.source_path}
        pushing={
          waitingPush.current ||
          (f.resume_action === "push" &&
            (f.status === "queued" || f.status === "claimed" || f.status === "running"))
        }
      />

      <section className="glass-card p-5 grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
        <Meta label="Repo" value={repoName || f.repo_id.slice(0, 8)} />
        <Meta
          label="Scan"
          value={
            f.scan_id
              ? `${f.scan_id.slice(0, 8)}${scanBranch ? ` · ${scanBranch}` : ""}`
              : "—"
          }
          mono
        />
        <Meta label="Engine" value={f.engine} mono />
        <Meta label="Model" value={f.model || "default"} mono />
        <Meta label="Effort" value={f.effort || "default"} mono />
        <Meta label="Mode" value={f.mode} mono />
        <Meta label="Severity floor" value={f.severity_floor || "—"} />
        <Meta
          label="Round"
          value={`${f.current_loop || 0}/${f.max_loops || 1}`}
        />
        <Meta label="Findings" value={String(f.finding_ids?.length ?? 0)} />
        <Meta
          label="Branch"
          value={f.result_branch || f.target_branch || "—"}
          mono
        />
        {f.pushed && <Meta label="Pushed" value={f.push_sha || "yes"} mono />}
      </section>

      {summary.rounds && summary.rounds.length > 0 && (
        <section className="glass-card p-5 text-sm space-y-2">
          <h2 className="text-sm font-medium">Rescan rounds</h2>
          <ul className="space-y-1 text-muted-foreground">
            {summary.rounds.map((round) => (
              <li key={round.round}>
                Round {round.round}: kept {round.kept}, {round.remaining} still
                open
              </li>
            ))}
          </ul>
        </section>
      )}

      <FixLogConsole
        fixId={f.id}
        status={f.status}
        queuedBehind={f.queued_behind}
      />
      <AttemptsTable
        attempts={f.attempts ?? []}
        tools={f.tool_stats ?? summary.tools}
        fixId={f.id}
      />
      <MutedList attempts={f.attempts ?? []} />
      {summary.open && summary.open.length > 0 && (
        <WhyNotFixed notes={summary.open} />
      )}
    </div>
  );
}

function PushResult({
  pushed,
  sha,
  branch,
  status,
  error,
  sourcePath,
  pushing,
}: {
  pushed: boolean;
  sha?: string;
  branch?: string;
  status: string;
  error?: string;
  sourcePath?: string;
  pushing: boolean;
}) {
  const branchUrl = githubBranchUrl(sourcePath, branch);
  const commitUrl = githubCommitUrl(sourcePath, sha);
  if (pushed) {
    return (
      <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-4 py-3 text-sm">
        <div className="font-medium text-emerald-200">Pushed to GitHub</div>
        <p className="mt-1 text-emerald-100/80">
          {branch ? (
            <>
              Branch{" "}
              {branchUrl ? (
                <a
                  href={branchUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono underline"
                >
                  {branch}
                </a>
              ) : (
                <span className="font-mono">{branch}</span>
              )}
            </>
          ) : (
            "Fix branch is on the remote."
          )}
          {sha ? (
            <>
              {" "}
              @{" "}
              {commitUrl ? (
                <a
                  href={commitUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono underline"
                >
                  {sha.slice(0, 7)}
                </a>
              ) : (
                <span className="font-mono">{sha.slice(0, 7)}</span>
              )}
            </>
          ) : null}
        </p>
      </div>
    );
  }
  if (pushing) {
    return (
      <div className="rounded-md border border-sky-500/40 bg-sky-500/10 px-4 py-3 text-sm text-sky-100">
        Pushing {branch ? <span className="font-mono">{branch}</span> : "the fix branch"}{" "}
        to GitHub…
      </div>
    );
  }
  if (status === "push_failed" || (status === "failed" && error)) {
    return (
      <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm">
        <div className="font-medium text-amber-100">
          Fixes are on the branch — GitHub push did not land
        </div>
        <p className="mt-1 text-amber-100/90">{pushFailureHint(error)}</p>
      </div>
    );
  }
  return null;
}

function BackLink({ to }: { to: "/agents" | "/fixes" }) {
  return (
    <Link
      to={to}
      className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
      aria-label="Back"
    >
      <ArrowLeftIcon className="size-4" />
    </Link>
  );
}

function MutedList({ attempts }: { attempts: { outcome: string; tool_name?: string; rule_id?: string; title?: string; file_path?: string; diff_excerpt?: string }[] }) {
  const muted = attempts.filter((a) => a.outcome === "muted");
  const [open, setOpen] = useState(false);
  if (muted.length === 0) return null;
  const groups = new Map<string, typeof muted>();
  for (const a of muted) {
    const key = `${a.tool_name || "unknown"} · ${a.rule_id || a.title || "rule"}`;
    const list = groups.get(key) ?? [];
    list.push(a);
    groups.set(key, list);
  }
  return (
    <section className="glass-card overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center gap-2 px-5 py-3 text-left text-sm font-medium hover:bg-muted/20"
      >
        <ChevronDownIcon
          className={`size-3.5 text-muted-foreground transition-transform ${
            open ? "" : "-rotate-90"
          }`}
        />
        Muted this run
        <span className="text-xs font-normal text-muted-foreground">
          {muted.length} finding{muted.length === 1 ? "" : "s"} · {groups.size}{" "}
          rule{groups.size === 1 ? "" : "s"} — this job only, unless a repo
          suppression exists
        </span>
      </button>
      {open && (
        <ul className="divide-y divide-border/20 text-sm border-t border-border/30">
          {[...groups.entries()].map(([key, items]) => (
            <li key={key} className="px-5 py-2">
              <div className="flex items-baseline gap-2 min-w-0">
                <span className="font-mono text-xs truncate">{key}</span>
                <span className="text-xs text-muted-foreground shrink-0">
                  ×{items.length}
                </span>
              </div>
              <p className="mt-0.5 text-[11px] text-muted-foreground truncate">
                {(items[0].diff_excerpt || "").replace(/^MUTE:\s*/, "")}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function WhyNotFixed({ notes }: { notes: OpenNote[] }) {
  const [open, setOpen] = useState(false);
  return (
    <section className="glass-card overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center gap-2 px-5 py-3 text-left text-sm font-medium hover:bg-muted/20"
      >
        <ChevronDownIcon
          className={`size-3.5 text-muted-foreground transition-transform ${
            open ? "" : "-rotate-90"
          }`}
        />
        Why not fixed
        <span className="text-xs font-normal text-muted-foreground">
          {notes.length} leftover{notes.length === 1 ? "" : "s"}
        </span>
      </button>
      {open && (
        <ul className="divide-y divide-border/20 text-sm border-t border-border/30">
          {notes.map((n) => (
            <li key={n.id} className="px-5 py-2">
              <div className="flex items-baseline gap-2 min-w-0">
                <span className="text-[10px] uppercase tracking-wide text-muted-foreground shrink-0">
                  {n.severity || "—"}
                </span>
                <span className="font-mono text-xs shrink-0">{n.tool}</span>
                <span className="truncate text-foreground/80">
                  {n.file || "(no file)"}
                  {n.title ? ` — ${n.title}` : ""}
                </span>
              </div>
              <p className="mt-0.5 text-[11px] text-muted-foreground">{n.reason}</p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

type ToolRow = {
  tool: string;
  total: number;
  kept: number;
  open: number;
  unfixable: number;
  muted?: number;
  deferred: number;
};

type OpenNote = {
  id: string;
  tool: string;
  severity?: string;
  file?: string;
  title?: string;
  reason: string;
};

function Meta({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className={mono ? "font-mono text-xs" : ""}>{value}</span>
    </div>
  );
}
