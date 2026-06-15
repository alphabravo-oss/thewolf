// Fix detail — the per-job view for the autonomous fix engine. It shows the
// job status + metadata, the per-attempt audit trail (engine used + verify
// outcomes), the proposed-diff viewer, and a live log console (SSE relay of the
// out-of-process worker). Gated on autofix_enabled like the rest of the surface.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon, Loader2Icon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { CardSkeleton } from "@/components/skeleton";
import { AttemptsTable } from "@/components/fixes/attempts-table";
import { DiffViewer } from "@/components/fixes/diff-viewer";
import { FixLogConsole } from "@/components/fixes/fix-log-console";
import { StatusBadge } from "./_authed.fixes.index";
import {
  cancelFix,
  isFixTerminal,
  useAutofixEnabled,
  useFixJob,
} from "@/lib/fixes";

export const Route = createFileRoute("/_authed/fixes/$fixId")({
  component: FixDetailPage,
});

function FixDetailPage() {
  const { fixId } = Route.useParams();
  const qc = useQueryClient();
  const { enabled, isLoading: flagLoading } = useAutofixEnabled();
  const q = useFixJob(fixId, enabled);

  const cancel = useMutation({
    mutationFn: () => cancelFix(fixId),
    onSuccess: () => {
      toast.success("Fix cancelled");
      qc.invalidateQueries({ queryKey: ["fix-job", fixId] });
      qc.invalidateQueries({ queryKey: ["fix-jobs"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Cancel failed"),
  });

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
        <BackLink />
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
  const terminal = isFixTerminal(f.status);

  return (
    <div className="page stack page--mid">
      <div className="flex items-center gap-3">
        <BackLink />
        <div className="flex items-center gap-2 flex-1 min-w-0">
          <StatusBadge status={f.status} />
          <h1 className="text-xl font-semibold truncate">
            {f.result_branch || f.target_branch || `Fix ${f.id.slice(0, 8)}`}
          </h1>
        </div>
        {!terminal && (
          <button
            type="button"
            onClick={() => cancel.mutate()}
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

      <section className="glass-card p-5 grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
        <Meta label="Engine" value={f.engine} mono />
        <Meta label="Mode" value={f.mode} mono />
        <Meta label="Severity floor" value={f.severity_floor || "—"} />
        <Meta label="Max attempts" value={String(f.max_attempts || "—")} />
        <Meta label="Findings" value={String(f.finding_ids?.length ?? 0)} />
        <Meta label="Branch" value={f.result_branch || f.target_branch || "—"} mono />
        {f.error && (
          <div className="col-span-2 text-xs text-destructive break-words">
            {f.error}
          </div>
        )}
      </section>

      <FixLogConsole fixId={f.id} status={f.status} />

      <AttemptsTable attempts={f.attempts ?? []} />

      <DiffViewer fixId={f.id} />
    </div>
  );
}

function BackLink() {
  return (
    <Link
      to="/fixes"
      className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
      aria-label="Back"
    >
      <ArrowLeftIcon className="size-4" />
    </Link>
  );
}

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
