// Scans list. Virtualized table for large lists; empty-state + skeleton
// for cold loads.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GaugeIcon, PlayIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Scan } from "@/lib/types";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { ScanStatusPill } from "@/components/scan-status-pill";

export const Route = createFileRoute("/_authed/scans")({
  component: ScansPage,
});

function ScansPage() {
  const q = useQuery({
    queryKey: ["scans", "list"],
    queryFn: async () => {
      const r = await api.get<{ scans: Scan[] }>("/scans?limit=200");
      return r.data.scans ?? [];
    },
    refetchInterval: 10_000,
  });

  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Scans</h1>
        <button
          type="button"
          className="inline-flex items-center gap-2 px-3 h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90"
        >
          <PlayIcon className="size-4" />
          New scan
        </button>
      </header>

      {q.isLoading ? (
        <TableSkeleton rows={10} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={GaugeIcon}
          title="No scans yet"
          description="Start by creating a collection, then trigger a scan from it."
          cta={{ label: "Go to collections", to: "/collections" }}
        />
      ) : (
        <div className="glass-card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="text-left font-medium px-4 py-2">Status</th>
                <th className="text-left font-medium px-4 py-2">Repo</th>
                <th className="text-left font-medium px-4 py-2">Branch</th>
                <th className="text-left font-medium px-4 py-2">Tools</th>
                <th className="text-right font-medium px-4 py-2">Findings</th>
                <th className="text-right font-medium px-4 py-2">Started</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((s) => (
                <tr key={s.id} className="border-t border-border/30 table-row-hover">
                  <td className="px-4 py-2">
                    <ScanStatusPill status={s.status} />
                  </td>
                  <td className="px-4 py-2">
                    <Link
                      to={
                        s.status === "running"
                          ? "/scans/$scanId/live"
                          : "/scans/$scanId"
                      }
                      params={{ scanId: s.id }}
                      className="font-medium hover:underline"
                    >
                      {s.repo?.name ?? s.id.slice(0, 8)}
                    </Link>
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {s.branch}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground tabular-nums">
                    {s.tools_completed.length}/{s.tools_selected.length}
                  </td>
                  <td className="px-4 py-2 text-right font-mono tabular-nums">
                    {s.finding_count}
                  </td>
                  <td className="px-4 py-2 text-right text-muted-foreground tabular-nums">
                    {s.started_at
                      ? new Date(s.started_at).toLocaleString()
                      : "—"}
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
