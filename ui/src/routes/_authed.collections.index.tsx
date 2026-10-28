// Collections list + creation. The empty-state CTA and the header "Add
// collection" button both open the CollectionFormDialog.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PackageIcon } from "lucide-react";
import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import type { Collection } from "@/lib/types";
import { ListSkeleton } from "@/components/skeleton";
import { EmptyState, ErrorState, PermissionState } from "@/components/ui/empty-state";
import { PageHeader, PageShell } from "@/components/ui/page";
import { CollectionFormDialog } from "@/components/collection-form-dialog";
import { AddButton } from "@/components/ui/add-button";
import { Card, CardContent } from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { MetricCard } from "@/components/ui/metric-card";
import { RowActions } from "@/components/ui/row-actions";
import { formatRelativeTime } from "@/lib/utils";

export const Route = createFileRoute("/_authed/collections/")({
  component: CollectionsPage,
});

function CollectionsPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);

  const q = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      // API list endpoints return {data: T[], meta: {...}} — the api
      // wrapper strips the envelope so r.data IS the array.
      const r = await api.get<Collection[]>("/collections");
      return r.data ?? [];
    },
  });

  const m = useMutation({
    mutationFn: async (body: { name: string; description: string }) => {
      // POST returns {data: Collection} → r.data IS the Collection.
      const r = await api.post<Collection>("/collections", body);
      return r.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["collections", "all"] });
      setCreating(false);
    },
  });

  const rows = q.data ?? [];
  const repoTotal = rows.reduce((n, c) => n + (c.repo_count ?? 0), 0);
  const columns: Column<Collection>[] = [
    {
      key: "name",
      header: "Collection",
      sortAccessor: (c) => c.name,
      accessor: (c) => (
        <Link to="/collections/$collectionId" params={{ collectionId: c.id }} className="flex min-w-0 items-center gap-3" onClick={(e) => e.stopPropagation()}>
          <div className="grid size-8 shrink-0 place-items-center rounded-md bg-muted"><PackageIcon className="size-4 text-muted-foreground" aria-hidden /></div>
          <div className="min-w-0">
            <div className="truncate text-sm font-medium hover:underline">{c.name}</div>
            <div className="truncate text-xs text-muted-foreground">{c.description || "No description"}</div>
          </div>
        </Link>
      ),
    },
    { key: "repos", header: "Repos", align: "right", sortAccessor: (c) => c.repo_count ?? 0, accessor: (c) => <span className="font-mono tabular-nums">{c.repo_count ?? 0}</span> },
    { key: "updated", header: "Updated", sortAccessor: (c) => c.updated_at ?? "", accessor: (c) => <span className="text-xs text-muted-foreground">{c.updated_at ? formatRelativeTime(c.updated_at) : "—"}</span> },
    {
      key: "actions",
      header: "Actions",
      hideHeader: true,
      sortable: false,
      align: "right",
      width: "3rem",
      accessor: (c) => (
        <RowActions label={`Actions for ${c.name}`}>
          <DropdownMenuItem onSelect={() => void navigate({ to: "/collections/$collectionId", params: { collectionId: c.id } })}>Open collection</DropdownMenuItem>
        </RowActions>
      ),
    },
  ];

  return (
    <PageShell>
      <PageHeader
        title="Collections"
        description="Group repos for batch scanning and cross-repo metrics."
        actions={q.data && q.data.length > 0 ? <AddButton thing="collection" onClick={() => setCreating(true)} /> : undefined}
      />

      <CollectionFormDialog
        open={creating}
        onOpenChange={(open) => { setCreating(open); if (!open) m.reset(); }}
        onSubmit={(body) => m.mutate(body)}
        pending={m.isPending}
        error={m.error}
      />

      {q.data && q.data.length > 0 && (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <MetricCard title="Collections" value={rows.length} subtitle="Groups of repositories" />
          <MetricCard title="Repository links" value={repoTotal} subtitle="A repo can be in several collections" />
        </div>
      )}

      {q.error instanceof ApiError && q.error.status === 403 ? <PermissionState description="You do not have access to these collections." /> : q.isError ? <ErrorState title="Could not load collections" onRetry={() => void q.refetch()} /> : q.isLoading ? (
        <ListSkeleton rows={5} />
      ) : !q.data || q.data.length === 0 ? (
        <Card>
          <EmptyState
            icon={PackageIcon}
            title="No collections yet"
            description="Create your first collection to scan a set of repos together."
            cta={{ label: "Create collection", onClick: () => setCreating(true) }}
          />
        </Card>
      ) : (
        <Card>
          <CardContent className="p-4">
            <DataTable
              data={rows}
              columns={columns}
              keyExtractor={(c) => c.id}
              density="compact"
              searchPlaceholder="Search collections..."
              columnVisibilityControl={false}
              emptyMessage="No collections match your search"
              onRowClick={(c) => void navigate({ to: "/collections/$collectionId", params: { collectionId: c.id } })}
            />
          </CardContent>
        </Card>
      )}
    </PageShell>
  );
}
