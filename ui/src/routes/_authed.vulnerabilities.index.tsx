import { findingTitle, FindingTitle } from "@/lib/finding-title";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ShieldAlert } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { EmptyState, PermissionState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SeverityBadge } from "@/components/severity-badge";
import { severityRank } from "@/lib/severity";
import type { Vulnerability } from "@/lib/types";

const EMPTY: Vulnerability[] = [];

type Search = { q?: string; page?: number; sort?: string; order?: "asc" | "desc" };
export const Route = createFileRoute("/_authed/vulnerabilities/")({
  validateSearch: (s): Search => ({
    q: typeof s.q === "string" ? s.q : undefined,
    page: Number.isSafeInteger(Number(s.page)) && Number(s.page) > 0 ? Number(s.page) : undefined,
    sort: typeof s.sort === "string" && ["severity", "title", "evidence", "baseline"].includes(s.sort) ? s.sort : undefined,
    order: s.order === "asc" ? "asc" : "desc",
  }),
  component: VulnerabilitiesPage,
});

function VulnerabilitiesPage() {
  const navigate = Route.useNavigate();
  const search = Route.useSearch();
  const pagination = { pageIndex: (search.page ?? 1) - 1, pageSize: 50 };
  const sorting = [{ id: search.sort ?? "severity", desc: search.order !== "asc" }];
  const q = useQuery({
    queryKey: ["vulnerabilities", search],
    queryFn: async () => {
      const params = new URLSearchParams({
        page: String(pagination.pageIndex + 1),
        per_page: String(pagination.pageSize),
        sort: sorting[0].id, order: sorting[0].desc ? "desc" : "asc", q: search.q ?? "",
      });
      const r = await api.get<Vulnerability[]>(`/vulnerabilities?${params}`);
      const items = r.data ?? [];
      return { items, total: r.meta?.total ?? items.length };
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
      sortAccessor: (v) => findingTitle(v),
      accessor: (v) => (
        <Link
          to="/vulnerabilities/$vulnerabilityId"
          params={{ vulnerabilityId: v.id }}
          className="hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          <FindingTitle finding={v} findingId={v.finding_ids?.[0]} />{v.title !== findingTitle(v) && !v.title.includes(" ") && <span className="mt-1 block max-w-sm truncate font-mono text-xs text-muted-foreground">{v.title}</span>}
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
      sortable: false,
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
        description="Issues grouped by shared evidence across the repositories you can access. A vulnerability can combine findings from several tools; open one to see its supporting findings."
      />
      {q.error instanceof ApiError && q.error.status === 403 ? <PermissionState description="You do not have access to these vulnerabilities." /> : !search.q && !q.isLoading && !q.isError && (q.data?.total ?? 0) === 0 ? (
        <EmptyState
          icon={ShieldAlert}
          title="No vulnerabilities yet"
          description="Complete a scan to see vulnerabilities and their supporting evidence."
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
          searchPlaceholder="Search vulnerabilities..."
          emptyMessage="No vulnerabilities match"
          serverSide={{
            rowCount: q.data?.total ?? 0,
            pagination,
            onPaginationChange: (next) => void navigate({ search: { ...search, page: next.pageIndex + 1 } }),
            search: search.q ?? "",
            onSearchChange: (value) => void navigate({ search: { ...search, q: value || undefined, page: 1 } }),
            sorting,
            onSortingChange: (next) => void navigate({ search: { ...search, sort: next[0]?.id, order: next[0]?.desc ? "desc" : "asc", page: 1 } }),
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
