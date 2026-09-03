import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Building2 } from "lucide-react";
import { api } from "@/lib/api";
import { PageHeader, PageShell } from "@/components/ui/page";

export const Route = createFileRoute("/_authed/enterprise/$")({
  component: EnterpriseModulePage,
});

function EnterpriseModulePage() {
  const { _splat } = Route.useParams();
  const path = `/enterprise/${_splat ?? ""}`;
  const q = useQuery({
    queryKey: ["enterprise", path],
    queryFn: async () => (await api.get<Record<string, unknown>>(path)).data,
    enabled: !!_splat,
  });
  const title =
    typeof q.data?.module === "string"
      ? q.data.module
      : (_splat ?? "Enterprise").replace(/-/g, " ");

  return (
    <PageShell>
      <PageHeader
        eyebrow="Enterprise"
        title={
          <span className="inline-flex items-center gap-2">
            <Building2 className="h-5 w-5" />
            {title}
          </span>
        }
        description="Overlay module status. Community binaries have no routes here."
      />
      {q.isError ? (
        <p className="text-sm text-muted-foreground">
          This module is not available in this edition or is not licensed.
        </p>
      ) : (
        <pre className="glass-card p-4 text-xs overflow-auto">
          {q.isLoading ? "Loading…" : JSON.stringify(q.data ?? {}, null, 2)}
        </pre>
      )}
    </PageShell>
  );
}
