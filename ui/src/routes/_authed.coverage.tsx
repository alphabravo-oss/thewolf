import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { LayersIcon } from "lucide-react";
import { api } from "@/lib/api";
import { PageHeader, PageShell } from "@/components/ui/page";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";

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
          {t.display_name || t.name}
          <span className="ml-2 font-mono text-xs text-muted-foreground">{t.name}</span>
        </span>
      ),
    },
    {
      key: "category",
      header: "Category",
      sortAccessor: (t) => t.category ?? "",
      accessor: (t) => t.category ?? "—",
    },
    {
      key: "tier",
      header: "Tier",
      sortAccessor: (t) => t.integration_tier ?? "",
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

  return (
    <PageShell>
      <PageHeader
        title="Coverage"
        description={
          q.data?.honesty ??
          "Honest scanner matrix from tools.yaml. Wolf does not claim CPG depth from federated tools."
        }
      />
      {q.isError ? (
        <EmptyState
          icon={LayersIcon}
          title="Coverage unavailable"
          description="The scanner manifest could not be loaded."
        />
      ) : (
        <DataTable
          data={tools}
          columns={columns}
          keyExtractor={(t) => t.name}
          persistKey="coverage"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No tools in the scanner manifest"
        />
      )}
      {q.data?.tool_count != null ? (
        <p className="text-xs text-muted-foreground">
          {q.data.tool_count} tools · {q.data.source}
        </p>
      ) : null}
    </PageShell>
  );
}