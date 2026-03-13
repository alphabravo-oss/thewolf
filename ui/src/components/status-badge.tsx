import { Badge } from "@/components/ui/badge";

const variants: Record<string, string> = {
  open: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
  fixed: "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
  wont_fix: "bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200",
  false_positive: "bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200",
  pending: "bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200",
  running: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
  completed: "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
  failed: "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200",
  cancelled: "bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200",
  paused: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200",
  stopped: "bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200",
  in_progress: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
  skipped: "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400",
  pass: "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
  fail: "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200",
};

const labels: Record<string, string> = {
  wont_fix: "Won't Fix",
  false_positive: "False Positive",
  in_progress: "In Progress",
};

export function StatusBadge({ status }: { status: string }) {
  const variant = variants[status] || variants.pending;
  const label = labels[status] || status.replace(/_/g, " ");
  return (
    <Badge variant="outline" className={variant}>
      {label}
    </Badge>
  );
}
