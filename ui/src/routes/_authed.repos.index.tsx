// Repos list. A repo is the unit a scan runs against — either a local
// filesystem path or a remote git URL. Repos can be referenced from
// multiple collections.
import { useMemo, useState } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GitBranchIcon, GitForkIcon, HardDriveIcon, PlusIcon, ServerIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Collection, Repo, Scan } from "@/lib/types";
import { ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { AddRepoForm } from "@/components/add-repo-form";
import {
  FilterBar,
  type ReposFilters,
  type RepoSourceFilter,
  type RepoStatusFilter,
} from "@/components/repos/filter-bar";

type GroupBy = "none" | "source_type" | "collection" | "language";

interface ReposSearch {
  source?: RepoSourceFilter[];
  collection?: string[];
  status?: RepoStatusFilter[];
  group?: GroupBy;
}

const VALID_SOURCES: RepoSourceFilter[] = ["local", "github", "ssh", "git"];
const VALID_STATUSES: RepoStatusFilter[] = [
  "clean",
  "open-high",
  "open-critical",
  "none",
  "failed",
];
const VALID_GROUPS: GroupBy[] = ["none", "source_type", "collection", "language"];

function parseStringArray<T extends string>(raw: unknown, allowed: readonly T[]): T[] {
  const out: T[] = [];
  const push = (s: unknown) => {
    if (typeof s !== "string") return;
    for (const part of s.split(",")) {
      const v = part.trim();
      if (v && (allowed as readonly string[]).includes(v) && !out.includes(v as T)) {
        out.push(v as T);
      }
    }
  };
  if (Array.isArray(raw)) raw.forEach(push);
  else push(raw);
  return out;
}

function parseFreeStringArray(raw: unknown): string[] {
  const out: string[] = [];
  const push = (s: unknown) => {
    if (typeof s !== "string") return;
    for (const part of s.split(",")) {
      const v = part.trim();
      if (v && !out.includes(v)) out.push(v);
    }
  };
  if (Array.isArray(raw)) raw.forEach(push);
  else push(raw);
  return out;
}

export const Route = createFileRoute("/_authed/repos/")({
  validateSearch: (s: Record<string, unknown>): ReposSearch => {
    const group =
      typeof s.group === "string" && (VALID_GROUPS as readonly string[]).includes(s.group)
        ? (s.group as GroupBy)
        : undefined;
    return {
      source: parseStringArray(s.source, VALID_SOURCES),
      collection: parseFreeStringArray(s.collection),
      status: parseStringArray(s.status, VALID_STATUSES),
      group,
    };
  },
  component: ReposPage,
});

function ReposPage() {
  const [showAdd, setShowAdd] = useState(false);
  const search = Route.useSearch();
  const navigate = useNavigate();

  const filters: ReposFilters = {
    source: search.source ?? [],
    collection: search.collection ?? [],
    status: search.status ?? [],
  };

  const updateSearch = (patch: Partial<ReposSearch>) => {
    navigate({
      to: "/repos",
      search: (prev) => ({ ...prev, ...patch }),
    });
  };

  const onFiltersChange = (next: ReposFilters) => {
    updateSearch({
      source: next.source.length ? next.source : undefined,
      collection: next.collection.length ? next.collection : undefined,
      status: next.status.length ? next.status : undefined,
    });
  };

  const reposQ = useQuery({
    queryKey: ["repos", "all"],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos");
      return r.data ?? [];
    },
  });

  const collectionsQ = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      const r = await api.get<Collection[]>("/collections");
      return r.data ?? [];
    },
  });

  // For last-scan status filtering we need each repo's most-recent scan.
  // Pull the last N scans in one call and reduce client-side. For very
  // large fleets the server will need a `?latest_per_repo=1` mode — out
  // of scope here, comment kept for future readers.
  const scansQ = useQuery({
    queryKey: ["scans", "latest"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?limit=500");
      return r.data ?? [];
    },
  });

  const latestScanByRepo = useMemo(() => {
    const m = new Map<string, Scan>();
    for (const s of scansQ.data ?? []) {
      const prev = m.get(s.repo_id);
      if (!prev || (s.created_at ?? "") > (prev.created_at ?? "")) {
        m.set(s.repo_id, s);
      }
    }
    return m;
  }, [scansQ.data]);

  // collection_id → Set<repo_id>
  const collectionMembership = useMemo(() => {
    const m = new Map<string, Set<string>>();
    for (const c of collectionsQ.data ?? []) {
      const set = new Set<string>();
      for (const r of c.repos ?? []) set.add(r.id);
      m.set(c.id, set);
    }
    return m;
  }, [collectionsQ.data]);

  const filtered = useMemo(() => {
    const all = reposQ.data ?? [];
    return all.filter((r) => {
      if (filters.source.length > 0 && !filters.source.includes(r.source_type as RepoSourceFilter)) {
        return false;
      }
      if (filters.collection.length > 0) {
        const inAny = filters.collection.some((cid) =>
          collectionMembership.get(cid)?.has(r.id),
        );
        if (!inAny) return false;
      }
      if (filters.status.length > 0) {
        const last = latestScanByRepo.get(r.id);
        const status = repoStatusFor(last);
        if (!filters.status.includes(status)) return false;
      }
      return true;
    });
  }, [reposQ.data, filters, collectionMembership, latestScanByRepo]);

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

      <FilterBar
        filters={filters}
        onChange={onFiltersChange}
        collections={(collectionsQ.data ?? []).map((c) => ({ id: c.id, name: c.name }))}
      />

      {reposQ.isLoading ? (
        <ListSkeleton rows={5} />
      ) : !reposQ.data || reposQ.data.length === 0 ? (
        <EmptyState
          icon={GitForkIcon}
          title="No repositories yet"
          description="Add a local path, GitHub repo, or SSH-accessible tree to get started."
          cta={{ label: "Add repo", onClick: () => setShowAdd(true) }}
        />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={GitForkIcon}
          title="No repositories match"
          description="Try clearing one of the filter chips above."
        />
      ) : (
        <ul className="space-y-2">
          {filtered.map((r) => (
            <li key={r.id}>
              <RepoRow repo={r} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function RepoRow({ repo: r }: { repo: Repo }) {
  return (
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
  );
}

// Classify the most-recent scan for a repo into one of the filter buckets.
// Severity counts are not on the Scan summary, so we approximate with
// finding_count for high/critical buckets; the dedicated counts will land
// when /scans returns aggregated severity in a follow-up.
function repoStatusFor(scan: Scan | undefined): RepoStatusFilter {
  if (!scan) return "none";
  if (scan.status === "failed" || scan.status === "cancelled") return "failed";
  if (scan.status !== "completed") return "none";
  if (scan.finding_count === 0) return "clean";
  // No severity breakdown available on Scan summary — flag any non-zero
  // result as open-high so the filter is useful today. Will be refined
  // once the API returns finding_counts on the scan summary.
  return "open-high";
}
