// ui/src/components/fleet/posture-cards.tsx
import { Card, CardContent } from "@/components/ui/card";
import { useFleetPosture } from "@/lib/fleet";
import { Skeleton } from "@/components/ui/skeleton";
import { TrendingUpIcon, TrendingDownIcon, MinusIcon } from "lucide-react";

export function PostureCards() {
  const q = useFleetPosture();
  if (q.isLoading) {
    return (
      <div className="grid grid-cols-4 gap-3">
        {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-28" />)}
      </div>
    );
  }
  if (!q.data) return null;
  const sev = q.data.open_findings_by_severity;
  const delta = q.data.week_over_week_delta;
  const totalOpen = (sev.critical ?? 0) + (sev.high ?? 0) + (sev.medium ?? 0) + (sev.low ?? 0) + (sev.info ?? 0);
  const totalDelta = Object.values(delta).reduce((a, b) => a + b, 0);

  return (
    <div className="grid grid-cols-4 gap-3">
      <PostureCard label="Open findings" value={totalOpen} delta={totalDelta} />
      <PostureCard label="High severity" value={sev.high ?? 0} delta={delta.high ?? 0} />
      <PostureCard label="Critical severity" value={sev.critical ?? 0} delta={delta.critical ?? 0} />
      <PostureCard label="Gates failing" value={q.data.gates_failing} />
    </div>
  );
}

function PostureCard({ label, value, delta }: { label: string; value: number; delta?: number }) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
        <div className="mt-1 text-3xl font-semibold tabular-nums">{value}</div>
        {delta !== undefined && <DeltaPill delta={delta} />}
      </CardContent>
    </Card>
  );
}

function DeltaPill({ delta }: { delta: number }) {
  const Icon = delta > 0 ? TrendingUpIcon : delta < 0 ? TrendingDownIcon : MinusIcon;
  const tone = delta > 0 ? "text-red-400" : delta < 0 ? "text-emerald-400" : "text-muted-foreground";
  return (
    <div className={`mt-2 inline-flex items-center gap-1 text-xs ${tone}`}>
      <Icon className="size-3" />
      {delta > 0 ? `+${delta}` : delta} this week
    </div>
  );
}
