// Dashboard panel: repos that want an operator's attention right now.
// Renders through the shared DataTable so sorting, search, and the empty/error
// states match every other list in the console.
import { Link, useNavigate } from "@tanstack/react-router";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/status-badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { useNeedsAttention, type NeedsAttentionRow } from "@/lib/fleet";

const reasonLabel: Record<NeedsAttentionRow["reason"], string> = {
  gate_failing: "Gate failing",
  stale: "Stale scan",
  new_high: "New high",
  scan_failed: "Scan failed",
};

// Each reason maps onto the shared status vocabulary so its pill picks up the
// same tone the rest of the app uses for that severity of problem.
const reasonStatus: Record<NeedsAttentionRow["reason"], string> = {
  gate_failing: "failed",
  scan_failed: "failed",
  new_high: "high",
  stale: "stale",
};

// Stable empty array — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_ROWS: NeedsAttentionRow[] = [];

export function NeedsAttention() {
  const navigate = useNavigate();
  const q = useNeedsAttention();
  const rows = q.data ?? EMPTY_ROWS;

  const columns: Column<NeedsAttentionRow>[] = [
    {
      key: "name",
      header: "Repo",
      sortAccessor: (row) => row.name,
      accessor: (row) => (
        <Link
          to="/repos/$repoId"
          params={{ repoId: row.repo_id }}
          className="font-medium hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          {row.name}
        </Link>
      ),
    },
    {
      key: "reason",
      header: "Reason",
      sortAccessor: (row) => reasonLabel[row.reason],
      filter: { label: "Reason" },
      accessor: (row) => (
        <StatusBadge
          status={reasonStatus[row.reason]}
          label={reasonLabel[row.reason]}
          size="sm"
          showDot={false}
        />
      ),
    },
    {
      key: "detail",
      header: "Detail",
      sortAccessor: (row) => row.detail,
      accessor: (row) => <span className="text-xs text-muted-foreground">{row.detail}</span>,
    },
  ];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Needs attention</CardTitle>
      </CardHeader>
      <CardContent>
        {!q.isLoading && !q.isError && rows.length === 0 ? (
          <div className="text-sm text-muted-foreground">All clear.</div>
        ) : (
          <DataTable
            data={rows}
            columns={columns}
            keyExtractor={(row) => row.repo_id}
            density="compact"
            searchable={false}
            pageSize={8}
            loading={q.isLoading}
            isError={q.isError}
            onRetry={() => void q.refetch()}
            emptyMessage="All clear"
            onRowClick={(row) =>
              navigate({ to: "/repos/$repoId", params: { repoId: row.repo_id } })
            }
          />
        )}
      </CardContent>
    </Card>
  );
}
