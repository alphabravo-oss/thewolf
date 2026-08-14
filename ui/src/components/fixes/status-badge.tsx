import { isFixTerminal, type FixJobStatus } from "@/lib/fixes";

export function StatusBadge({ status }: { status: FixJobStatus }) {
  const cls = isFixTerminal(status)
    ? status === "succeeded"
      ? "text-emerald-300 bg-emerald-500/10 border-emerald-500/30"
      : status === "failed"
        ? "text-rose-300 bg-rose-500/10 border-rose-500/30"
        : status === "superseded"
          ? "text-muted-foreground bg-muted/20 border-border/30"
          : "text-muted-foreground bg-muted/20 border-border/30"
    : status === "push_failed"
      ? "text-amber-300 bg-amber-500/10 border-amber-500/30"
      : status === "awaiting_review" || status === "awaiting_push"
        ? "text-amber-300 bg-amber-500/10 border-amber-500/30"
        : status === "queued"
        ? "text-muted-foreground bg-muted/20 border-border/30"
        : "text-sky-300 bg-sky-500/10 border-sky-500/30";
  return (
    <span
      className={`text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 ${cls}`}
    >
      {status.replace("_", " ")}
    </span>
  );
}
