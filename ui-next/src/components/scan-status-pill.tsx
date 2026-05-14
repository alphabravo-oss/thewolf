// Run-state pill with a subtle glow. Used in scan lists, dashboard,
// live scan view.
import type { ScanStatus } from "@/lib/types";
import { cn } from "@/lib/cn";

const styles: Record<
  ScanStatus,
  { label: string; cls: string; glow: string }
> = {
  pending: {
    label: "Pending",
    cls: "bg-indigo-500/15 text-indigo-300 ring-indigo-500/25",
    glow: "glow-pending",
  },
  running: {
    label: "Running",
    cls: "bg-blue-500/15 text-blue-300 ring-blue-500/25 animate-pulse-glow",
    glow: "glow-info",
  },
  completed: {
    label: "Completed",
    cls: "bg-emerald-500/15 text-emerald-300 ring-emerald-500/25",
    glow: "glow-success",
  },
  failed: {
    label: "Failed",
    cls: "bg-red-500/15 text-red-300 ring-red-500/25",
    glow: "glow-error",
  },
  cancelled: {
    label: "Cancelled",
    cls: "bg-zinc-500/15 text-zinc-300 ring-zinc-500/25",
    glow: "",
  },
};

export function ScanStatusPill({ status }: { status: ScanStatus }) {
  const s = styles[status] ?? styles.pending;
  return (
    <span
      className={cn(
        "inline-flex items-center px-2 py-0.5 rounded-md text-xs font-medium ring-1 ring-inset whitespace-nowrap",
        s.cls,
        s.glow,
      )}
    >
      {s.label}
    </span>
  );
}
