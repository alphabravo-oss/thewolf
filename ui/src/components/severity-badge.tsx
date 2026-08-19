// Severity pill. Now a thin wrapper over the shared StatusBadge so severities
// draw from the same status scale as every other state pill in the app
// (see components/ui/status-badge.tsx and the `--color-status-*` tokens in
// globals.css). Kept as its own component because ~20 call sites pass a
// `Severity` and expect the label to read as a severity, not a status.
import type { Severity } from "@/lib/types";
import { StatusBadge } from "@/components/ui/status-badge";
import { severityLabel } from "@/lib/severity";

type Props = {
  severity: Severity | null | undefined;
  className?: string;
  children?: React.ReactNode;
  size?: "sm" | "md" | "lg";
};

export function SeverityBadge({ severity, className, children, size }: Props) {
  const sev = severity ?? "info";
  return (
    <StatusBadge
      status={sev}
      label={typeof children === "string" ? children : severityLabel[sev]}
      showDot={false}
      size={size}
      className={className}
    />
  );
}
