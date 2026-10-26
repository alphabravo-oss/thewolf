// FixFindingButton — enqueue a per-finding fix job. Offers propose-only
// (branch + diff) or push-the-fix-branch, plus an optional human pause
// between rescan rounds.
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FixerSetupBanner } from "@/components/fixes/fixer-setup-banner";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Loader2Icon, WrenchIcon } from "lucide-react";
import { toast } from "sonner";
import {
  useFixSubmission,
  useAutofixEnabled,
  useFixEngines,
  useRepoFixable,
} from "@/lib/fixes";

export function FixFindingButton({
  findingId,
  repoId,
  scanId,
}: {
  findingId: string;
  repoId: string;
  scanId?: string;
}) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { enabled: autofixEnabled, isLoading: flagLoading } = useAutofixEnabled();
  const fixableQ = useRepoFixable(repoId, autofixEnabled);
  const [confirmPush, setConfirmPush] = useState(false);
  const [hitl, setHitl] = useState(false);
  const [engine, setEngine] = useState("");
  const [model, setModel] = useState("");
  const [effort, setEffort] = useState("");
  const engines = useFixEngines(autofixEnabled);

  const submitFix = useFixSubmission();
  const enqueue = useMutation({
    mutationFn: (mode: "dry_run" | "push") =>
      submitFix({
        repo_id: repoId,
        scan_id: scanId,
        finding_ids: [findingId],
        mode,
        max_loops: 2,
        human_in_the_loop: hitl,
        engine: engine || undefined,
        model: model || undefined,
        effort: effort || undefined,
      }),
    onSuccess: (job) => {
      toast.success(job.mode === "push" ? "Fix + push queued" : "Fix queued");
      qc.invalidateQueries({ queryKey: ["fix-jobs"] });
      navigate({ to: "/agents/$agentId", params: { agentId: job.id } });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Failed to queue fix"),
  });

  if (flagLoading) return null;
  if (!autofixEnabled) {
    return <FixerSetupBanner />;
  }

  const fixable = fixableQ.data;
  const probing = fixableQ.isLoading;
  const writable = fixable?.writable ?? false;
  const canPush = fixable?.can_push ?? false;

  const catalog = engines.data?.catalog ?? [];
  const defaults = engines.data?.defaults ?? {};
  const harness =
    catalog.find((e) => e.name === (engine || defaults.fixer_engine || "auto")) ??
    catalog.find((e) => e.name === "claude-code") ??
    catalog[0];

  return (
    <div className="flex flex-col items-end gap-2">
      {harness && (
        <details className="text-xs">
          <summary className="cursor-pointer text-muted-foreground">Advanced fix options</summary>
          <div className="flex flex-wrap justify-end gap-2 mt-2">
          <select
            aria-label="Fix engine"
            value={engine}
            onChange={(e) => {
              setEngine(e.target.value);
              setModel("");
            }}
            className="h-8 px-2 rounded-md bg-muted/40 border border-border text-xs max-w-[220px]"
          >
            <option value="">Harness: default</option>
            {catalog.map((item) => (
              <option key={item.name} value={item.name}>
                {item.label}
              </option>
            ))}
          </select>
          <select
            aria-label="Fix model"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            className="h-8 px-2 rounded-md bg-muted/40 border border-border text-xs max-w-[220px]"
          >
            <option value="">Model: default</option>
            {harness.models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.label}
                {m.context_k ? ` (${m.context_k}k)` : ""}
              </option>
            ))}
          </select>
          <select
            aria-label="Reasoning effort"
            value={effort}
            onChange={(e) => setEffort(e.target.value)}
            className="h-8 px-2 rounded-md bg-muted/40 border border-border text-xs"
          >
            <option value="">Effort: default</option>
            {(harness.efforts ?? []).map((e) => (
              <option key={e.id} value={e.id}>
                {e.label}
              </option>
            ))}
          </select>
          </div>
        </details>
      )}
      <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <input
          type="checkbox"
          checked={hitl}
          onChange={(e) => setHitl(e.target.checked)}
        />
        Pause between rounds
      </label>
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => enqueue.mutate("dry_run")}
          disabled={probing || !writable || enqueue.isPending}
          title={
            fixable && !fixable.writable
              ? fixable.reason
              : "Create a verified fix branch (no push)"
          }
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/40 disabled:opacity-50"
        >
          {enqueue.isPending ? (
            <Loader2Icon className="size-4 animate-spin" />
          ) : (
            <WrenchIcon className="size-4" />
          )}
          Fix on branch
        </button>
        <button
          type="button"
          onClick={() => setConfirmPush(true)}
          disabled={probing || !writable || !canPush || enqueue.isPending}
          title={
            !canPush
              ? fixable?.reason || "A write-capable GitHub token is required to push"
              : "Fix, rescan, then push the branch for review"
          }
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
        >
          Fix and push
        </button>
      </div>
      <ConfirmDialog open={confirmPush} onOpenChange={setConfirmPush}
        title="Fix and push a branch?" variant="default" confirmLabel="Fix and push"
        description="Wolf will create a fix branch, rescan the changes, and push the branch to your repository, and open a pull request for review."
        pending={enqueue.isPending} onConfirm={() => enqueue.mutate("push")} />
      {!probing && fixable && !fixable.writable && (
        <span className="text-[11px] text-status-warning max-w-xs text-right">
          {fixable.reason}
        </span>
      )}
    </div>
  );
}
