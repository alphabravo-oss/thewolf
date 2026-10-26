// FixableBadge — the repo-level "can wolf write a fix here?" indicator, from
// GET /repos/{id}/fixable. Only rendered when autofix_enabled is on (the whole
// fix surface is dark otherwise). Green "fixable" when writable; muted
// "not fixable" with the reason as a tooltip when not.
import { StatusBadge } from "@/components/ui/status-badge";
import { ShieldCheckIcon, ShieldXIcon } from "lucide-react";
import { useAutofixEnabled, useRepoFixable } from "@/lib/fixes";

export function FixableBadge({ repoId }: { repoId: string }) {
  const { enabled, isLoading: flagLoading } = useAutofixEnabled();
  const q = useRepoFixable(repoId, enabled);

  if (flagLoading || !enabled) return null;
  if (q.isLoading || !q.data) return null;

  const { writable, can_push, reason } = q.data;
  return (
    <span title={reason}>
      <StatusBadge
        status={writable ? "success" : "neutral"}
        label={writable ? (can_push ? "Fixable" : "Fixable (no push)") : "Not fixable"}
        icon={writable ? <ShieldCheckIcon className="size-3" /> : <ShieldXIcon className="size-3" />}
        size="sm"
      />
    </span>
  );
}
