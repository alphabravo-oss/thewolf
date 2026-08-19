// Dashboard panel: the rules/CVEs with the widest blast radius across the fleet.
import { Link } from "@tanstack/react-router";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { useTopVulnerableRules, type AggregateRow } from "@/lib/fleet";

// Stable empty array — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_ROWS: AggregateRow[] = [];

const columns: Column<AggregateRow>[] = [
  {
    key: "key",
    header: "Rule",
    sortAccessor: (row) => row.key,
    accessor: (row) => (
      <Link
        to="/findings"
        search={{ view: "rule", q: row.key }}
        className="font-mono text-xs hover:underline"
      >
        {row.key}
      </Link>
    ),
  },
  {
    key: "repos",
    header: "Repos",
    align: "right",
    sortAccessor: (row) => row.repos,
    accessor: (row) => <span className="tabular-nums">{row.repos}</span>,
  },
  {
    key: "findings",
    header: "Findings",
    align: "right",
    sortAccessor: (row) => row.findings,
    accessor: (row) => <span className="tabular-nums">{row.findings}</span>,
  },
];

export function TopComponents() {
  const q = useTopVulnerableRules(10);
  const rows = q.data ?? EMPTY_ROWS;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Top vulnerable components</CardTitle>
      </CardHeader>
      <CardContent>
        {!q.isLoading && !q.isError && rows.length === 0 ? (
          <div className="text-sm text-muted-foreground">No findings yet.</div>
        ) : (
          <DataTable
            data={rows}
            columns={columns}
            keyExtractor={(row) => row.key}
            density="compact"
            searchable={false}
            pageSize={10}
            loading={q.isLoading}
            isError={q.isError}
            onRetry={() => void q.refetch()}
            emptyMessage="No findings yet"
          />
        )}
      </CardContent>
    </Card>
  );
}
