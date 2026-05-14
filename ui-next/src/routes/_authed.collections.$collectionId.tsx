// Collection detail — Phase 1 stub. Shows name + repo list + recent scans.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Collection } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";

export const Route = createFileRoute("/_authed/collections/$collectionId")({
  component: CollectionDetailPage,
});

function CollectionDetailPage() {
  const { collectionId } = Route.useParams();
  const q = useQuery({
    queryKey: ["collection", collectionId],
    queryFn: async () => {
      const r = await api.get<Collection>(`/collections/${collectionId}`);
      return r.data;
    },
  });

  if (q.isLoading || !q.data) {
    return (
      <div className="p-6 space-y-3 max-w-5xl">
        <CardSkeleton />
      </div>
    );
  }
  const c = q.data;

  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-center gap-3">
        <Link
          to="/collections"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div>
          <h1 className="text-xl font-semibold">{c.name}</h1>
          {c.description && (
            <p className="text-sm text-muted-foreground">{c.description}</p>
          )}
        </div>
      </div>

      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">Repositories</h2>
        {c.repos && c.repos.length > 0 ? (
          <ul className="space-y-1">
            {c.repos.map((r) => (
              <li
                key={r.id}
                className="text-sm flex justify-between border-b border-border/30 py-2 last:border-0"
              >
                <span className="font-mono">{r.name}</span>
                <span className="text-muted-foreground">{r.default_branch}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No repos in this collection.</p>
        )}
      </section>
    </div>
  );
}
