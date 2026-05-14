// Loop detail — Phase 1 stub.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Loop } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";

export const Route = createFileRoute("/_authed/loops/$loopId")({
  component: LoopDetailPage,
});

function LoopDetailPage() {
  const { loopId } = Route.useParams();
  const q = useQuery({
    queryKey: ["loop", loopId],
    queryFn: async () => {
      const r = await api.get<Loop>(`/loops/${loopId}`);
      return r.data;
    },
    refetchInterval: 5000,
  });
  if (q.isLoading || !q.data) return <CardSkeleton />;
  const l = q.data;
  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-center gap-3">
        <Link to="/loops" className="size-9 grid place-items-center rounded-md hover:bg-muted/50">
          <ArrowLeftIcon className="size-4" />
        </Link>
        <h1 className="text-xl font-semibold">{l.repo?.name ?? l.repo_id.slice(0, 8)}</h1>
      </div>
      <section className="glass-card p-5 space-y-2 text-sm">
        <div><strong>Status:</strong> {l.status}</div>
        <div><strong>Iteration:</strong> {l.current_iteration}/{l.max_iterations}</div>
        <div><strong>Findings initial:</strong> {l.total_findings_initial}</div>
        <div><strong>Findings fixed:</strong> {l.total_findings_fixed}</div>
        <div><strong>Findings remaining:</strong> {l.total_findings_remaining}</div>
      </section>
    </div>
  );
}
