// Shimmer skeletons. Used during initial route-load and inside TanStack
// Query `pending` states. Always render a layout that roughly matches the
// page it precedes — that's the trick to reducing perceived loading time.
import { cn } from "@/lib/utils";

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("shimmer h-4 w-full", className)} />;
}

export function CardSkeleton() {
  return (
    <div className="glass-card p-5 space-y-3">
      <Skeleton className="h-5 w-1/3" />
      <Skeleton className="h-3 w-2/3" />
      <Skeleton className="h-3 w-1/2" />
    </div>
  );
}

export function ListSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: rows }).map((_, i) => (
        <div
          key={i}
          className="glass-card px-4 py-3 flex items-center gap-3"
        >
          <Skeleton className="h-4 w-4 rounded-full" />
          <Skeleton className="h-4 flex-1 max-w-sm" />
          <Skeleton className="h-4 w-20" />
        </div>
      ))}
    </div>
  );
}

export function TableSkeleton({ rows = 8 }: { rows?: number }) {
  return (
    <div className="glass-card overflow-hidden">
      <div className="px-4 py-3 border-b border-border">
        <Skeleton className="h-4 w-24" />
      </div>
      {Array.from({ length: rows }).map((_, i) => (
        <div
          key={i}
          className="px-4 py-3 border-b border-border grid grid-cols-6 gap-4"
        >
          <Skeleton className="h-3" />
          <Skeleton className="h-3 col-span-2" />
          <Skeleton className="h-3" />
          <Skeleton className="h-3" />
          <Skeleton className="h-3" />
        </div>
      ))}
    </div>
  );
}
