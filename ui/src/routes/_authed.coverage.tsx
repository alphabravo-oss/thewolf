import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { PageHeader, PageShell } from "@/components/ui/page";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ErrorState, PermissionState } from "@/components/ui/empty-state";
import { Card, CardContent } from "@/components/ui/card";
import { MetricCard } from "@/components/ui/metric-card";

type CoverageTool = {
  name: string;
  display_name?: string;
  category?: string;
  integration_tier?: string;
  network_required?: boolean;
  pinned_version?: string;
  parser_contract?: boolean;
  depth?: string;
};

type Coverage = {
  source?: string;
  tool_count?: number;
  honesty?: string;
  tools?: CoverageTool[];
};

export const Route = createFileRoute("/_authed/coverage")({
  component: CoveragePage,
});

function CoveragePage() {
  const q = useQuery({
    queryKey: ["coverage"],
    queryFn: async () => (await api.get<Coverage>("/coverage")).data,
  });
  const tools = q.data?.tools ?? [];

  const columns: Column<CoverageTool>[] = [
    {
      key: "name",
      header: "Tool",
      sortAccessor: (t) => t.display_name || t.name,
      accessor: (t) => (
        <span>
          <span className="font-medium">{t.display_name || t.name}</span>
          <span className="ml-2 font-mono text-xs text-muted-foreground">{t.name}</span>
        </span>
      ),
    },
    {
      key: "category",
      header: "Category",
      sortAccessor: (t) => t.category ?? "",
      filter: { label: "Category" },
      accessor: (t) => t.category ?? "—",
    },
    {
      key: "tier",
      header: "Tier",
      sortAccessor: (t) => t.integration_tier ?? "",
      filter: { label: "Tier" },
      accessor: (t) => (
        <span className="font-mono text-xs">{t.integration_tier ?? "—"}</span>
      ),
    },
    {
      key: "version",
      header: "Pinned",
      sortAccessor: (t) => t.pinned_version ?? "",
      accessor: (t) => (
        <span className="font-mono text-xs">{t.pinned_version ?? "—"}</span>
      ),
    },
    {
      key: "depth",
      header: "Depth",
      sortAccessor: (t) => t.depth ?? "",
      accessor: (t) => t.depth ?? "federated-scanner",
    },
  ];

  const categories = new Set(tools.map((t) => t.category).filter(Boolean)).size;
  return (
    <PageShell>
      <PageHeader
        title="Scanner coverage"
        description={
          q.data?.honesty ??
          "Honest scanner matrix from tools.yaml. Wolf does not claim CPG depth from federated tools."
        }
      />
      {tools.length > 0 && (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <MetricCard title="Tools" value={q.data?.tool_count ?? tools.length} subtitle="In the scanner manifest" />
          <MetricCard title="Categories" value={categories} subtitle="SAST, SCA, secrets, IaC, …" />
          <MetricCard title="Parser contract" value={tools.filter((t) => t.parser_contract).length} subtitle="Output normalised by Wolf" />
          <MetricCard title="Needs network" value={tools.filter((t) => t.network_required).length} subtitle="Fetch data at scan time" />
        </div>
      )}
      {q.error instanceof ApiError && q.error.status === 403 ? <PermissionState description="You do not have access to scanner coverage." /> : q.isError ? (
        <ErrorState title="Coverage unavailable" description="The scanner manifest could not be loaded." onRetry={() => void q.refetch()} />
      ) : (
        <Card><CardContent className="p-4"><DataTable
          data={tools}
          columns={columns}
          keyExtractor={(t) => t.name}
          persistKey="coverage"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No tools in the scanner manifest"
        /></CardContent></Card>
      )}
      {q.data?.tool_count != null ? (
        <p className="text-xs text-muted-foreground">
          {q.data.tool_count} tools · {q.data.source}
        </p>
      ) : null}
    </PageShell>
  );
}
