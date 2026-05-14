// Static scan detail. Stub for now — full version reuses the old UI's
// scan-detail page; phased in during Phase 2.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeftIcon, BugIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Finding, Scan } from "@/lib/types";
import { CardSkeleton, ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { SeverityBadge } from "@/components/severity-badge";

export const Route = createFileRoute("/_authed/scans/$scanId")({
  component: ScanDetailPage,
});

function ScanDetailPage() {
  const { scanId } = Route.useParams();

  const scanQ = useQuery({
    queryKey: ["scan", scanId],
    queryFn: async () => {
      const r = await api.get<Scan>(`/scans/${scanId}`);
      return r.data;
    },
  });
  const findingsQ = useQuery({
    queryKey: ["scan", scanId, "findings"],
    queryFn: async () => {
      const r = await api.get<{ findings: Finding[] }>(
        `/scans/${scanId}/findings?limit=500`,
      );
      return r.data.findings ?? [];
    },
  });

  if (scanQ.isLoading || !scanQ.data) {
    return (
      <div className="p-6 space-y-3">
        <CardSkeleton />
      </div>
    );
  }
  const scan = scanQ.data;

  return (
    <div className="p-6 space-y-6 max-w-7xl">
      <div className="flex items-center gap-3">
        <Link
          to="/scans"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div className="flex-1 min-w-0">
          <h1 className="text-xl font-semibold truncate">
            {scan.repo?.name ?? scan.id.slice(0, 8)}{" "}
            <span className="text-muted-foreground font-normal">
              · {scan.branch}
            </span>
          </h1>
          <p className="text-xs text-muted-foreground">
            {scan.tools_completed.length}/{scan.tools_selected.length} tools
            completed · {scan.finding_count} findings
          </p>
        </div>
        <ScanStatusPill status={scan.status} />
        {scan.status === "running" && (
          <Link
            to="/scans/$scanId/live"
            params={{ scanId }}
            className="inline-flex items-center px-3 h-9 rounded-md bg-blue-500/15 ring-1 ring-blue-500/30 text-blue-300 text-sm hover:bg-blue-500/20"
          >
            Watch live →
          </Link>
        )}
      </div>

      <section>
        <h2 className="text-base font-semibold mb-3">Findings</h2>
        {findingsQ.isLoading ? (
          <ListSkeleton rows={8} />
        ) : !findingsQ.data || findingsQ.data.length === 0 ? (
          <EmptyState
            icon={BugIcon}
            title="No findings"
            description="This scan completed without any issues. Nice."
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
                  <th className="text-right px-4 py-2 font-medium">Line</th>
                </tr>
              </thead>
              <tbody>
                {findingsQ.data.map((f) => (
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
                    <td className="px-4 py-2 text-right text-muted-foreground tabular-nums">
                      {f.line_start || "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
