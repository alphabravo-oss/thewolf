import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ShieldAlert } from "lucide-react";
import { api } from "@/lib/api";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SeverityBadge } from "@/components/severity-badge";
import type { Vulnerability } from "@/lib/types";

const EMPTY: Vulnerability[] = [];

const SEVERITY_RANK: Record<string, number> = {
  critical: 5,
  high: 4,
  medium: 3,
  low: 2,
  info: 1,
};

export const Route = createFileRoute("/_authed/vulnerabilities/")({
  component: VulnerabilitiesPage,
});

function VulnerabilitiesPage() {
  const navigate = useNavigate();
  const q = useQuery({
    queryKey: ["vulnerabilities"],
    queryFn: async () => {
      const r = await api.get<Vulnerability[]>("/vulnerabilities?per_page=200");
      return r.data ?? [];
    },
  });
  const rows = q.data ?? EMPTY;

  const columns: Column<Vulnerability>[] = [
    {
      key: "severity",
      header: "Severity",
      width: "8rem",
      sortAccessor: (v) => SEVERITY_RANK[v.severity] ?? 0,
      accessor: (v) => <SeverityBadge severity={v.severity} size="sm" />,
    },
    {
      key: "title",
      header: "Title",
      sortAccessor: (v) => v.title,
      accessor: (v) => (
        <Link
          to="/vulnerabilities/$vulnerabilityId"
          params={{ vulnerabilityId: v.id }}
          className="hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          {v.title}
        </Link>
      ),
    },
    {
      key: "evidence",
      header: "Evidence",
      align: "right",
      sortAccessor: (v) => v.evidence_count,
      accessor: (v) => (
        <span className="tabular-nums">{v.evidence_count}</span>
      ),
    },
    {
      key: "tools",
      header: "Flagged by",
      sortAccessor: (v) => (v.corroborated_by ?? []).join(","),
      accessor: (v) => (
        <span className="font-mono text-xs text-muted-foreground">
          {(v.corroborated_by ?? []).join(", ") || "—"}
        </span>
      ),
    },
    {
      key: "baseline",
      header: "Baseline",
      sortAccessor: (v) => v.baseline_state ?? "",
      accessor: (v) => (
        <span className="text-xs text-muted-foreground">
          {v.baseline_state || "—"}
        </span>
      ),
    },
  ];

  return (
    <PageShell>
      <PageHeader
        title="Vulnerabilities"
        description="Canonical issues clustered from scanner findings. Findings stay the compatibility layer."
      />
      {!q.isLoading && rows.length === 0 ? (
        <EmptyState
          icon={ShieldAlert}
          title="No vulnerabilities yet"
          description="Complete a scan, or open Findings if clusters have not dual-written yet."
        />
      ) : (
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(v) => v.id}
          persistKey="vulnerabilities"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          searchPlaceholder="Filter title, tools..."
          emptyMessage="No vulnerabilities match"
          onRowClick={(v) =>
            navigate({
              to: "/vulnerabilities/$vulnerabilityId",
              params: { vulnerabilityId: v.id },
            })
          }
        />
      )}
    </PageShell>
  );
}
