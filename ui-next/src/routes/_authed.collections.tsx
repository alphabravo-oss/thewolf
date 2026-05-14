// Collections list. Stub for Phase 1 — the rich version (per-collection
// scan triggers, trends, AI prompts) ports in Phase 2.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { PackageIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Collection } from "@/lib/types";
import { ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";

export const Route = createFileRoute("/_authed/collections")({
  component: CollectionsPage,
});

function CollectionsPage() {
  const q = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      const r = await api.get<{ collections: Collection[] }>("/collections");
      return r.data.collections ?? [];
    },
  });
  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Collections</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Group repos for batch scanning and cross-repo metrics.
        </p>
      </header>
      {q.isLoading ? (
        <ListSkeleton rows={5} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={PackageIcon}
          title="No collections yet"
          description="Create your first collection to scan a set of repos together."
        />
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
