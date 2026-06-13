// Collections list + creation. The empty-state CTA opens an inline form;
// the header has a "New collection" button on non-empty list views.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PackageIcon, PlusIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { api } from "@/lib/api";
import type { Collection } from "@/lib/types";
import { ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";

export const Route = createFileRoute("/_authed/collections/")({
  component: CollectionsPage,
});

function CollectionsPage() {
  const qc = useQueryClient();
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

  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <header className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Collections</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Group repos for batch scanning and cross-repo metrics.
          </p>
        </div>
        {!creating && q.data && q.data.length > 0 && (
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="inline-flex items-center gap-1.5 px-3 h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition"
          >
            <PlusIcon className="size-4" />
            New collection
          </button>
        )}
      </header>

      {creating && (
        <CreateCollectionForm
          onCancel={() => setCreating(false)}
          onSubmit={(body) => m.mutate(body)}
          pending={m.isPending}
          error={m.error}
        />
      )}

      {q.isLoading ? (
        <ListSkeleton rows={5} />
      ) : !q.data || q.data.length === 0 ? (
        !creating && (
          <EmptyState
            icon={PackageIcon}
            title="No collections yet"
            description="Create your first collection to scan a set of repos together."
            cta={{ label: "Create collection", onClick: () => setCreating(true) }}
          />
        )
      ) : (
        <ul className="space-y-2">
          {q.data.map((c) => (
            <li key={c.id}>
              <Link
                to="/collections/$collectionId"
                params={{ collectionId: c.id }}
                className="glass-card px-4 py-3 flex items-center gap-3 hover:bg-muted/30 transition"
              >
                <div className="size-8 rounded-md bg-muted/40 grid place-items-center">
                  <PackageIcon className="size-4 text-muted-foreground" />
                </div>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium truncate">{c.name}</div>
                  <div className="text-xs text-muted-foreground truncate">
                    {c.repo_count ?? 0} repos · {c.description || "—"}
                  </div>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function CreateCollectionForm({
  onCancel,
  onSubmit,
  pending,
  error,
}: {
  onCancel: () => void;
  onSubmit: (body: { name: string; description: string }) => void;
  pending: boolean;
  error: unknown;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!name.trim()) return;
        onSubmit({ name: name.trim(), description: description.trim() });
      }}
      className="glass-card p-4 space-y-3 max-w-2xl"
    >
      <div className="flex items-center justify-between">
        <div className="text-sm font-semibold">New collection</div>
        <button
          type="button"
          onClick={onCancel}
          className="size-7 grid place-items-center rounded hover:bg-muted/40"
          aria-label="Cancel"
        >
          <XIcon className="size-4" />
        </button>
      </div>

      <label className="block">
        <span className="text-xs text-muted-foreground">Name *</span>
        <input
          type="text"
          required
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Backend services"
          className="mt-1 w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm focus:outline-none focus:ring-1 focus:ring-primary"
        />
      </label>

      <label className="block">
        <span className="text-xs text-muted-foreground">Description</span>
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Optional — what repos belong here?"
          rows={2}
          className="mt-1 w-full px-2 py-1.5 rounded-md bg-background border border-muted/40 text-sm focus:outline-none focus:ring-1 focus:ring-primary resize-none"
        />
      </label>

      {error ? (
        <div className="text-xs text-red-500">
          {error instanceof Error ? error.message : "Failed to create collection"}
        </div>
      ) : null}

      <div className="flex items-center gap-2 justify-end">
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
          className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed transition"
        >
          {pending ? "Creating…" : "Create"}
        </button>
      </div>
    </form>
  );
}
