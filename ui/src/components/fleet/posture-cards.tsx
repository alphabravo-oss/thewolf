// ui/src/components/fleet/posture-cards.tsx
import { useFleetPosture } from "@/lib/fleet";
import { Skeleton } from "@/components/ui/skeleton";
import { TrendingUpIcon, TrendingDownIcon, MinusIcon } from "lucide-react";

export function PostureCards({ collectionId }: { collectionId?: string } = {}) {
  const q = useFleetPosture(collectionId);
  if (q.isLoading) {
    return (
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-[6.5rem] rounded-[0.7rem]" />
        ))}
      </div>
    );
  }
  if (!q.data) return null;
  const sev = q.data.open_findings_by_severity;
  const delta = q.data.week_over_week_delta;
  const totalOpen =
    (sev.critical ?? 0) + (sev.high ?? 0) + (sev.medium ?? 0) + (sev.low ?? 0) + (sev.info ?? 0);
  const totalDelta = Object.values(delta).reduce((a, b) => a + b, 0);

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
      <PostureCard label="Open findings" value={totalOpen} delta={totalDelta} />
      <PostureCard label="High severity" value={sev.high ?? 0} delta={delta.high ?? 0} tone="high" />
      <PostureCard label="Critical severity" value={sev.critical ?? 0} delta={delta.critical ?? 0} tone="critical" />
      <PostureCard label="Gates failing" value={q.data.gates_failing} tone="critical" />
    </div>
  );
}

function PostureCard({
  label,
  value,
  delta,
  tone,
}: {
  label: string;
  value: number;
  delta?: number;
  tone?: "high" | "critical";
}) {
  const accent =
    value > 0 && tone === "critical"
      ? "text-[hsl(0_72%_64%)]"
      : value > 0 && tone === "high"
        ? "text-[hsl(28_90%_60%)]"
        : "";
  return (
    <div className="stat-card">
      <div className="stat-label">{label}</div>
      <div className={`stat-value ${accent}`}>{value}</div>
      {delta !== undefined && <DeltaPill delta={delta} />}
    </div>
  );
}

function DeltaPill({ delta }: { delta: number }) {
  // Findings going UP week-over-week is bad (red); going down is good (green).
  const Icon = delta > 0 ? TrendingUpIcon : delta < 0 ? TrendingDownIcon : MinusIcon;
  const cls = delta > 0 ? "stat-delta up" : delta < 0 ? "stat-delta down" : "stat-delta";
  return (
    <div className={cls}>
      <Icon className="size-3.5" />
      {delta > 0 ? `+${delta}` : delta} this week
    </div>
  );
}
