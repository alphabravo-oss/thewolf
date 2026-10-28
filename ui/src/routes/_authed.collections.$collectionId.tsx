import { EmptyState, ErrorState, PermissionState } from "@/components/ui/empty-state";
// Collection detail. Lets the user:
//   - Rename or clear description (edit dialog)
//   - Delete the collection (modal; optional purge of scan records)
//   - Add a repo: pick an existing one or create a new one
//       (local path or remote git URL)
//   - Remove a repo from this collection (doesn't delete the repo)
//   - Trigger a scan against any single repo in the collection
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PencilIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api, ApiError, isNotFound } from "@/lib/api";
import { useMe, canModify } from "@/lib/me";
import { parseToolList, type Collection, type Repo, type Scan } from "@/lib/types";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { formatRelativeTime } from "@/lib/utils";
import { CardSkeleton } from "@/components/skeleton";
import { AddRepoDialog } from "@/components/add-repo-form";
import { CollectionFormDialog } from "@/components/collection-form-dialog";
import { AddButton } from "@/components/ui/add-button";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { PageHeader, PageShell } from "@/components/ui/page";
import { RowActions } from "@/components/ui/row-actions";
import { PostureCards } from "@/components/fleet/posture-cards";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DeleteWithRecordsDialog } from "@/components/delete-with-records-dialog";
import { useScanWithPreflight } from "@/components/scan-preflight";

// GetCollection returns a wrapped shape: { data: { collection, repos, scans } }
type CollectionDetail = {
  collection: Collection;
  repos: Repo[];
  scans: Scan[];
};

type RepositoryRow = Repo & { latest?: Scan; scanCount: number };

export const Route = createFileRoute("/_authed/collections/$collectionId")({
  component: CollectionDetailPage,
});

function CollectionDetailPage() {
  const { collectionId } = Route.useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const [editing, setEditing] = useState(false);
  const [adding, setAdding] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [removeTarget, setRemoveTarget] = useState<Repo | null>(null);
  const scan = useScanWithPreflight();

  const q = useQuery({
    queryKey: ["collection", collectionId],
    queryFn: async () => {
      const r = await api.get<CollectionDetail>(`/collections/${collectionId}`);
      return r.data;
    },
    refetchInterval: 30_000,
  });

  const update = useMutation({
    mutationFn: async (body: { name?: string; description?: string }) => {
      const r = await api.put<Collection>(`/collections/${collectionId}`, body);
      return r.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["collection", collectionId] });
      qc.invalidateQueries({ queryKey: ["collections", "all"] });
      setEditing(false);
      toast.success("Collection updated");
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Update failed");
    },
  });

  const del = useMutation({
    mutationFn: async (purge: boolean) => {
      await api.delete(
        `/collections/${collectionId}${purge ? "?purge=true" : ""}`,
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["collections", "all"] });
      toast.success("Collection deleted");
      navigate({ to: "/collections" });
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    },
  });

  const removeRepo = useMutation({
    mutationFn: async (repoId: string) => {
      await api.delete(`/collections/${collectionId}/repos/${repoId}`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["collection", collectionId] });
      toast.success("Removed from collection");
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Remove failed");
    },
  });


  if (q.error instanceof ApiError && q.error.status === 403) return <PermissionState description="You do not have access to this collection." />;
  if (isNotFound(q.error)) return <EmptyState title="Collection not found" />;
  if (q.isError) return <ErrorState title="Could not load collection" actionLabel="Retry collection" onRetry={() => void q.refetch()} />;
  if (q.isLoading) {
    return (
      <PageShell>
        <CardSkeleton />
      </PageShell>
    );
  }
  if (!q.data) return <EmptyState title="Collection not found" />;
  const { collection: c, repos, scans } = q.data;
  const history = new Map<string, Scan[]>();
  for (const s of scans ?? []) {
    if (s.origin_scan_id) continue;
    const items = history.get(s.repo_id) ?? [];
    items.push(s);
    history.set(s.repo_id, items);
  }
  for (const items of history.values()) items.sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id));
  const rows: RepositoryRow[] = (repos ?? []).map((repo) => ({ ...repo, latest: history.get(repo.id)?.[0], scanCount: history.get(repo.id)?.length ?? 0 }));
  const columns: Column<RepositoryRow>[] = [
    { key: "name", header: "Repository", sortAccessor: (r) => r.name, accessor: (r) => <div className="min-w-40 max-w-64 space-y-1"><Link to="/repos/$repoId" params={{ repoId: r.id }} search={{ collection_id: collectionId }} className="block truncate font-medium hover:underline" title={r.name}>{r.name}</Link><p className="truncate text-xs text-muted-foreground" title={`${r.source_type} · ${r.source_path}`}>{r.source_type} · {r.source_path}</p></div> },
    { key: "branch", header: "Default branch", sortAccessor: (r) => r.default_branch, accessor: (r) => <span className="font-mono text-xs">{r.default_branch || "—"}</span> },
    { key: "latest", header: "Latest scan", sortAccessor: (r) => r.latest?.created_at ?? "", accessor: (r) => r.latest ? <Link to="/scans/$scanId" params={{ scanId: r.latest.id }} className="block space-y-1.5 hover:underline"><ScanStatusPill status={r.latest.status} scan={r.latest} size="sm" /><p className="whitespace-nowrap text-xs text-muted-foreground">{r.latest.branch || "Default source"} · <time dateTime={r.latest.created_at} title={new Date(r.latest.created_at).toLocaleString()}>{formatRelativeTime(r.latest.created_at)}</time></p></Link> : <span className="whitespace-nowrap text-sm text-muted-foreground">Never scanned</span> },
    { key: "coverage", header: "Tools completed", sortable: false, accessor: (r) => r.latest ? <div className="text-xs tabular-nums">{parseToolList(r.latest.tools_completed).length} / {parseToolList(r.latest.tools_selected).length}{parseToolList(r.latest.tools_failed).length > 0 && <p className="text-status-error">{parseToolList(r.latest.tools_failed).length} failed</p>}</div> : <span className="text-muted-foreground">—</span> },
    { key: "findings", header: "Latest findings", align: "right", sortAccessor: (r) => r.latest?.finding_count ?? -1, accessor: (r) => r.latest ? <Link to="/scans/$scanId" params={{ scanId: r.latest.id }} className="tabular-nums hover:underline">{r.latest.finding_count.toLocaleString()}</Link> : <span className="text-muted-foreground">—</span> },
    { key: "scans", header: "Scan history", align: "right", sortAccessor: (r) => r.scanCount, accessor: (r) => <Link to="/scans" search={{ repo_id: r.id }} className="tabular-nums hover:underline" aria-label={`View ${r.scanCount} scans for ${r.name}`}>{r.scanCount} {r.scanCount === 1 ? "scan" : "scans"}</Link> },
    { key: "actions", header: "Actions", hideHeader: true, sortable: false, align: "right", width: "3rem", accessor: (r) => <RowActions label={`Actions for ${r.name}`}>
      <DropdownMenuItem asChild><Link to="/repos/$repoId" params={{ repoId: r.id }} search={{ collection_id: collectionId }} hash="findings">Findings</Link></DropdownMenuItem>
      {canModify(me.data, r.user_id) && <DropdownMenuItem disabled={scan.busy} onSelect={() => scan.launch({ repo_id: r.id, collection_id: collectionId, branch: r.default_branch || "main", profile: "standard" })}>Scan</DropdownMenuItem>}
      {canModify(me.data, c.user_id) && <DropdownMenuItem onSelect={() => setRemoveTarget(r)}>Remove from collection</DropdownMenuItem>}
    </RowActions> },
  ];


  return (
    <PageShell>
      <DeleteWithRecordsDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        kind="collection"
        name={c.name}
        recordCount={scans?.filter((s) => s.collection_id === collectionId).length}
        pending={del.isPending}
        onConfirm={(purge) => del.mutate(purge)}
      />
      <ConfirmDialog
        open={!!removeTarget}
        onOpenChange={(o) => !o && setRemoveTarget(null)}
        title={`Remove “${removeTarget?.name ?? ""}” from this collection?`}
        description="The repo itself is not deleted — only the membership in this collection."
        confirmLabel="Remove"
        pending={removeRepo.isPending}
        onConfirm={() => {
          if (removeTarget) {
            removeRepo.mutate(removeTarget.id, {
              onSuccess: () => setRemoveTarget(null),
            });
          }
        }}
      />
      <CollectionFormDialog
        open={editing}
        onOpenChange={(open) => { setEditing(open); if (!open) update.reset(); }}
        initial={{ name: c.name, description: c.description ?? "" }}
        onSubmit={(body) => update.mutate({
          name: body.name !== c.name ? body.name : undefined,
          description: body.description !== (c.description ?? "") ? body.description : undefined,
        })}
        pending={update.isPending}
      />
      <AddRepoDialog open={adding} collectionId={collectionId} onDone={() => setAdding(false)} />
      <PageHeader
        breadcrumbs={[{ label: "Collections", link: { to: "/collections" } }, { label: c.name }]}
        title={c.name}
        description={c.description || <span className="italic">No description</span>}
        actions={canModify(me.data, c.user_id) ? (
          <>
            <Button variant="outline" size="sm" onClick={() => setEditing(true)}><PencilIcon aria-hidden />Edit</Button>
            <Button variant="outline" size="sm" className="text-destructive-text" onClick={() => setDeleteOpen(true)}><Trash2Icon aria-hidden />Delete</Button>
          </>
        ) : undefined}
      />

      <p className="text-sm text-muted-foreground">Work within this collection: open a repository to review its findings, or inspect a scan for the evidence from that run. Scan history follows the repository when it moves between collections.</p>
      <PostureCards collectionId={collectionId} />

      <Card>
        <CardHeader>
          <CardTitle>Repositories ({repos?.length ?? 0})</CardTitle>
          {canModify(me.data, c.user_id) && <AddButton thing="repo" onClick={() => setAdding(true)} />}
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs text-muted-foreground">Finding counts describe the latest scan, including partial results. Use Findings to review the repository’s current open issues.</p>
          <DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} searchPlaceholder="Search repositories in this collection..." pageSize={20} density="compact" persistKey={`collection-repos:${collectionId}`} emptyMessage="No repositories in this collection. Add a repository to get started." />
        </CardContent>
      </Card>

      {scan.dialog}
    </PageShell>
  );
}
