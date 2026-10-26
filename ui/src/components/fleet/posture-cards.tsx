// ui/src/components/fleet/posture-cards.tsx
import { useFleetPosture } from "@/lib/fleet";
import { Skeleton } from "@/components/ui/skeleton";


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
  if (q.isError || !q.data) return <div role="alert" className="text-sm">Posture is unavailable. <button className="underline" onClick={() => void q.refetch()}>Retry posture</button></div>;
  const sev = q.data.open_findings_by_severity;
  const totalOpen =
    (sev.critical ?? 0) + (sev.high ?? 0) + (sev.medium ?? 0) + (sev.low ?? 0) + (sev.info ?? 0);

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
      <PostureCard label="Open findings" value={totalOpen} />
      <PostureCard label="High severity" value={sev.high ?? 0} tone="high" />
      <PostureCard label="Critical severity" value={sev.critical ?? 0} tone="critical" />
      <PostureCard label="Gates failing" value={q.data.gates_failing} tone="critical" />
    </div>
  );
}

function PostureCard({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
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
    </div>
  );
}
