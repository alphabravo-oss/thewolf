// FixFindingButton — the "Fix this finding (dry-run)" action on a finding's
// detail page. It enqueues a per-finding, dry-run fix job and routes to the new
// job's detail page.
//
// Gating, in order:
//   1. autofix_enabled off  → the whole control is hidden (the surface is dark).
//   2. repo not writable    → the button is disabled with the writability reason
//                             as its tooltip (from GET /repos/{id}/fixable).
//   3. otherwise            → enqueue POST /fixes { finding_ids:[id], mode:dry_run }.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Loader2Icon, WrenchIcon } from "lucide-react";
import { toast } from "sonner";
import {
  enqueueFix,
  useAutofixEnabled,
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

  const enqueue = useMutation({
    mutationFn: () =>
      enqueueFix({
        repo_id: repoId,
        scan_id: scanId,
        finding_ids: [findingId],
        mode: "dry_run",
      }),
    onSuccess: (job) => {
      toast.success("Dry-run fix queued");
      qc.invalidateQueries({ queryKey: ["fix-jobs"] });
      navigate({ to: "/fixes/$fixId", params: { fixId: job.id } });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Failed to queue fix"),
  });

  // Master gate: dark when off (or still resolving the flag).
  if (flagLoading || !autofixEnabled) return null;

  const fixable = fixableQ.data;
  const probing = fixableQ.isLoading;
  const writable = fixable?.writable ?? false;
  const reason =
    fixable && !fixable.writable
      ? fixable.reason
      : "Queue a dry-run fix for this finding (branch + diff only, no push)";

  const disabled = probing || !writable || enqueue.isPending;

  return (
    <div className="flex flex-col items-end gap-1">
      <button
        type="button"
        onClick={() => enqueue.mutate()}
        disabled={disabled}
        title={reason}
        className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
      >
        {enqueue.isPending || probing ? (
          <Loader2Icon className="size-4 animate-spin" />
        ) : (
          <WrenchIcon className="size-4" />
        )}
        Fix this finding (dry-run)
      </button>
      {!probing && fixable && !fixable.writable && (
        <span className="text-[11px] text-amber-300 max-w-xs text-right">
          {fixable.reason}
        </span>
      )}
    </div>
  );
}
