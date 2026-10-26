// Audit log — every mutating API request, with the actor and response status.
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { api, ApiError } from "@/lib/api";
import { Input } from "@/components/ui/input";
import { PermissionState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { StatusBadge } from "@/components/ui/status-badge";
import { formatRelativeTime } from "@/lib/utils";

type AuditEntry = {
  id: string;
  method: string;
  path: string;
  status_code: number;
  user_id: string;
  token_id?: string;
  resource_id?: string;
  action: string;
  event_type?: string;
  category?: string;
  severity?: string;
  ip?: string;
  created_at: string;
};

type AuditSearch = { q?: string; method?: string; category?: string; severity?: string; event?: string; actor?: string; ip?: string; from?: string; before?: string; page?: number; sort?: "time" | "status"; order?: "asc" | "desc" };
const filterKeys = ["method", "category", "severity", "event", "actor", "ip", "from", "before"] as const;

export const Route = createFileRoute("/_authed/audit/")({
  validateSearch: (s): AuditSearch => ({
    ...Object.fromEntries(["q", ...filterKeys].map((key) => [key, typeof s[key] === "string" ? s[key] : undefined])),
    page: Number.isSafeInteger(Number(s.page)) && Number(s.page) > 0 ? Number(s.page) : 1,
    sort: s.sort === "status" ? "status" : "time",
    order: s.order === "asc" ? "asc" : "desc",
  }),
  component: AuditPage,
});

// Stable empty array — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_ENTRIES: AuditEntry[] = [];

const columns: Column<AuditEntry>[] = [
  {
    key: "method",
    sortable: false,
    header: "Method",
    width: "7rem",
    accessor: (r) => <span className="font-mono text-xs">{r.method}</span>,
  },
  {
    key: "path",
    sortable: false,
    header: "Path",
    accessor: (r) => (
      <span className="block max-w-[40ch] truncate font-mono text-xs" title={r.path}>
        {r.path}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    width: "6rem",
    sortAccessor: (r) => r.status_code,
    accessor: (r) => <StatusPill code={r.status_code} />,
  },
  {
    key: "actor",
    sortable: false,
    header: "Actor",
    accessor: (r) => (
      <span className="font-mono text-xs">
        {r.token_id ? `token:${r.token_id.slice(0, 8)}…` : `user:${r.user_id.slice(0, 8)}…`}
      </span>
    ),
  },
  { key: "event", sortable: false, header: "Event", accessor: (r) => r.event_type || r.action },
  { key: "category", sortable: false, header: "Category", accessor: (r) => r.category || "—" },
  { key: "severity", sortable: false, header: "Severity", accessor: (r) => r.severity || "—" },
  { key: "ip", sortable: false, header: "Client IP", accessor: (r) => r.ip || "—" },
  {
    key: "time",
    header: "When",
    align: "right",
    sortAccessor: (r) => r.created_at,
    accessor: (r) => (
      <span className="text-xs text-muted-foreground" title={new Date(r.created_at).toLocaleString()}>
        {formatRelativeTime(r.created_at)}
      </span>
    ),
  },
];

function AuditPage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const update = (next: Partial<AuditSearch>) => void navigate({ search: { ...search, page: 1, ...next } });
  const pagination = { pageIndex: (search.page ?? 1) - 1, pageSize: 50 };
  const q = useQuery({
    queryKey: ["audit-log", search],
    queryFn: async () => {
      const params = new URLSearchParams({ page: String(search.page ?? 1), per_page: String(pagination.pageSize) });
      for (const key of ["q", "sort", "order", ...filterKeys] as const) {
        if (search[key]) params.set(key, search[key]);
      }
      const result = await api.get<AuditEntry[]>(`/audit-log?${params}`);
      return { items: result.data ?? EMPTY_ENTRIES, total: result.meta?.total ?? 0 };
    },
  });

  // A 403 here is expected for non-admins, and reads better as a permission
  // state than as a generic table error.
  const forbidden = q.error instanceof ApiError && q.error.status === 403;
  if (forbidden) {
    return (
      <PageShell>
        <PageHeader title="Audit log" />
        <PermissionState
          title="Admin only"
          permission="admin"
          description="The audit log is only readable by users with the admin scope."
        />
      </PageShell>
    );
  }

  const rows = q.data?.items ?? EMPTY_ENTRIES;

  return (
    <PageShell>
      <PageHeader
        title="Audit log"
        description="Search recorded events across the full audit history. Dates use UTC; Before excludes that day."
      />
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 mb-4">
        {filterKeys.map((key) => (
          <label key={key} className="text-sm space-y-1">
            <span>{({ method: "Method", category: "Category", severity: "Severity", event: "Event", actor: "Actor ID", ip: "Client IP", from: "From (UTC)", before: "Before (UTC)" })[key]}</span>
            <Input type={key === "from" || key === "before" ? "date" : "text"} value={search[key] ?? ""} onChange={(e) => update({ [key]: e.target.value || undefined })} />
          </label>
        ))}
      </div>
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(r) => r.id}
          persistKey="audit"
          density="compact"
          pageSize={pagination.pageSize}
          serverSide={{
            rowCount: q.data?.total ?? 0,
            pagination,
            onPaginationChange: (next) => update({ page: next.pageIndex + 1 }),
            search: search.q ?? "",
            onSearchChange: (q) => update({ q: q || undefined }),
            sorting: [{ id: search.sort ?? "time", desc: search.order !== "asc" }],
            onSortingChange: ([sort]) => update({ sort: sort?.id === "status" ? "status" : "time", order: sort?.desc ? "desc" : "asc" }),
          }}
          loading={q.isLoading}
          isError={q.isError}
          errorMessage={q.isError ? (q.error as Error).message : undefined}
          onRetry={() => void q.refetch()}
          searchPlaceholder="Search events, paths, actors, IPs..."
          emptyMessage="No audit entries match your filters"
        />
    </PageShell>
  );
}

// Response-status chip, coloured on the shared status scale rather than raw
// Tailwind palette colours.
function StatusPill({ code }: { code: number }) {
  const status = code >= 500 ? "failed" : code >= 400 ? "warning" : code >= 300 ? "redirected" : "success";
  return <StatusBadge status={status} label={String(code)} showDot={false} size="sm" className="tabular-nums" />;
}
