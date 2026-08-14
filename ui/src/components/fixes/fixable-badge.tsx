// FixableBadge — the repo-level "can wolf write a fix here?" indicator, from
// GET /repos/{id}/fixable. Only rendered when autofix_enabled is on (the whole
// fix surface is dark otherwise). Green "fixable" when writable; muted
// "not fixable" with the reason as a tooltip when not.
import { ShieldCheckIcon, ShieldXIcon } from "lucide-react";
import { useAutofixEnabled, useRepoFixable } from "@/lib/fixes";

export function FixableBadge({ repoId }: { repoId: string }) {
  const { enabled, isLoading: flagLoading } = useAutofixEnabled();
  const q = useRepoFixable(repoId, enabled);

  if (flagLoading || !enabled) return null;
  if (q.isLoading || !q.data) return null;

  const { writable, can_push, reason } = q.data;
  if (writable) {
    return (
      <span
        className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-emerald-300 bg-emerald-500/10 border border-emerald-500/30 rounded px-1.5 py-0.5"
        title={reason}
      >
        <ShieldCheckIcon className="size-3" /> {can_push ? "fixable" : "fixable (no push)"}
      </span>
    );
  }
  return (
    <span
      className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-muted-foreground bg-muted/20 border border-border/30 rounded px-1.5 py-0.5"
      title={reason}
    >
      <ShieldXIcon className="size-3" /> not fixable
    </span>
  );
}
