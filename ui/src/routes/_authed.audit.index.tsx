// Audit log — every mutating API request, with the actor and response status.
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { ScrollTextIcon } from "lucide-react";
import { api } from "@/lib/api";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { cn, formatRelativeTime } from "@/lib/utils";

type AuditEntry = {
  id: string;
  method: string;
  path: string;
  status_code: number;
  user_id: string;
  token_id?: string;
  resource_id?: string;
  action: string;
  created_at: string;
};

export const Route = createFileRoute("/_authed/audit/")({
  component: AuditPage,
});

// Stable empty array — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_ENTRIES: AuditEntry[] = [];

const columns: Column<AuditEntry>[] = [
  {
    key: "method",
    header: "Method",
    width: "7rem",
    sortAccessor: (r) => r.method,
    filter: { label: "Method" },
    accessor: (r) => <span className="font-mono text-xs">{r.method}</span>,
  },
  {
    key: "path",
    header: "Path",
    sortAccessor: (r) => r.path,
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
    header: "Actor",
    sortAccessor: (r) => r.token_id ?? r.user_id,
    accessor: (r) => (
      <span className="font-mono text-xs">
        {r.token_id ? `token:${r.token_id.slice(0, 8)}…` : `user:${r.user_id.slice(0, 8)}…`}
      </span>
    ),
  },
  {
    key: "when",
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
  const q = useQuery({
    queryKey: ["audit-log"],
    queryFn: async () => {
      const { data } = await api.get<{ data: AuditEntry[] }>("/audit-log?limit=100");
      return data.data ?? [];
    },
  });

  // A 403 here is expected for non-admins, and reads better as a permission
  // state than as a generic table error.
  const forbidden = q.isError && /403|forbidden/i.test((q.error as Error).message);
  if (forbidden) {
    return (
      <PageShell>
        <PageHeader title="Audit log" />
        <EmptyState
          icon={ScrollTextIcon}
          title="Admin only"
          description="The audit log is only readable by users with the admin scope."
        />
      </PageShell>
    );
  }

  const rows = q.data ?? EMPTY_ENTRIES;

  return (
    <PageShell>
      <PageHeader
        title="Audit log"
        description="Every mutating API request — POST, PUT, DELETE — with the actor, the resource, and the response status. Most recent 100."
      />
      {!q.isLoading && !q.isError && rows.length === 0 ? (
        <EmptyState
          icon={ScrollTextIcon}
          title="No audit entries yet"
          description="Mutating API requests will land here once anyone changes anything."
        />
      ) : (
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(r) => r.id}
          persistKey="audit"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          errorMessage={q.isError ? (q.error as Error).message : undefined}
          onRetry={() => void q.refetch()}
          searchPlaceholder="Search paths, actors..."
          emptyMessage="No entries match your filters"
        />
      )}
    </PageShell>
  );
}

// Response-status chip, coloured on the shared status scale rather than raw
// Tailwind palette colours.
function StatusPill({ code }: { code: number }) {
  const tone =
    code >= 500
      ? "bg-status-error/10 text-status-error"
      : code >= 400
        ? "bg-status-warning/10 text-status-warning"
        : code >= 300
          ? "bg-status-info/10 text-status-info"
          : "bg-status-success/10 text-status-success";
  return (
    <span className={cn("inline-flex h-5 items-center rounded px-1.5 text-xs font-medium tabular-nums", tone)}>
      {code}
    </span>
  );
}
