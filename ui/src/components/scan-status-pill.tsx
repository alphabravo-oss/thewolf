// Run-state pill. Used in scan lists, dashboard, live scan view.
import { cva, type VariantProps } from "class-variance-authority";
import type { ScanStatus } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const scanStatusVariants = cva("", {
  variants: {
    status: {
      pending:   "border-indigo-500/40 bg-indigo-500/15 text-indigo-300",
      running:   "border-blue-500/40 bg-blue-500/15 text-blue-300 animate-pulse-glow",
      completed: "border-emerald-500/40 bg-emerald-500/15 text-emerald-300",
      failed:    "border-red-500/40 bg-red-500/15 text-red-300",
      cancelled: "border-zinc-500/40 bg-zinc-500/15 text-zinc-300",
    },
  },
  defaultVariants: { status: "pending" },
});

const labels: Record<ScanStatus, string> = {
  pending: "Pending",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

type Props = VariantProps<typeof scanStatusVariants> & {
  className?: string;
  children?: React.ReactNode;
};

export function ScanStatusPill({ status, className, children }: Props) {
  return (
    <Badge variant="outline" className={cn(scanStatusVariants({ status }), className)}>
      {children ?? labels[(status ?? "pending") as ScanStatus]}
    </Badge>
  );
}
