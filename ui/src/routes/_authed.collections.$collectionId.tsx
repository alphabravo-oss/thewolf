import { EmptyState, ErrorState, PermissionState } from "@/components/ui/empty-state";
// Collection detail. Lets the user:
//   - Rename or clear description (inline edit)
//   - Delete the collection (modal; optional purge of scan records)
//   - Add a repo: pick an existing one or create a new one
//       (local path or remote git URL)
//   - Remove a repo from this collection (doesn't delete the repo)
//   - Trigger a scan against any single repo in the collection
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api, ApiError, isNotFound } from "@/lib/api";
import { useMe, canModify } from "@/lib/me";
import { parseToolList, type Collection, type Repo, type Scan } from "@/lib/types";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { formatRelativeTime } from "@/lib/utils";
import { CardSkeleton } from "@/components/skeleton";
import { AddRepoForm } from "@/components/add-repo-form";
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
      <div className="space-y-6">
        <CardSkeleton />
      </div>
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
    { key: "scans", header: "Scan history", align: "right", sortAccessor: (r) => r.scanCount, accessor: (r) => <Link to="/scans" search={{ repo_id: r.id }} className="tabular-nums hover:underline" aria-label={`View ${r.scanCount} scans for ${r.name}`}>{r.scanCount} scans</Link> },
    { key: "actions", header: "Actions", sortable: false, accessor: (r) => <div className="flex items-center gap-1"><Link to="/repos/$repoId" params={{ repoId: r.id }} search={{ collection_id: collectionId }} hash="findings" className="px-2 py-1 text-xs hover:underline">Findings</Link>{canModify(me.data, r.user_id) && <button type="button" aria-label={`Scan ${r.name}`} onClick={() => scan.launch({ repo_id: r.id, collection_id: collectionId, branch: r.default_branch || "main", profile: "standard" })} disabled={scan.busy} className="inline-flex h-8 items-center gap-1 rounded px-2 text-xs font-medium hover:bg-muted/50 disabled:opacity-50"><PlayIcon className="size-3.5" />Scan</button>}{canModify(me.data, c.user_id) && <button type="button" onClick={() => setRemoveTarget(r)} className="size-8 grid place-items-center rounded hover:bg-muted/50" aria-label={`Remove ${r.name} from collection`}><XIcon className="size-3.5" /></button>}</div> },
  ];


  return (
    <div className="space-y-6">
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
      <div className="flex items-start gap-3">
        <Link
          to="/collections"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div className="flex-1 min-w-0">
          {editing ? (
            <EditHeader
              initialName={c.name}
              initialDescription={c.description ?? ""}
              onSave={(body) => update.mutate(body)}
              onCancel={() => setEditing(false)}
              pending={update.isPending}
            />
          ) : (
            <>
              <h1 className="text-xl font-semibold">{c.name}</h1>
              {c.description ? (
                <p className="text-sm text-muted-foreground">{c.description}</p>
              ) : (
                <p className="text-sm text-muted-foreground italic">No description</p>
              )}
            </>
          )}
        </div>
        {!editing && canModify(me.data, c.user_id) && (
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => setEditing(true)}
              className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
              aria-label="Edit"
            >
              <PencilIcon className="size-4" />
            </button>
            <button
              type="button"
              onClick={() => setDeleteOpen(true)}
              className="size-9 grid place-items-center rounded-md hover:bg-destructive/10 text-destructive"
              aria-label="Delete"
            >
              <Trash2Icon className="size-4" />
            </button>
          </div>
        )}
      </div>

      <p className="text-sm text-muted-foreground">Work within this collection: open a repository to review its findings, or inspect a scan for the evidence from that run. Scan history follows the repository when it moves between collections.</p>
      <PostureCards collectionId={collectionId} />

      <section className="glass-card p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-medium">Repositories ({repos?.length ?? 0})</h2>
          {!adding && canModify(me.data, c.user_id) && (
            <button
              type="button"
              onClick={() => setAdding(true)}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90"
            >
              <PlusIcon className="size-4" />
              Add repo
            </button>
          )}
        </div>

        {adding && (
          <AddRepoForm
            collectionId={collectionId}
            onDone={() => {
              setAdding(false);
              // Toast already fired inside the component on success; nothing else needed.
            }}
          />
        )}

        <p className="mb-4 text-xs text-muted-foreground">Finding counts describe the latest scan, including partial results. Use Findings to review the repository’s current open issues.</p>
        <DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} searchPlaceholder="Search repositories in this collection..." pageSize={20} density="compact" persistKey={`collection-repos:${collectionId}`} emptyMessage="No repositories in this collection. Add a repository to get started." />
      </section>

      {scan.dialog}


    </div>
  );
}

// ── Edit header ───────────────────────────────────────────────

function EditHeader({
  initialName,
  initialDescription,
  onSave,
  onCancel,
  pending,
}: {
  initialName: string;
  initialDescription: string;
  onSave: (body: { name?: string; description?: string }) => void;
  onCancel: () => void;
  pending: boolean;
}) {
  const [name, setName] = useState(initialName);
  const [description, setDescription] = useState(initialDescription);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!name.trim()) return;
        onSave({
          name: name.trim() !== initialName ? name.trim() : undefined,
          description: description !== initialDescription ? description : undefined,
        });
      }}
      className="space-y-2"
    >
      <input
        type="text"
        value={name}
        onChange={(e) => setName(e.target.value)}
        required
        autoFocus
        placeholder="Collection name"
        className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-semibold"
      />
      <textarea
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        rows={2}
        placeholder="Description (optional)"
        className="w-full px-2 py-1.5 rounded-md bg-background border border-muted/40 text-sm resize-none"
      />
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={onCancel}
          className="h-8 px-3 rounded-md text-sm hover:bg-muted/40"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={pending || !name.trim()}
          className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
        >
          {pending ? "Saving…" : "Save"}
        </button>
      </div>
    </form>
  );
}

