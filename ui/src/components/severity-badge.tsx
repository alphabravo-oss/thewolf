// Severity badge — used in tables, finding cards, sidebar pills.
import type { Severity } from "@/lib/types";
import { severityBadgeClass, severityLabel } from "@/lib/severity";

export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span className={severityBadgeClass[severity]}>
      {severityLabel[severity]}
    </span>
  );
}
