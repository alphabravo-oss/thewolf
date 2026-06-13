// Loops list — Phase 1 stub.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { RepeatIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Loop } from "@/lib/types";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";

export const Route = createFileRoute("/_authed/loops/")({
  component: LoopsPage,
});

function LoopsPage() {
  const q = useQuery({
    queryKey: ["loops", "all"],
    queryFn: async () => {
      const r = await api.get<Loop[]>("/loops?limit=200");
      return r.data ?? [];
    },
    refetchInterval: 10_000,
  });
  return (
    <div className="page stack">
      <h1 className="text-2xl font-semibold tracking-tight">Loops</h1>
      {q.isLoading ? (
        <TableSkeleton rows={6} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={RepeatIcon}
          title="No loops running"
          description="Loops chain scan → fix → rescan iterations with guardrails."
        />
      ) : (
        <div className="glass-card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="text-left px-4 py-2">Status</th>
                <th className="text-left px-4 py-2">Repo</th>
                <th className="text-right px-4 py-2">Iter</th>
                <th className="text-right px-4 py-2">Fixed</th>
                <th className="text-right px-4 py-2">Remaining</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((l) => (
                <tr key={l.id} className="border-t border-border/30 table-row-hover">
                  <td className="px-4 py-2">
                    <Link to="/loops/$loopId" params={{ loopId: l.id }} className="hover:underline">
                      {l.status}
                    </Link>
                  </td>
                  <td className="px-4 py-2">{l.repo?.name ?? l.repo_id.slice(0, 8)}</td>
                  <td className="px-4 py-2 text-right tabular-nums">
                    {l.current_iteration}/{l.max_iterations}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums">{l.total_findings_fixed}</td>
                  <td className="px-4 py-2 text-right tabular-nums">{l.total_findings_remaining}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
