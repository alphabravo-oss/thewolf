// Scans list + scan-trigger form.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useForm, useStore } from "@tanstack/react-form";
import { useQuery } from "@tanstack/react-query";
import { GaugeIcon, PlayIcon, XIcon } from "lucide-react";
import { useEffect, useState } from "react";
import type { PaginationState } from "@tanstack/react-table";
import { api } from "@/lib/api";
import { useScanWithPreflight } from "@/components/scan-preflight";
import type { Repo, Scan } from "@/lib/types";
import { parseToolList } from "@/lib/types";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { BranchSelect } from "@/components/branch-select";
import { ScanWorkerNotice } from "@/components/scan-worker-notice";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { StatusBadge } from "@/components/fixes/status-badge";
import {
  isFixPaused,
  useFixJobs,
  type FixJob,
  type FixJobStatus,
} from "@/lib/fixes";


type ScanSearch = { repo_id?: string; status?: string; q?: string; page?: number; sort?: string; order?: "asc" | "desc" };

export const Route = createFileRoute("/_authed/scans/")({
  validateSearch: (s): ScanSearch => ({
    repo_id: typeof s.repo_id === "string" ? s.repo_id : undefined,
    status: typeof s.status === "string" && ["pending", "running", "completed", "failed", "cancelled"].includes(s.status) ? s.status : undefined,
    q: typeof s.q === "string" ? s.q : undefined,
    page: Number.isInteger(Number(s.page)) && Number(s.page) > 0 ? Number(s.page) : undefined,
    sort: typeof s.sort === "string" && ["status", "id", "repo", "findings", "started"].includes(s.sort) ? s.sort : undefined,
    order: s.order === "asc" ? "asc" : "desc",
  }),
  component: ScansPage,
});

// Stable empty array — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_SCANS: Scan[] = [];

function durationMs(started?: string | null, completed?: string | null): number {
  if (!started || !completed) return -1;
  const ms = new Date(completed).getTime() - new Date(started).getTime();
  return Number.isNaN(ms) || ms < 0 ? -1 : ms;
}

function formatDuration(
  started?: string | null,
  completed?: string | null,
): string {
  if (!started || !completed) return "—";
  const ms = new Date(completed).getTime() - new Date(started).getTime();
  if (Number.isNaN(ms) || ms < 0) return "—";
  if (ms < 1000) return "0s";
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m ${s}s`;
}

const ORIGIN_FROM_BRANCH =
  /wolf-fix\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/i;

function jobScanKeys(job: FixJob): string[] {
  const keys = new Set<string>();
  if (job.scan_id) keys.add(job.scan_id);
  const branch = job.result_branch || job.target_branch || "";
  const m = ORIGIN_FROM_BRANCH.exec(branch);
  if (m) keys.add(m[1]);
  return [...keys];
}

function jobsByScan(
  jobs: FixJob[] | undefined,
  scans: Scan[] | undefined,
): Map<string, FixJob[]> {
  const originOf = new Map<string, string>();
  for (const s of scans ?? []) {
    originOf.set(s.id, s.origin_scan_id || s.id);
  }
  const m = new Map<string, FixJob[]>();
  const add = (key: string, job: FixJob) => {
    const list = m.get(key) ?? [];
    if (list.some((j) => j.id === job.id)) return;
    list.push(job);
    m.set(key, list);
  };
  for (const j of jobs ?? []) {
    for (const key of jobScanKeys(j)) {
      add(key, j);
      const origin = originOf.get(key);
      if (origin) add(origin, j);
    }
  }
  for (const list of m.values()) {
    list.sort(
      (a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
    );
  }
  return m;
}

function primaryJob(jobs: FixJob[]): FixJob | undefined {
  return (
    jobs.find((j) =>
      ["running", "claimed", "queued"].includes(j.status),
    ) ??
    jobs.find((j) => isFixPaused(j.status)) ??
    jobs.find((j) => j.status !== "superseded") ??
    jobs[0]
  );
}

function ScanAgentsCell({ jobs }: { jobs: FixJob[] }) {
  const job = primaryJob(jobs);
  if (!job) {
    return <span className="text-muted-foreground">—</span>;
  }
  const runs = jobs.length;
  const planned = job.planned_runs || 1;
  const extra =
    runs > 1
      ? `${runs} runs`
      : planned > 1
        ? `run ${job.run_index || 1}/${planned}`
        : "1 run";
  return (
    <Link
      to="/agents/$agentId"
      params={{ agentId: job.id }}
      className="inline-flex flex-col gap-0.5 min-w-0 hover:opacity-90"
      title={`${runs} agent run${runs === 1 ? "" : "s"}`}
    >
      <span className="inline-flex items-center gap-1.5 min-w-0">
        <StatusBadge status={job.status as FixJobStatus} />
      </span>
      <span className="text-[10px] text-muted-foreground tabular-nums">
        {extra}
        {job.pushed ? " · pushed" : ""}
      </span>
    </Link>
  );
}

function ScansPage() {
  const navigate = useNavigate();
  const [showForm, setShowForm] = useState(false);
  const [emptyFormDismissed, setEmptyFormDismissed] = useState(false);
  const search = Route.useSearch();
  const pagination = { pageIndex: (search.page ?? 1) - 1, pageSize: 50 };
  const setPagination = (next: PaginationState) => void navigate({ to: "/scans", search: { ...search, page: next.pageIndex + 1 } });
  const q = useQuery({
    queryKey: ["scans", "list", search, pagination.pageIndex, pagination.pageSize],
    queryFn: async () => {
      const params = new URLSearchParams({
        page: String(pagination.pageIndex + 1),
        per_page: String(pagination.pageSize),
        roots: "1",
      });
      for (const key of ["repo_id", "status", "q", "sort", "order"] as const) {
        if (search[key]) params.set(key, search[key]);
      }
      const r = await api.get<Scan[]>(`/scans?${params}`);
      const items = r.data ?? [];
      return { items, total: r.meta?.total ?? items.length };
    },
    refetchInterval: 10_000,
  });
  const jobsQ = useFixJobs();
  const childScansQ = useQuery({
    queryKey: ["scans", "for-agent-lineage"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?limit=200");
      return r.data ?? [];
    },
    refetchInterval: 10_000,
  });
  const byScan = jobsByScan(jobsQ.data, [
    ...(q.data?.items ?? []),
    ...(childScansQ.data ?? []),
  ]);

  const scans = q.data?.items ?? EMPTY_SCANS;

  const columns: Column<Scan>[] = [
    {
      key: "status",
      header: "Status",
      sortAccessor: (s) => s.status,
      filter: { label: "Status" },
      accessor: (s) => <ScanStatusPill scan={s} status={s.status} size="sm" />,
    },
    {
      key: "id",
      header: "Scan ID",
      sortAccessor: (s) => s.id,
      accessor: (s) => (
        <Link
          to={s.status === "running" ? "/scans/$scanId/live" : "/scans/$scanId"}
          params={{ scanId: s.id }}
          className="font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
          title={s.id}
          onClick={(e) => e.stopPropagation()}
        >
          {s.id.slice(0, 8)}
        </Link>
      ),
    },
    {
      key: "repo",
      header: "Repo · Branch",
      sortAccessor: (s) => s.repo?.name ?? "",
      accessor: (s) => (
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{s.repo?.name ?? "—"}</div>
          <div className="truncate font-mono text-xs text-muted-foreground">{s.branch}</div>
        </div>
      ),
    },
    {
      key: "tools",
      sortable: false,
      header: "Tools",
      sortAccessor: (s) => parseToolList(s.tools_completed).length,
      accessor: (s) => {
        const sel = parseToolList(s.tools_selected);
        const done = parseToolList(s.tools_completed);
        const failed = parseToolList(s.tools_failed);
        return (
          <span className="text-xs tabular-nums">
            <span className="text-foreground">{done.length}</span>
            <span className="text-muted-foreground">/{sel.length}</span>
            {failed.length > 0 && (
              <span className="ml-1 text-status-error">· {failed.length} failed</span>
            )}
          </span>
        );
      },
    },
    {
      key: "findings",
      header: "Findings",
      align: "right",
      sortAccessor: (s) => s.finding_count,
      accessor: (s) => (
        <span className="font-mono tabular-nums">{s.finding_count.toLocaleString()}</span>
      ),
    },
    {
      key: "agents",
      header: "Agents",
      sortable: false,
      accessor: (s) => <ScanAgentsCell jobs={byScan.get(s.id) ?? []} />,
    },
    {
      key: "started",
      header: "Started",
      sortAccessor: (s) => s.started_at ?? "",
      accessor: (s) => (
        <span className="text-xs text-muted-foreground">
          {s.started_at ? new Date(s.started_at).toLocaleString() : "—"}
        </span>
      ),
    },
    {
      key: "duration",
      sortable: false,
      header: "Duration",
      align: "right",
      sortAccessor: (s) => durationMs(s.started_at, s.completed_at),
      accessor: (s) => (
        <span className="text-xs">{formatDuration(s.started_at, s.completed_at)}</span>
      ),
    },
  ];

  const isEmpty = !q.isLoading && !q.isError && scans.length === 0 && (q.data?.total ?? 0) === 0;
  useEffect(() => {
    if (isEmpty && !search.q && !search.status && !search.repo_id && !emptyFormDismissed) setShowForm(true);
  }, [isEmpty, emptyFormDismissed, search.q, search.status, search.repo_id]);

  return (
    <PageShell>
      <PageHeader
        title={search.repo_id ? "Repository scans" : "All scans"}
        description={search.repo_id ? "Scan runs for the selected repository. Open a run to inspect its tools, coverage, gate, and findings." : "Scan runs across all repositories you can access, including every collection. Each scan is one execution against a branch or source."}
        actions={
          !showForm && (
            <button
              type="button"
              onClick={() => setShowForm(true)}
              className="inline-flex items-center gap-2 px-3 h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90"
            >
              <PlayIcon className="size-4" />
              New scan
            </button>
          )
        }
      />

      <ScanWorkerNotice />
      <div className="flex flex-wrap items-center gap-3">
        <label className="text-sm">Status <select aria-label="Scan status" value={search.status ?? ""}
          onChange={(e) => void navigate({ to: "/scans", search: { ...search, status: e.target.value || undefined, page: 1 } })}
          className="ml-2 h-9 rounded-md border border-border bg-background px-2">
          <option value="">All statuses</option>
          {["pending", "running", "completed", "failed", "cancelled"].map((status) => <option key={status} value={status}>{status}</option>)}
        </select></label>
        {search.repo_id && <button className="text-sm underline" onClick={() => void navigate({ to: "/scans", search: { ...search, repo_id: undefined, page: 1 } })}>Clear repository filter</button>}
      </div>
      {showForm && (
        <NewScanForm
          onClose={() => {
            setShowForm(false);
            setEmptyFormDismissed(true);
          }}
        />
      )}

      {isEmpty && !showForm && !search.q && !search.status && !search.repo_id ? (
        <EmptyState
          icon={GaugeIcon}
          title="No scans yet"
          description="Click New scan above, or pick a repo from a collection."
          cta={{ label: "New scan", onClick: () => setShowForm(true) }}
        />
      ) : (
        <DataTable
          data={scans}
          columns={columns}
          keyExtractor={(s) => s.id}
          persistKey="scans"
          density="compact"
          pageSize={pagination.pageSize}
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          searchPlaceholder="Search scans, repositories, branches..."
          emptyMessage="No scans match your filters"
          serverSide={{
            rowCount: q.data?.total ?? 0,
            pagination,
            onPaginationChange: setPagination,
            search: search.q ?? "",
            onSearchChange: (q) => void navigate({ to: "/scans", search: { ...search, q: q || undefined, page: 1 }, replace: true }),
            sorting: search.sort ? [{ id: search.sort, desc: search.order !== "asc" }] : [],
            onSortingChange: ([sort]) => void navigate({ to: "/scans", search: { ...search, sort: sort?.id, order: sort?.desc ? "desc" : "asc", page: 1 } }),
          }}
          onRowClick={(s) =>
            navigate({
              to: s.status === "running" ? "/scans/$scanId/live" : "/scans/$scanId",
              params: { scanId: s.id },
            })
          }
        />
      )}
    </PageShell>
  );
}

// New-scan form: pick a repo, optionally narrow the tool list, override
// branch, then start via useScanWithPreflight (same path as Scan this).
type ScannerSummary = { name: string; category: string; languages: string[] };

function NewScanForm({ onClose }: { onClose: () => void }) {
  const scan = useScanWithPreflight();
  const repos = useQuery({
    queryKey: ["repos", "for-scan"],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos");
      return r.data ?? [];
    },
  });
  const scanners = useQuery({
    queryKey: ["scanners", "list"],
    queryFn: async () => {
      const r = await api.get<ScannerSummary[]>("/scanners/list");
      return r.data ?? [];
    },
  });
  const form = useForm({
    defaultValues: { repoId: "", branch: "", toolMode: "auto" as "auto" | "explicit", picked: new Set<string>() },
    validators: { onSubmit: ({ value }) => !value.repoId ? "Choose a repository." :
      value.toolMode === "explicit" && value.picked.size === 0 ? "Choose at least one scanner." : undefined },
    onSubmit: ({ value }) => {
      const repo = repos.data?.find((r) => r.id === value.repoId);
      scan.launch({ repo_id: value.repoId, branch: value.branch.trim() || repo?.default_branch || "main",
        ...(value.toolMode === "explicit" ? { tools: Array.from(value.picked).sort() } : {}) });
    },
  });
  const { repoId, branch, toolMode, picked } = useStore(form.store, (state) => state.values);
  const setRepoId = (value: string) => form.setFieldValue("repoId", value);
  const setBranch = (value: string) => form.setFieldValue("branch", value);
  const setToolMode = (value: "auto" | "explicit") => form.setFieldValue("toolMode", value);
  const setPicked = (value: Set<string> | ((prev: Set<string>) => Set<string>)) => form.setFieldValue("picked", value);

  const selectedRepo = repos.data?.find((r) => r.id === repoId);

  // Group scanners by category for a tidier checkbox list.
  const grouped = (() => {
    const map = new Map<string, ScannerSummary[]>();
    for (const s of scanners.data ?? []) {
      const key = s.category || "other";
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(s);
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  })();

  const togglePick = (name: string) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };
  const pickAll = () =>
    setPicked(new Set((scanners.data ?? []).map((s) => s.name)));
  const pickNone = () => setPicked(new Set());

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
      className="glass-card p-4 space-y-3 max-w-2xl"
    >
      <div className="flex items-center justify-between">
        <div className="text-sm font-semibold">New scan</div>
        <button
          type="button"
          onClick={onClose}
          className="size-7 grid place-items-center rounded hover:bg-muted/40"
          aria-label="Cancel"
        >
          <XIcon className="size-4" />
        </button>
      </div>

      <form.Subscribe selector={(state) => state.errors}>{(errors) => errors.length > 0 &&
        <p role="alert" className="text-sm text-status-danger">{errors.join(" ")}</p>}</form.Subscribe>
      {repos.isError && <p role="alert" className="text-sm text-status-danger">Could not load repositories. <button type="button" onClick={() => void repos.refetch()}>Retry</button></p>}
      <label className="block">
        <span className="text-xs text-muted-foreground">Repo *</span>
        {repos.isLoading ? (
          <div className="text-xs text-muted-foreground mt-1">Loading…</div>
        ) : repos.isError ? null : !repos.data || repos.data.length === 0 ? (
          <div className="text-xs text-muted-foreground mt-1">
            No repos. Add one from a{" "}
            <Link to="/collections" className="underline">
              collection
            </Link>{" "}
            first.
          </div>
        ) : (
          <select
            name="repo_id"
            value={repoId}
            onChange={(e) => {
              setRepoId(e.target.value);
              const r = repos.data?.find((x) => x.id === e.target.value);
              if (r) setBranch("");
            }}
            required
            className="mt-1 w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          >
            <option value="">Pick a repo…</option>
            {repos.data.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name} — {r.source_path}
              </option>
            ))}
          </select>
        )}
      </label>

      {selectedRepo && (
        <label className="block">
          <span className="text-xs text-muted-foreground">Branch</span>
          <BranchSelect
            repoId={selectedRepo.id}
            value={branch}
            onChange={setBranch}
            defaultBranch={selectedRepo.default_branch}
            className="mt-1 w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </label>
      )}

      {selectedRepo && (
        <div className="space-y-2">
          <div className="text-xs text-muted-foreground">Tools to run</div>
          <div className="flex gap-1 text-xs">
            <button
              type="button"
              onClick={() => setToolMode("auto")}
              aria-pressed={toolMode === "auto"}
              className={`h-7 px-2.5 rounded-md transition ${
                toolMode === "auto"
                  ? "bg-primary text-primary-foreground"
                  : "hover:bg-muted/40 text-muted-foreground"
              }`}
            >
              Auto (by detected language)
            </button>
            <button
              type="button"
              onClick={() => setToolMode("explicit")}
              aria-pressed={toolMode === "explicit"}
              className={`h-7 px-2.5 rounded-md transition ${
                toolMode === "explicit"
                  ? "bg-primary text-primary-foreground"
                  : "hover:bg-muted/40 text-muted-foreground"
              }`}
            >
              Pick tools
            </button>
          </div>

          {toolMode === "explicit" && (
            <div className="border border-border rounded-md p-3 space-y-2 bg-muted/10">
              {scanners.isLoading ? (
                <div className="text-xs text-muted-foreground">
                  Loading scanners…
                </div>
              ) : (
                <>
                  <div className="flex items-center justify-between text-xs">
                    <span className="text-muted-foreground">
                      {picked.size} of {scanners.data?.length ?? 0} selected
                    </span>
                    <div className="flex gap-2">
                      <button
                        type="button"
                        onClick={pickAll}
                        className="underline hover:text-foreground text-muted-foreground"
                      >
                        All
                      </button>
                      <button
                        type="button"
                        onClick={pickNone}
                        className="underline hover:text-foreground text-muted-foreground"
                      >
                        None
                      </button>
                    </div>
                  </div>
                  <div className="max-h-64 overflow-y-auto space-y-2">
                    {grouped.map(([cat, items]) => (
                      <div key={cat}>
                        <div className="text-[10px] uppercase tracking-wide text-muted-foreground mb-1">
                          {cat}
                        </div>
                        <div className="grid grid-cols-1 sm:grid-cols-3 gap-1 text-xs">
                          {items.map((s) => (
                            <label
                              key={s.name}
                              className="flex items-center gap-1.5 px-1.5 py-1 rounded hover:bg-muted/30 cursor-pointer"
                            >
                              <input
                                type="checkbox"
                                name="tools"
                                value={s.name}
                                checked={picked.has(s.name)}
                                onChange={() => togglePick(s.name)}
                                className="accent-primary"
                              />
                              <span
                                className="truncate"
                                title={s.languages.join(", ") || "any"}
                              >
                                {s.name}
                              </span>
                            </label>
                          ))}
                        </div>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>
          )}
        </div>
      )}

      <div className="flex items-center gap-2 justify-end">
        <button
          type="button"
          onClick={onClose}
          className="h-8 px-3 rounded-md text-sm hover:bg-muted/40"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={
            !repoId ||
            scan.busy ||
            (toolMode === "explicit" && picked.size === 0)
          }
          className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50 inline-flex items-center gap-1.5"
        >
          <PlayIcon className="size-3.5" />
          {scan.busy
            ? "Starting…"
            : toolMode === "explicit"
              ? `Start scan (${picked.size})`
              : "Start scan"}
        </button>
      </div>
      {scan.dialog}
    </form>
  );
}
