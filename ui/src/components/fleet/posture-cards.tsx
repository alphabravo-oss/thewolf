// ui/src/components/fleet/posture-cards.tsx
import { useFleetPosture } from "@/lib/fleet";
import { MetricCard } from "@/components/ui/metric-card";
import { Skeleton } from "@/components/ui/skeleton";

export function PostureCards({ collectionId }: { collectionId?: string } = {}) {
  const q = useFleetPosture(collectionId);
  if (q.isLoading) {
    return (
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-28" />
        ))}
      </div>
    );
  }
  if (q.isError || !q.data) return <div role="alert" className="text-sm">Posture is unavailable. <button className="underline" onClick={() => void q.refetch()}>Retry posture</button></div>;
  const sev = q.data.open_findings_by_severity;
  const totalOpen =
    (sev.critical ?? 0) + (sev.high ?? 0) + (sev.medium ?? 0) + (sev.low ?? 0) + (sev.info ?? 0);

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
      <MetricCard title="Open findings" value={totalOpen} />
      <MetricCard title="High severity" value={sev.high ?? 0} />
      <MetricCard title="Critical severity" value={sev.critical ?? 0} />
      <MetricCard title="Gates failing" value={q.data.gates_failing} />
    </div>
  );
}
