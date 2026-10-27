// Scans list; "New scan" opens the NewScanDialog.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GaugeIcon, PlayIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import type { PaginationState } from "@tanstack/react-table";
import { api } from "@/lib/api";
import type { Scan } from "@/lib/types";
import { parseToolList } from "@/lib/types";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { MetricCard } from "@/components/ui/metric-card";
import { RowActions } from "@/components/ui/row-actions";
import { NewScanDialog } from "@/components/new-scan-dialog";
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

// One-row count queries so the KPI strip reports whole-fleet totals, not just the page on screen.
const COUNT_STATUSES = ["running", "failed", "completed"] as const;
function useScanCounts(repoId?: string) {
  return useQuery({
    queryKey: ["scans", "counts", repoId ?? null],
    queryFn: async () => {
      const total = async (status?: string) => {
        const params = new URLSearchParams({ per_page: "1", roots: "1" });
        if (repoId) params.set("repo_id", repoId);
        if (status) params.set("status", status);
        const r = await api.get<Scan[]>(`/scans?${params}`);
        return r.meta?.total ?? (r.data ?? []).length;
      };
      const [all, ...rest] = await Promise.all([total(), ...COUNT_STATUSES.map((st) => total(st))]);
      return { all, running: rest[0], failed: rest[1], completed: rest[2] };
    },
    refetchInterval: 10_000,
  });
}

function ScansPage() {
  const navigate = useNavigate();
  const [showForm, setShowForm] = useState(false);
  const search = Route.useSearch();
  const pagination = { pageIndex: (search.page ?? 1) - 1, pageSize: 50 };
  const setPagination = (next: PaginationState) => void navigate({ to: "/scans", search: { ...search, page: next.pageIndex + 1 } });
  const counts = useScanCounts(search.repo_id);
  const setStatus = (status?: string) => void navigate({ to: "/scans", search: { ...search, status, page: 1 } });
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
    {
      key: "actions",
      header: "Actions",
      hideHeader: true,
      sortable: false,
      align: "right",
      width: "3rem",
      accessor: (s) => (
        <RowActions label={`Actions for scan ${s.id.slice(0, 8)}`}>
          <DropdownMenuItem onSelect={() => void navigate({ to: "/scans/$scanId", params: { scanId: s.id } })}>Open scan</DropdownMenuItem>
          {s.status === "running" && <DropdownMenuItem onSelect={() => void navigate({ to: "/scans/$scanId/live", params: { scanId: s.id } })}>Open live view</DropdownMenuItem>}
          <DropdownMenuItem onSelect={() => void navigator.clipboard.writeText(s.id).then(() => toast.success("Scan ID copied"), () => toast.error("Could not copy the scan ID"))}>Copy scan ID</DropdownMenuItem>
        </RowActions>
      ),
    },
  ];

  const isEmpty = !q.isLoading && !q.isError && scans.length === 0 && (q.data?.total ?? 0) === 0;
  const unfiltered = !search.q && !search.status && !search.repo_id;
  const kpi = (title: string, status: string | undefined, value: number | undefined, subtitle: string) => (
    <button type="button" aria-pressed={search.status === status} aria-label={`${title}: filter scans`} onClick={() => setStatus(search.status === status ? undefined : status)} className="rounded-lg text-left focus-visible:outline-2 focus-visible:outline-primary">
      <MetricCard title={title} value={counts.isError ? "—" : value ?? "…"} subtitle={subtitle} className={search.status === status && status ? "h-full border-primary" : "h-full"} />
    </button>
  );

  return (
    <PageShell>
      <PageHeader
        title={search.repo_id ? "Repository scans" : "All scans"}
        description={search.repo_id ? "Scan runs for the selected repository. Open a run to inspect its tools, coverage, gate, and findings." : "Scan runs across all repositories you can access, including every collection. Each scan is one execution against a branch or source."}
        actions={
          <Button size="sm" onClick={() => setShowForm(true)}>
            <PlayIcon aria-hidden />
            New scan
          </Button>
        }
      />

      <ScanWorkerNotice />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {kpi("Scans", undefined, counts.data?.all, search.repo_id ? "For this repository" : "Root scans across the fleet")}
        {kpi("Running", "running", counts.data?.running, "Queued or executing now")}
        {kpi("Failed", "failed", counts.data?.failed, "Need a retry or a look")}
        {kpi("Completed", "completed", counts.data?.completed, "Finished, with or without findings")}
      </div>
      <NewScanDialog open={showForm} onOpenChange={setShowForm} />

      {isEmpty && unfiltered ? (
        <Card>
          <EmptyState
            icon={GaugeIcon}
            title="No scans yet"
            description="Start a scan on one of your repositories, or add a repository first."
            cta={{ label: "New scan", onClick: () => setShowForm(true) }}
          />
        </Card>
      ) : (
        <Card>
          <CardContent className="p-4">
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
              toolbar={
                <>
                  <label className="text-sm">Status <select aria-label="Scan status" value={search.status ?? ""}
                    onChange={(e) => setStatus(e.target.value || undefined)}
                    className="ml-2 h-9 rounded-md border border-input bg-background px-2">
                    <option value="">All statuses</option>
                    {["pending", "running", "completed", "failed", "cancelled"].map((status) => <option key={status} value={status}>{status}</option>)}
                  </select></label>
                  {search.repo_id && <Button variant="ghost" size="sm" onClick={() => void navigate({ to: "/scans", search: { ...search, repo_id: undefined, page: 1 } })}>Clear repository filter</Button>}
                </>
              }
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
          </CardContent>
        </Card>
      )}
    </PageShell>
  );
}
