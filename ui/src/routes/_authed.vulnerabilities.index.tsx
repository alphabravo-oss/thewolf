import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import type { PaginationState } from "@tanstack/react-table";
import { ShieldAlert } from "lucide-react";
import { api } from "@/lib/api";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SeverityBadge } from "@/components/severity-badge";
import { severityRank } from "@/lib/severity";
import type { Vulnerability } from "@/lib/types";

const EMPTY: Vulnerability[] = [];

export const Route = createFileRoute("/_authed/vulnerabilities/")({
  component: VulnerabilitiesPage,
});

function VulnerabilitiesPage() {
  const navigate = useNavigate();
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 50,
  });
  const q = useQuery({
    queryKey: ["vulnerabilities", pagination.pageIndex, pagination.pageSize],
    queryFn: async () => {
      const params = new URLSearchParams({
        page: String(pagination.pageIndex + 1),
        per_page: String(pagination.pageSize),
      });
      const r = await api.get<Vulnerability[]>(`/vulnerabilities?${params}`);
      return { items: r.data ?? [], total: r.meta?.total ?? 0 };
    },
  });
  const rows = q.data?.items ?? EMPTY;

  const columns: Column<Vulnerability>[] = [
    {
      key: "severity",
      header: "Severity",
      width: "8rem",
      sortAccessor: (v) => severityRank[v.severity] ?? 0,
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
      {!q.isLoading && (q.data?.total ?? 0) === 0 ? (
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
          pageSize={pagination.pageSize}
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          searchPlaceholder="Search this page..."
          emptyMessage="No vulnerabilities match"
          serverSide={{
            rowCount: q.data?.total ?? 0,
            pagination,
            onPaginationChange: setPagination,
          }}
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
