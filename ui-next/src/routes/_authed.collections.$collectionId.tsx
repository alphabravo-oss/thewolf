// Collection detail. Lets the user:
//   - Rename or clear description (inline edit)
//   - Delete the collection (cascade — confirmation prompt)
//   - Add a repo: pick an existing one or create a new one
//       (local path or remote git URL)
//   - Remove a repo from this collection (doesn't delete the repo)
//   - Trigger a scan against any single repo in the collection
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { Collection, Repo, Scan } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";
import { AddRepoForm } from "@/components/add-repo-form";

// GetCollection returns a wrapped shape: { data: { collection, repos, scans } }
type CollectionDetail = {
  collection: Collection;
  repos: Repo[];
  scans: Scan[];
};

export const Route = createFileRoute("/_authed/collections/$collectionId")({
  component: CollectionDetailPage,
});

function CollectionDetailPage() {
  const { collectionId } = Route.useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [adding, setAdding] = useState(false);

  const q = useQuery({
    queryKey: ["collection", collectionId],
    queryFn: async () => {
      const r = await api.get<CollectionDetail>(`/collections/${collectionId}`);
      return r.data;
    },
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
    mutationFn: async () => {
      await api.delete(`/collections/${collectionId}`);
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

  if (q.isLoading || !q.data) {
    return (
      <div className="p-6 space-y-3 max-w-5xl">
        <CardSkeleton />
      </div>
    );
  }
  const { collection: c, repos, scans } = q.data;

  return (
    <div className="p-6 space-y-6 max-w-5xl">
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
        {!editing && (
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
              onClick={() => {
                if (
                  window.confirm(
                    `Delete collection "${c.name}"? This will also delete its scans (repos stay). This can't be undone.`,
                  )
                ) {
                  del.mutate();
                }
              }}
              className="size-9 grid place-items-center rounded-md hover:bg-destructive/10 text-destructive"
              aria-label="Delete"
            >
              <Trash2Icon className="size-4" />
            </button>
          </div>
        )}
      </div>

      <section className="glass-card p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-medium">Repositories ({repos?.length ?? 0})</h2>
          {!adding && (
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

        {repos && repos.length > 0 ? (
          <ul className="divide-y divide-border/30">
            {repos.map((r) => (
              <li key={r.id} className="py-2.5 flex items-center justify-between gap-3">
                <Link
                  to="/repos/$repoId"
                  params={{ repoId: r.id }}
                  className="flex-1 min-w-0"
                >
                  <div className="text-sm font-medium truncate">{r.name}</div>
                  <div className="text-xs text-muted-foreground truncate">
                    <span className="font-mono">{r.source_path}</span>
                    {r.default_branch && <> · {r.default_branch}</>}
                  </div>
                </Link>
                <button
                  type="button"
                  onClick={() => {
                    if (window.confirm(`Remove "${r.name}" from this collection? (The repo itself is not deleted.)`)) {
                      removeRepo.mutate(r.id);
                    }
                  }}
                  className="size-7 grid place-items-center rounded hover:bg-muted/50"
                  aria-label="Remove from collection"
                  title="Remove from collection"
                >
                  <XIcon className="size-3.5" />
                </button>
              </li>
            ))}
          </ul>
        ) : (
          !adding && (
            <p className="text-sm text-muted-foreground py-2">
              No repos yet. Click <strong>Add repo</strong> to attach one.
            </p>
          )
        )}
      </section>

      {scans && scans.length > 0 && (
        <section className="glass-card p-5">
          <h2 className="text-sm font-medium mb-3">Recent scans ({scans.length})</h2>
          <ul className="divide-y divide-border/30">
            {scans.slice(0, 10).map((s) => (
              <li key={s.id} className="py-2 text-sm flex items-center justify-between">
                <Link
                  to="/scans/$scanId"
                  params={{ scanId: s.id }}
                  className="font-mono truncate flex-1 min-w-0 hover:underline"
                >
                  {s.id.slice(0, 8)} · {s.status}
                </Link>
                <span className="text-xs text-muted-foreground">
                  {s.finding_count ?? 0} findings
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
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

