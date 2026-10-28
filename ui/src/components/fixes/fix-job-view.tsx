import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2Icon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorState } from "@/components/ui/empty-state";
import { MetricCard } from "@/components/ui/metric-card";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Section } from "@/components/ui/section";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { FixerSetupBanner } from "@/components/fixes/fixer-setup-banner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { AttemptsTable } from "@/components/fixes/attempts-table";
import { FixLogConsole } from "@/components/fixes/fix-log-console";
import { StatusBadge } from "@/components/fixes/status-badge";
import {
  acceptRemediation,
  cancelFix,
  isFixCleanupPending,
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
  const q = useFixJob(fixId, true);
  const repoQ = useRepoName(q.data?.repo_id);
  const scanQ = useScanSummary(q.data?.scan_id);
  const lineageQ = useScanLineage(q.data?.scan_id);
  const fixableQ = useRepoFixable(q.data?.repo_id, enabled);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const waitingPush = useRef(false);

  const cancel = useMutation({
    mutationFn: () => cancelFix(fixId),
    onSuccess: (job) => {
      toast.success(isFixCleanupPending(job) ? "Agent cancelled; workspace cleanup pending" : "Branch work discarded");
    },
    onSettled: () => {
      setConfirmCancel(false);
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
      toast.success(q.data?.pushed && q.data?.status === "succeeded"
        ? "Workspace cleaned up"
        : "Branch handed off — this scan is frozen");
      qc.invalidateQueries({ queryKey: ["fix-job", fixId] });
      qc.invalidateQueries({ queryKey: ["scan-lineage"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Accept failed"),
  });

  useEffect(() => {
    const job = q.data;
    if (!job || !waitingPush.current) return;
    if (job.status === "failed" || job.status === "push_failed") {
      waitingPush.current = false;
      toast.error(job.status === "push_failed" ? pushFailureHint(job.error) : job.error || "Agent failed");
      return;
    }
    if (job.pushed && job.status === "succeeded") {
      waitingPush.current = false;
      toast.success("Branch published for review");
    }
  }, [q.data]);

  if (flagLoading || q.isLoading) {
    return (
      <PageShell>
        <Skeleton className="h-16" />
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-24" />)}</div>
        <Skeleton className="h-64" />
      </PageShell>
    );
  }

  if (q.isError || !q.data) return <ErrorState title="Unable to load fix job" onRetry={() => void q.refetch()} />;

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
  const cleanupPending = isFixCleanupPending(f);
  const paused = isFixPaused(f.status);
  const active = f.status === "queued" || f.status === "claimed" || f.status === "running";
  const summary: {
    rounds?: { round: number; kept: number; remaining: number }[];
    tools?: ToolRow[];
    open?: OpenNote[];
    report_markdown?: string;
    pr_url?: string;
    publication_error?: string;
  } = (() => {
    try {
      return f.summary ? JSON.parse(f.summary) : {};
    } catch {
      return {};
    }
  })();
  const attempts = f.attempts ?? [];
  const fixed = attempts.filter((a) => a.outcome === "kept").length;
  const title = repoName || f.result_branch || `Agent ${f.id.slice(0, 8)}`;
  const lastChild = lineageQ.data?.children?.[lineageQ.data.children.length - 1];

  return (
    <PageShell>
      {!enabled && <FixerSetupBanner />}
      <PageHeader
        breadcrumbs={[{ label: backTo === "/fixes" ? "Fixes" : "Agents", link: { to: backTo } }, { label: title }]}
        title={title}
        status={<StatusBadge status={f.status} />}
        description={
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
            {f.repo_id ? <Link to="/repos/$repoId" params={{ repoId: f.repo_id }} className="hover:underline">{repoName || "Repo"}</Link> : "Repo"}
            {f.scan_id ? (
              <>
                <span aria-hidden>·</span>
                <Link to="/scans/$scanId" params={{ scanId: f.scan_id }} className="hover:underline">
                  Scan {f.scan_id.slice(0, 8)}{scanBranch ? ` (${scanBranch})` : ""}{typeof scanFindings === "number" ? ` · ${scanFindings} findings` : ""}
                </Link>
              </>
            ) : null}
            {f.result_branch ? <><span aria-hidden>·</span><span className="font-mono">{f.result_branch}</span></> : null}
            {lastChild ? <><span aria-hidden>·</span><Link to="/scans/$scanId" params={{ scanId: lastChild.id }} className="hover:underline">Child scan {lastChild.id.slice(0, 8)}</Link></> : null}
          </span>
        }
        actions={
          <>
            {paused && f.status === "awaiting_review" && (
              <Button size="sm" variant="outline" onClick={() => resume.mutate("continue")} disabled={!enabled || resume.isPending}>
                Continue
              </Button>
            )}
            {paused && (fixableQ.data?.can_push ? (
              <Button size="sm" onClick={() => resume.mutate("push")} disabled={!enabled || resume.isPending}>
                {resume.isPending ? <Loader2Icon className="animate-spin" /> : null}
                {f.status === "push_failed" ? (f.pushed ? "Retry PR creation" : "Retry push") : "Push branch and open PR"}
              </Button>
            ) : (
              <Button size="sm" onClick={() => accept.mutate()} disabled={accept.isPending || !f.remediation_id}>
                Accept branch
              </Button>
            ))}
            {terminal && lineageQ.data?.remediation?.state === "open" && !fixableQ.data?.can_push && f.remediation_id && (
              <Button size="sm" variant="outline" onClick={() => accept.mutate()} disabled={accept.isPending}>
                Accept branch
              </Button>
            )}
            {f.status === "succeeded" && f.pushed && f.workspace_path && f.remediation_id && lineageQ.data?.remediation?.state === "frozen" && (
              <Button size="sm" variant="outline" onClick={() => accept.mutate()} disabled={accept.isPending}>
                Retry cleanup
              </Button>
            )}
            {(!terminal || (cleanupPending && !f.claimed_by)) && (
              <Button size="sm" variant="outline" className="text-destructive hover:text-destructive" onClick={() => cleanupPending ? cancel.mutate() : setConfirmCancel(true)} disabled={cancel.isPending}>
                {cancel.isPending ? <Loader2Icon className="animate-spin" /> : <XIcon aria-hidden />}
                {cleanupPending ? "Retry cleanup" : "Cancel"}
              </Button>
            )}
          </>
        }
      />

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

      {cleanupPending && (
        <div role="status" className="rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-sm text-status-warning">
          {f.claimed_by ? "Agent cancelled. Waiting for the worker to stop and clean up." : "Agent cancelled. Workspace cleanup is pending; new work will wait until cleanup succeeds."}
        </div>
      )}
      {f.pause_reason && !cleanupPending && (
        <div className="rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-sm text-status-warning">
          {f.pause_reason}
        </div>
      )}
      {(f.status === "failed" || cleanupPending) && f.error && (
        <div role="alert" className="rounded-md border border-status-error/40 bg-status-error/10 px-4 py-3 text-sm text-status-error">
          {f.error}
        </div>
      )}
      {summary.pr_url && /^https:\/\//.test(summary.pr_url) && (
        <a href={summary.pr_url} target="_blank" rel="noreferrer" className="text-sm underline">View pull request</a>
      )}
      <PushResult
        pushed={f.pushed}
        sha={f.push_sha}
        branch={f.result_branch}
        status={f.status}
        error={f.error}
        errorCode={summary.publication_error}
        sourcePath={repoQ.data?.source_path || scanQ.data?.source_path}
        pushing={
          waitingPush.current ||
          (f.resume_action === "push" &&
            (f.status === "queued" || f.status === "claimed" || f.status === "running"))
        }
      />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <MetricCard title="Findings" value={f.finding_ids?.length ?? 0} subtitle={f.severity_floor ? `Severity floor: ${f.severity_floor}` : "No severity floor"} />
        <MetricCard title="Fixed" value={attempts.length ? `${fixed} / ${attempts.length}` : "—"} subtitle={attempts.length ? "Decisions kept by this agent" : "No decisions recorded yet"} />
        <MetricCard title="Round" value={`${f.current_loop || 0} / ${f.max_loops || 1}`} subtitle={f.mode ? `Mode: ${f.mode}` : "Rescan rounds"} />
        <MetricCard title="Engine" value={f.engine} subtitle={`${f.model || "default model"}${f.effort ? ` · ${f.effort}` : ""}`} />
      </div>

      <Tabs defaultValue="overview">
        <TabsList aria-label="Agent views" className="flex h-auto flex-wrap justify-start">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="decisions">Decisions{attempts.length ? ` (${attempts.length})` : ""}</TabsTrigger>
        </TabsList>
        {/* The log stays mounted across tab switches so the SSE stream is not replayed. */}
        <TabsContent value="overview" forceMount className="mt-5 space-y-5 data-[state=inactive]:hidden">
          <Card>
            <CardHeader><CardTitle className="text-base">Run details</CardTitle></CardHeader>
            <CardContent className="grid grid-cols-1 gap-x-8 gap-y-2 pt-4 text-sm md:grid-cols-2">
              <Meta label="Repo" value={repoName || f.repo_id.slice(0, 8)} />
              <Meta label="Scan" value={f.scan_id ? `${f.scan_id.slice(0, 8)}${scanBranch ? ` · ${scanBranch}` : ""}` : "—"} mono />
              <Meta label="Engine" value={f.engine} mono />
              <Meta label="Model" value={f.model || "default"} mono />
              <Meta label="Effort" value={f.effort || "default"} mono />
              <Meta label="Mode" value={f.mode} mono />
              <Meta label="Severity floor" value={f.severity_floor || "—"} />
              <Meta label="Round" value={`${f.current_loop || 0}/${f.max_loops || 1}`} />
              <Meta label="Findings" value={String(f.finding_ids?.length ?? 0)} />
              <Meta label="Branch" value={f.result_branch || f.target_branch || "—"} mono />
              {f.pushed && <Meta label="Pushed" value={f.push_sha || "yes"} mono />}
            </CardContent>
          </Card>
          {summary.rounds && summary.rounds.length > 0 && (
            <Card>
              <CardHeader><CardTitle className="text-base">Rescan rounds</CardTitle></CardHeader>
              <CardContent className="pt-4">
                <ul className="space-y-1 text-sm text-muted-foreground">
                  {summary.rounds.map((round) => (
                    <li key={round.round}>Round {round.round}: kept {round.kept}, {round.remaining} still open</li>
                  ))}
                </ul>
              </CardContent>
            </Card>
          )}
          {summary.open && summary.open.length > 0 && <WhyNotFixed notes={summary.open} />}
          <Section title="Agent log" summary={active ? "Live" : "Finished"} defaultOpen={active} className="bg-card">
            <FixLogConsole fixId={f.id} status={f.status} queuedBehind={f.queued_behind} />
          </Section>
        </TabsContent>
        <TabsContent value="decisions" className="mt-5 space-y-5">
          <AttemptsTable attempts={attempts} tools={f.tool_stats ?? summary.tools} fixId={f.id} />
          <MutedList attempts={attempts} />
        </TabsContent>
      </Tabs>
    </PageShell>
  );
}

function PushResult({
  pushed,
  sha,
  branch,
  status,
  error,
  errorCode,
  sourcePath,
  pushing,
}: {
  pushed: boolean;
  sha?: string;
  branch?: string;
  status: string;
  error?: string;
  errorCode?: string;
  sourcePath?: string;
  pushing: boolean;
}) {
  const branchUrl = githubBranchUrl(sourcePath, branch);
  const commitUrl = githubCommitUrl(sourcePath, sha);
  if (status === "push_failed") {
    return (
      <div className="rounded-md border border-status-warning/40 bg-status-warning/10 px-4 py-3 text-sm">
        <div className="font-medium text-status-warning">
          {errorCode === "publication_not_ready" ? "Publication not ready; branch retained" : pushed ? "Branch pushed; pull request creation failed" : "GitHub push failed; branch retained"}
        </div>
        <p className="mt-1 text-status-warning/90">{pushFailureHint(error)}</p>
      </div>
    );
  }
  if (pushed) {
    return (
      <div className="rounded-md border border-status-success/40 bg-status-success/10 px-4 py-3 text-sm">
        <div className="font-medium text-status-success">Pushed to GitHub</div>
        <p className="mt-1 text-status-success/80">
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
      <div className="rounded-md border border-status-info/40 bg-status-info/10 px-4 py-3 text-sm text-status-info">
        Pushing {branch ? <span className="font-mono">{branch}</span> : "the fix branch"}{" "}
        to GitHub…
      </div>
    );
  }
  return null;
}

function MutedList({ attempts }: { attempts: { outcome: string; tool_name?: string; rule_id?: string; title?: string; file_path?: string; diff_excerpt?: string }[] }) {
  const muted = attempts.filter((a) => a.outcome === "muted");
  if (muted.length === 0) return null;
  const groups = new Map<string, typeof muted>();
  for (const a of muted) {
    const key = `${a.tool_name || "unknown"} · ${a.rule_id || a.title || "rule"}`;
    const list = groups.get(key) ?? [];
    list.push(a);
    groups.set(key, list);
  }
  return (
    <Section
      title="Muted this run"
      className="bg-card"
      summary={<>{muted.length} finding{muted.length === 1 ? "" : "s"} · {groups.size} rule{groups.size === 1 ? "" : "s"} — this job only, unless a repo suppression exists</>}
    >
      <ul className="divide-y divide-border text-sm">
        {[...groups.entries()].map(([key, items]) => (
          <li key={key} className="py-2">
            <div className="flex items-baseline gap-2 min-w-0">
              <span className="font-mono text-xs truncate">{key}</span>
              <span className="text-xs text-muted-foreground shrink-0">×{items.length}</span>
            </div>
            <p className="mt-0.5 text-[11px] text-muted-foreground truncate">
              {(items[0].diff_excerpt || "").replace(/^MUTE:\s*/, "")}
            </p>
          </li>
        ))}
      </ul>
    </Section>
  );
}

function WhyNotFixed({ notes }: { notes: OpenNote[] }) {
  return (
    <Section title="Why not fixed" className="bg-card" summary={`${notes.length} leftover${notes.length === 1 ? "" : "s"}`}>
      <ul className="divide-y divide-border text-sm">
        {notes.map((n) => (
          <li key={n.id} className="py-2">
            <div className="flex items-baseline gap-2 min-w-0">
              <span className="text-[10px] uppercase tracking-wide text-muted-foreground shrink-0">{n.severity || "—"}</span>
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
    </Section>
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
