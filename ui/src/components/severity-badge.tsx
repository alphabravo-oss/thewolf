import { Badge } from "@/components/ui/badge";
import type { Severity } from "@/lib/types";

const variants: Record<Severity, string> = {
  critical: "bg-red-600 text-white hover:bg-red-700",
  high: "bg-orange-500 text-white hover:bg-orange-600",
  medium: "bg-yellow-500 text-black hover:bg-yellow-600",
  low: "bg-blue-500 text-white hover:bg-blue-600",
  info: "bg-gray-400 text-black hover:bg-gray-500",
};

export function SeverityBadge({ severity }: { severity: Severity }) {
  return <Badge className={variants[severity]}>{severity}</Badge>;
}
