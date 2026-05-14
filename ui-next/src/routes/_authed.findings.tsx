// Findings list with row navigation, severity filter, and a side-panel
// preview (Phase 4 will add multi-select + saved views).
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { BugIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Finding } from "@/lib/types";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { SeverityBadge } from "@/components/severity-badge";

export const Route = createFileRoute("/_authed/findings")({
  component: FindingsPage,
});

function FindingsPage() {
  const q = useQuery({
    queryKey: ["findings", "all"],
    queryFn: async () => {
      const r = await api.get<{ findings: Finding[] }>("/findings?limit=500");
      return r.data.findings ?? [];
    },
  });

  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Findings</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Aggregated across all your scans.
        </p>
      </header>

      {q.isLoading ? (
        <TableSkeleton rows={12} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={BugIcon}
          title="No findings yet"
          description="Run a scan and findings will land here."
          cta={{ label: "Go to scans", to: "/scans" }}
        />
      ) : (
        <div className="glass-card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Severity</th>
                <th className="text-left px-4 py-2 font-medium">Tool</th>
                <th className="text-left px-4 py-2 font-medium">Title</th>
                <th className="text-left px-4 py-2 font-medium">File</th>
                <th className="text-left px-4 py-2 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((f) => (
                <tr
                  key={f.id}
                  className="border-t border-border/30 table-row-hover"
                >
                  <td className="px-4 py-2">
                    <SeverityBadge severity={f.severity} />
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {f.tool_name}
                  </td>
                  <td className="px-4 py-2">
                    <Link
                      to="/findings/$findingId"
                      params={{ findingId: f.id }}
                      className="hover:underline"
                    >
                      {f.title}
                    </Link>
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground truncate max-w-xs">
                    {f.file_path}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {f.status}
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
