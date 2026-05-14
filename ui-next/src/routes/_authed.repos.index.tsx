// Repos list. A repo is the unit a scan runs against — either a local
// filesystem path or a remote git URL. Repos can be referenced from
// multiple collections.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GitBranchIcon, GitForkIcon, HardDriveIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Repo } from "@/lib/types";
import { ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";

export const Route = createFileRoute("/_authed/repos/")({
  component: ReposPage,
});

function ReposPage() {
  const q = useQuery({
    queryKey: ["repos", "all"],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos");
      return r.data ?? [];
    },
  });

  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Repositories</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Source-code targets — local filesystem paths or remote git URLs.
          Add new ones from a collection's <strong>Add repo</strong> form.
        </p>
      </header>

      {q.isLoading ? (
        <ListSkeleton rows={5} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={GitForkIcon}
          title="No repositories yet"
          description="Open a collection and use Add repo to attach a local path or remote git URL."
          cta={{ label: "Go to Collections", to: "/collections" }}
        />
      ) : (
        <ul className="space-y-2">
          {q.data.map((r) => (
            <li key={r.id}>
              <Link
                to="/repos/$repoId"
                params={{ repoId: r.id }}
                className="glass-card px-4 py-3 flex items-center gap-3 hover:bg-muted/30 transition"
              >
                <div className="size-8 rounded-md bg-muted/40 grid place-items-center">
                  {r.source_type === "local" ? (
                    <HardDriveIcon className="size-4 text-muted-foreground" />
                  ) : (
                    <GitBranchIcon className="size-4 text-muted-foreground" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium truncate">{r.name}</div>
                  <div className="text-xs text-muted-foreground truncate font-mono">
                    {r.source_path}
                  </div>
                </div>
                <div className="text-xs text-muted-foreground">
                  {r.default_branch || "main"}
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
