// Repos list. A repo is the unit a scan runs against — either a local
// filesystem path or a remote git URL. Repos can be referenced from
// multiple collections.
import { useMemo, useState } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GitBranchIcon, GitForkIcon, GithubIcon, HardDriveIcon, PlusIcon, ServerIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Collection, Repo, Scan } from "@/lib/types";
import { ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { AddRepoForm } from "@/components/add-repo-form";
import { ImportGitHubModal } from "@/components/repos/import-github-modal";
import { DiscoverSSHModal } from "@/components/repos/discover-ssh-modal";
import {
  FilterBar,
  type ReposFilters,
  type RepoSourceFilter,
  type RepoStatusFilter,
} from "@/components/repos/filter-bar";
import { GroupToggle, type RepoGroupBy } from "@/components/repos/group-toggle";
import { BulkToolbar } from "@/components/repos/bulk-toolbar";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";

type GroupBy = RepoGroupBy;

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
  const [showImportGitHub, setShowImportGitHub] = useState(false);
  const [showDiscoverSSH, setShowDiscoverSSH] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const search = Route.useSearch();
  const navigate = useNavigate();

  const toggleSelected = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const clearSelected = () => setSelected(new Set());

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
    <div className="page stack">
      <div className="flex items-start justify-between gap-4 mb-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Repositories</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Source-code targets — local paths, GitHub, remote git URLs, or SSH nodes.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setShowImportGitHub(true)}
            className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-sm font-medium hover:bg-muted/40"
          >
            <GithubIcon className="size-4" />
            Import from GitHub
          </button>
          <button
            type="button"
            onClick={() => setShowDiscoverSSH(true)}
            className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-sm font-medium hover:bg-muted/40"
          >
            <ServerIcon className="size-4" />
            Discover SSH
          </button>
          <button
            type="button"
            onClick={() => setShowAdd((v) => !v)}
            className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:opacity-90"
          >
            <PlusIcon className="size-4" />
            Add repo
          </button>
        </div>
      </div>

      {showImportGitHub && (
        <ImportGitHubModal onClose={() => setShowImportGitHub(false)} />
      )}

      {showDiscoverSSH && (
        <DiscoverSSHModal onClose={() => setShowDiscoverSSH(false)} />
      )}

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

      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterBar
          filters={filters}
          onChange={onFiltersChange}
          collections={(collectionsQ.data ?? []).map((c) => ({ id: c.id, name: c.name }))}
          visibleCount={filtered.length}
          onSelectAllVisible={() => setSelected(new Set(filtered.map((r) => r.id)))}
        />
        <GroupToggle
          value={search.group ?? "none"}
          onChange={(v) => updateSearch({ group: v === "none" ? undefined : v })}
        />
      </div>

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
      ) : (search.group ?? "none") === "none" ? (
        <ul className="space-y-2">
          {filtered.map((r) => (
            <li key={r.id}>
              <RepoRow
                repo={r}
                selected={selected.has(r.id)}
                onToggleSelect={() => toggleSelected(r.id)}
              />
            </li>
          ))}
        </ul>
      ) : (
        <GroupedRepos
          repos={filtered}
          groupBy={search.group ?? "none"}
          collections={collectionsQ.data ?? []}
          selected={selected}
          onToggleSelect={toggleSelected}
        />
      )}

      <BulkToolbar
        selectedIds={selected}
        repos={reposQ.data ?? []}
        onClear={clearSelected}
      />
    </div>
  );
}

function GroupedRepos({
  repos,
  groupBy,
  collections,
  selected,
  onToggleSelect,
}: {
  repos: Repo[];
  groupBy: GroupBy;
  collections: Collection[];
  selected: Set<string>;
  onToggleSelect: (id: string) => void;
}) {
  const groups = useMemo(() => groupRepos(repos, groupBy, collections), [
    repos,
    groupBy,
    collections,
  ]);
  return (
    <div className="space-y-3">
      {groups.map((g) => (
        <Card key={g.key}>
          <CardHeader className="py-3">
            <CardTitle className="flex items-center justify-between text-sm font-medium">
              <span>{g.label}</span>
              <span className="text-xs text-muted-foreground">{g.repos.length}</span>
            </CardTitle>
          </CardHeader>
          <CardContent className="pt-0">
            <ul className="space-y-2">
              {g.repos.map((r) => (
                <li key={r.id}>
                  <RepoRow
                    repo={r}
                    selected={selected.has(r.id)}
                    onToggleSelect={() => onToggleSelect(r.id)}
                  />
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

interface RepoGroup {
  key: string;
  label: string;
  repos: Repo[];
}

function groupRepos(repos: Repo[], by: GroupBy, collections: Collection[]): RepoGroup[] {
  if (by === "none") return [{ key: "all", label: "All", repos }];

  const groups = new Map<string, RepoGroup>();
  const ensure = (key: string, label: string): RepoGroup => {
    let g = groups.get(key);
    if (!g) {
      g = { key, label, repos: [] };
      groups.set(key, g);
    }
    return g;
  };

  if (by === "source_type") {
    const LABELS: Record<string, string> = {
      local: "Local",
      github: "GitHub",
      gitlab: "GitLab",
      git: "Git URL",
      ssh: "SSH",
    };
    for (const r of repos) {
      const key = r.source_type || "other";
      ensure(key, LABELS[key] ?? key).repos.push(r);
    }
  } else if (by === "language") {
    for (const r of repos) {
      const primary = primaryLanguage(r.detected_languages);
      const key = primary || "unknown";
      const label = primary ? capitalize(primary) : "Unknown language";
      ensure(key, label).repos.push(r);
    }
  } else if (by === "collection") {
    // Build membership map. A repo may belong to multiple collections; we
    // emit one row per (collection, repo) pair so each card lists the
    // members the user expects.
    for (const c of collections) {
      const members = (c.repos ?? []).map((cr) => cr.id);
      const subset = repos.filter((r) => members.includes(r.id));
      if (subset.length === 0) continue;
      ensure(c.id, c.name).repos.push(...subset);
    }
    const grouped = new Set<string>();
    for (const g of groups.values()) for (const r of g.repos) grouped.add(r.id);
    const ungrouped = repos.filter((r) => !grouped.has(r.id));
    if (ungrouped.length > 0) ensure("__none", "Ungrouped").repos.push(...ungrouped);
  }

  return Array.from(groups.values()).sort((a, b) => b.repos.length - a.repos.length);
}

function primaryLanguage(s: string | undefined): string {
  if (!s) return "";
  try {
    const obj = JSON.parse(s) as Record<string, number>;
    const sorted = Object.entries(obj).sort(([, a], [, b]) => b - a);
    return sorted[0]?.[0] ?? "";
  } catch {
    return "";
  }
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function RepoRow({
  repo: r,
  selected,
  onToggleSelect,
}: {
  repo: Repo;
  selected: boolean;
  onToggleSelect: () => void;
}) {
  return (
    <div
      className={
        "glass-card flex items-center gap-3 pl-3 pr-4 py-3 transition " +
        (selected ? "bg-accent/50" : "hover:bg-muted/30")
      }
    >
      <div
        // Stop link navigation when clicking the checkbox region.
        onClick={(e) => e.stopPropagation()}
        className="flex items-center"
      >
        <Checkbox
          checked={selected}
          onCheckedChange={onToggleSelect}
          aria-label={`Select ${r.name}`}
        />
      </div>
      <Link
        to="/repos/$repoId"
        params={{ repoId: r.id }}
        className="flex flex-1 items-center gap-3 min-w-0"
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
    </div>
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
