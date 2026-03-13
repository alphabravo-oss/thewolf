import { cn } from "@/lib/utils";

function getColor(score: number): string {
  if (score >= 80) return "bg-red-500";
  if (score >= 60) return "bg-orange-500";
  if (score >= 40) return "bg-yellow-500";
  if (score >= 20) return "bg-blue-500";
  return "bg-green-500";
}

export function ScoreBar({
  score,
  label,
  className,
}: {
  score: number;
  label?: string;
  className?: string;
}) {
  const pct = Math.min(100, Math.max(0, score));
  return (
    <div className={cn("flex items-center gap-2", className)}>
      {label && (
        <span className="text-xs text-muted-foreground w-20 shrink-0">
          {label}
        </span>
      )}
      <div className="flex-1 h-2 bg-muted rounded-full overflow-hidden">
        <div
          className={cn("h-full rounded-full transition-all", getColor(pct))}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="text-xs font-mono w-8 text-right">{pct.toFixed(0)}</span>
    </div>
  );
}
