import { isFixTerminal, type FixJobStatus } from "@/lib/fixes";

export function StatusBadge({ status }: { status: FixJobStatus }) {
  const cls = isFixTerminal(status)
    ? status === "succeeded"
      ? "text-status-success bg-status-success/10 border-status-success/30"
      : status === "failed"
        ? "text-status-error bg-status-error/10 border-status-error/30"
        : status === "superseded"
          ? "text-muted-foreground bg-muted/20 border-border"
          : "text-muted-foreground bg-muted/20 border-border"
    : status === "push_failed"
      ? "text-status-warning bg-status-warning/10 border-status-warning/30"
      : status === "awaiting_review" || status === "awaiting_push"
        ? "text-status-warning bg-status-warning/10 border-status-warning/30"
        : status === "queued"
        ? "text-muted-foreground bg-muted/20 border-border"
        : "text-status-info bg-status-info/10 border-status-info/30";
  return (
    <span
      className={`text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 ${cls}`}
    >
      {status.replace("_", " ")}
    </span>
  );
}
