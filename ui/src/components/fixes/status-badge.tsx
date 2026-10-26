import { StatusBadge as SharedStatusBadge } from "@/components/ui/status-badge";
import type { FixJobStatus } from "@/lib/fixes";

export function StatusBadge({ status }: { status: FixJobStatus }) {
  return <SharedStatusBadge status={status} size="sm" />;
}
