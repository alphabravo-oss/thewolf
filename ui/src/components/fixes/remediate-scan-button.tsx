// Run-agent dialog: how many sequential agent runs to queue.
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { RepeatIcon } from "lucide-react";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { enqueueFix, useAutofixEnabled, useRepoFixable } from "@/lib/fixes";

export function RemediateScanButton({
  scanId,
  repoId,
  frozen,
  frozenBranch,
  busy,
}: {
  scanId: string;
  repoId: string;
  frozen?: boolean;
  frozenBranch?: string;
  busy?: boolean;
}) {
  const qc = useQueryClient();
  const { enabled, isLoading } = useAutofixEnabled();
  const fixableQ = useRepoFixable(repoId, enabled);
  const [open, setOpen] = useState(false);
  const [runs, setRuns] = useState(1);

  const enqueue = useMutation({
    mutationFn: () =>
      enqueueFix({
        repo_id: repoId,
        scan_id: scanId,
        mode: "dry_run",
        max_loops: 2,
        planned_runs: runs,
        human_in_the_loop: false,
      }),
    onSuccess: (job) => {
      setOpen(false);
      toast.success(
        runs > 1
          ? `Agent queued — ${runs} runs, one after another`
          : "Agent queued",
      );
      qc.invalidateQueries({ queryKey: ["fix-jobs"] });
      qc.invalidateQueries({ queryKey: ["scan-lineage", scanId] });
      void job;
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Failed to queue remediation"),
  });

  if (isLoading || !enabled) return null;
  if (frozen) {
    return (
      <p className="text-xs text-muted-foreground max-w-xs text-right">
        This scan's branch was handed off
        {frozenBranch ? (
          <>
            {" "}
            (<code className="font-mono">{frozenBranch}</code>)
          </>
        ) : null}
        . Scan that branch again to keep fixing.
      </p>
    );
  }
  const writable = fixableQ.data?.writable ?? false;

  return (
    <>
      <button
        type="button"
        disabled={!writable || busy || enqueue.isPending}
        onClick={() => setOpen(true)}
        className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/40 disabled:opacity-50"
      >
        <RepeatIcon className="size-4" />
        Run agent
      </button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Run agent"
        description="Each run works leftover findings after the previous child scan finishes. Later runs should have less to fix."
        confirmLabel={runs === 1 ? "Start 1 run" : `Start ${runs} runs`}
        cancelLabel="Cancel"
        variant="default"
        pending={enqueue.isPending}
        confirmDisabled={!writable}
        onConfirm={() => enqueue.mutate()}
      >
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">How many runs?</p>
          <div className="flex gap-2">
            {[1, 2, 3].map((n) => (
              <button
                key={n}
                type="button"
                onClick={() => setRuns(n)}
                className={`h-9 min-w-12 px-3 rounded-md border text-sm tabular-nums ${
                  runs === n
                    ? "bg-primary text-primary-foreground border-primary"
                    : "border-border hover:bg-muted/40"
                }`}
              >
                {n}
              </button>
            ))}
          </div>
        </div>
      </ConfirmDialog>
    </>
  );
}
