// Run-state pill for scans. Delegates to the shared StatusBadge so a "running"
// scan is coloured identically to a running workload in Astronomer; the status
// -> tone mapping lives in lib/utils.ts.
import { parseToolList, type Scan, type ScanStatus } from "@/lib/types";
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
  scan?: Pick<Scan, "tools_selected" | "tools_completed" | "tools_failed" | "completeness">;
  incomplete?: boolean;
  gateStatus?: string;
  className?: string;
  children?: React.ReactNode;
  size?: "sm" | "md" | "lg";
};

export function ScanStatusPill({ status, scan, incomplete, gateStatus, className, children, size }: Props) {
  const s = (status ?? "pending") as ScanStatus;
  const selected = parseToolList(scan?.tools_selected);
  const completed = parseToolList(scan?.tools_completed);
  const partial = s === "completed" && scan && (
    incomplete || scan.completeness === "incomplete" || selected.length === 0 || parseToolList(scan.tools_failed).length > 0 ||
    selected.some((tool) => !completed.includes(tool))
  );
  const gateFailed = s === "completed" && (gateStatus === "fail" || gateStatus === "failed");
  const gateUnresolved = s === "completed" && gateStatus && gateStatus !== "pass" && !gateFailed;
  return (
    <StatusBadge
      status={partial || gateUnresolved ? "warning" : gateFailed ? "failed" : s}
      label={partial ? "Incomplete coverage" : gateFailed ? "Gate failed" : gateUnresolved ? gateStatus === "pending" ? "Checking gate" : "Gate needs attention" : typeof children === "string" ? children : labels[s]}
      pulse={s === "running"}
      size={size}
      className={className}
    />
  );
}
