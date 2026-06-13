// Repos list. A repo is the unit a scan runs against — either a local
// filesystem path or a remote git URL. Repos can be referenced from
// multiple collections.
import { useState } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GitBranchIcon, GitForkIcon, HardDriveIcon, PlusIcon, ServerIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Repo } from "@/lib/types";
import { ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { AddRepoForm } from "@/components/add-repo-form";

export const Route = createFileRoute("/_authed/repos/")({
  component: ReposPage,
});

function ReposPage() {
  const [showAdd, setShowAdd] = useState(false);
  const q = useQuery({
    queryKey: ["repos", "all"],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos");
      return r.data ?? [];
    },
  });

  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <div className="flex items-start justify-between gap-4 mb-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Repositories</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Source-code targets — local paths, GitHub, remote git URLs, or SSH nodes.
          </p>
        </div>
        <button
          type="button"
          onClick={() => setShowAdd((v) => !v)}
          className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:opacity-90"
        >
          <PlusIcon className="size-4" />
          Add repo
        </button>
      </div>

      {showAdd && (
        <AddRepoForm
          onDone={(repoId) => {
            setShowAdd(false);
            // The component already invalidates the repos query; the list refetches.
            // If the parent wants to navigate to the new repo immediately, do it here.
            if (repoId) {
              // optional: navigate({ to: `/repos/${repoId}` })
            }
          }}
        />
      )}

      {q.isLoading ? (
        <ListSkeleton rows={5} />
      ) : !q.data || q.data.length === 0 ? (
        <EmptyState
          icon={GitForkIcon}
          title="No repositories yet"
          description="Add a local path, GitHub repo, or SSH-accessible tree to get started."
          cta={{ label: "Add repo", onClick: () => setShowAdd(true) }}
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
                  ) : r.source_type === "ssh" ? (
                    <ServerIcon className="size-4 text-muted-foreground" />
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
