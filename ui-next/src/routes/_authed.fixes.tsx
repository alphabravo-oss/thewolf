// Fixes list — Phase 1 stub.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { WrenchIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Fix } from "@/lib/types";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";

export const Route = createFileRoute("/_authed/fixes")({
  component: FixesPage,
});

function FixesPage() {
  const q = useQuery({
    queryKey: ["fixes", "all"],
    queryFn: async () => {
      const r = await api.get<{ fixes: Fix[] }>("/fixes?limit=200");
      return r.data.fixes ?? [];
    },
    refetchInterval: 10_000,
  });
  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <h1 className="text-2xl font-semibold tracking-tight">Fixes</h1>
      {q.isLoading ? (
        <TableSkeleton rows={8} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={WrenchIcon}
          title="No fixes yet"
          description="Trigger an automated fix from a finding's detail page."
        />
      ) : (
        <div className="glass-card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="text-left px-4 py-2">Status</th>
                <th className="text-left px-4 py-2">Branch</th>
                <th className="text-right px-4 py-2">Fixed</th>
                <th className="text-right px-4 py-2">Failed</th>
                <th className="text-right px-4 py-2">Started</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((f) => (
                <tr key={f.id} className="border-t border-border/30 table-row-hover">
                  <td className="px-4 py-2">
                    <Link
                      to="/fixes/$fixId"
                      params={{ fixId: f.id }}
                      className="hover:underline"
                    >
                      {f.status}
                    </Link>
                  </td>
                  <td className="px-4 py-2 font-mono text-xs">{f.branch_name}</td>
                  <td className="px-4 py-2 text-right tabular-nums">{f.findings_fixed}</td>
                  <td className="px-4 py-2 text-right tabular-nums">{f.findings_failed}</td>
                  <td className="px-4 py-2 text-right text-muted-foreground tabular-nums">
                    {f.started_at ? new Date(f.started_at).toLocaleString() : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
