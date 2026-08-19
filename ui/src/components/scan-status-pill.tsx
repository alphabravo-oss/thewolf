// Run-state pill for scans. Delegates to the shared StatusBadge so a "running"
// scan is coloured identically to a running workload in Astronomer; the status
// -> tone mapping lives in lib/utils.ts.
import type { ScanStatus } from "@/lib/types";
import { StatusBadge } from "@/components/ui/status-badge";

const labels: Record<ScanStatus, string> = {
  pending: "Pending",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

type Props = {
  status: ScanStatus | null | undefined;
  className?: string;
  children?: React.ReactNode;
  size?: "sm" | "md" | "lg";
};

export function ScanStatusPill({ status, className, children, size }: Props) {
  const s = (status ?? "pending") as ScanStatus;
  return (
    <StatusBadge
      status={s}
      label={typeof children === "string" ? children : labels[s]}
      pulse={s === "running"}
      size={size}
      className={className}
    />
  );
}
