// Repository workspace: current findings first, with scan history and setup separated.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  DownloadIcon,
  PlayIcon,
  RefreshCwIcon,
  Trash2Icon,
  GitBranchIcon,
} from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { RepositoryCISnippet } from "@/components/repos/ci-snippet";
import { api, ApiError, isNotFound } from "@/lib/api";
import { parseToolList, type Finding, type Repo, type Scan, type ScanSchedule } from "@/lib/types";
import { EmptyState, ErrorState, LoadingState, PermissionState } from "@/components/ui/empty-state";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@/components/ui/select";
import { Checkbox } from "@/components/ui/checkbox";
import { SeverityBadge } from "@/components/severity-badge";
import { CardSkeleton } from "@/components/skeleton";
import { FrameworksChips } from "@/components/frameworks-chips";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader } from "@/components/ui/page";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { formatRelativeTime } from "@/lib/utils";
import { findingRuleHint, findingTitle } from "@/lib/finding-title";
import { useScanWithPreflight } from "@/components/scan-preflight";
import { DeleteWithRecordsDialog } from "@/components/delete-with-records-dialog";
import { useMe, canModify } from "@/lib/me";
import { RepositoryTestCoverage } from "@/components/repos/test-coverage";

// One row per past scan from GET /api/scans/trends?repo_id=&branch=
interface TrendPoint {
  scan_id: string;
  branch: string;
  status: string;
  completed_at: string;
  total: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
}

type RepoSearch = {
  collection_id?: string;
  tab?: "findings" | "scans" | "tests" | "settings";
  test_scan?: string;
  scope?: "new" | "all";
  q?: string;
  severity?: string;
  page?: number;
  per_page?: number;
  sort?: string;
  order?: "asc" | "desc";
};

const EMPTY_FINDINGS: Finding[] = [];
const EMPTY_SCANS: Scan[] = [];
const SEVERITIES = ["critical", "high", "medium", "low", "info"];

export const Route = createFileRoute("/_authed/repos/$repoId")({
  validateSearch: (s): RepoSearch => ({
    collection_id: typeof s.collection_id === "string" ? s.collection_id : undefined,
    tab: s.tab === "scans" || s.tab === "settings" || s.tab === "tests" ? s.tab : undefined,
    test_scan: typeof s.test_scan === "string" && /^[a-zA-Z0-9-]+$/.test(s.test_scan) ? s.test_scan : undefined,
    scope: s.scope === "new" || s.scope === "all" ? s.scope : undefined,
    q: typeof s.q === "string" ? s.q : undefined,
    severity: typeof s.severity === "string" && SEVERITIES.includes(s.severity) ? s.severity : undefined,
    page: Number.isSafeInteger(Number(s.page)) && Number(s.page) > 0 ? Number(s.page) : undefined,
    per_page: [25, 50, 100].includes(Number(s.per_page)) ? Number(s.per_page) : undefined,
    sort: typeof s.sort === "string" && ["severity", "title", "file_path", "tool_name"].includes(s.sort) ? s.sort : undefined,
    order: s.order === "asc" ? "asc" : undefined,
  }),
  component: RepoDetailPage,
});

function RepoDetailPage() {
  const { repoId } = Route.useParams();
  const search = Route.useSearch();
  const { collection_id: collectionId } = search;
  const navigate = useNavigate();
  const qc = useQueryClient();

  const repoQ = useQuery({
    queryKey: ["repo", repoId],
    queryFn: async () => {
      const r = await api.get<Repo>(`/repos/${repoId}`);
      return r.data;
    },
  });

  const scansQ = useQuery({
    queryKey: ["scans", "by-repo", repoId],
    queryFn: async () => {
      const r = await api.get<Scan[]>(`/scans?repo_id=${repoId}&per_page=50&roots=true`);
      return { rows: r.data ?? [], total: r.meta?.total ?? r.data?.length ?? 0 };
    },
    refetchInterval: 15_000,
  });

  const trendsQ = useQuery({
    queryKey: ["scans-trends", repoId],
    queryFn: async () => {
      const r = await api.get<TrendPoint[]>(
        `/scans/trends?repo_id=${repoId}&limit=30`,
      );
      return r.data ?? [];
    },
    enabled: search.tab === "scans",
    // Refresh when the user kicks off a new scan from this page.
    refetchInterval: 30_000,
  });

  const [deleteOpen, setDeleteOpen] = useState(false);
  const latest = scansQ.data?.rows[0];
  const latestCompleted = scansQ.data?.rows.find((s) => s.status === "completed");

  const gateQ = useQuery({
    queryKey: ["scan", latest?.id, "gate"],
    queryFn: async () => (await api.get<{ evaluation: { status: string } }>(`/scans/${latest!.id}/gate`)).data,
    enabled: !!latest && latest.status === "completed",
    refetchInterval: 15_000,
  });
  const baselineQ = useQuery({
    queryKey: ["repo-baselines", repoId],
    queryFn: async () => (await api.get<{ id: string; branch: string }[]>(`/repos/${repoId}/baselines`)).data ?? [],
    refetchInterval: 15_000,
  });
  const newQ = useQuery({
    queryKey: ["findings", "repo-new-count", repoId, latest?.id, latest?.status],
    queryFn: async () => (await api.get<Finding[]>(`/findings?repo_id=${repoId}&status=open&baseline_state=new,resurfaced&per_page=1`)).meta?.total,
    enabled: !!latest && !!baselineQ.data?.length,
    refetchInterval: 15_000,
  });
  const me = useMe();

  // Scanning goes through the image preflight: it prompts to pull any selected
  // scanner whose container image isn't present before starting the scan.
  const scan = useScanWithPreflight();

  const sync = useMutation({
    mutationFn: async () => {
      const branch =
        repoQ.data?.default_branch || "main";
      const r = await api.post<SyncRepoResult>(
        `/repos/${repoId}/sync?branch=${encodeURIComponent(branch)}`,
      );
      return r.data;
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["repo", repoId] });
      const sha = data.last_commit_sha
        ? data.last_commit_sha.slice(0, 12)
        : "unknown";
      toast.success(
        data.changed
          ? `Synced ${data.branch} to ${sha}`
          : `${data.branch} already at ${sha}`,
      );
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Sync failed");
    },
  });

  const del = useMutation({
    mutationFn: async (purge: boolean) => {
      await api.delete(`/repos/${repoId}${purge ? "?purge=true" : ""}`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["repos"] });
      toast.success("Repo deleted");
      navigate({ to: "/repos" });
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    },
  });

  if (repoQ.error instanceof ApiError && repoQ.error.status === 403) return <PermissionState description="You do not have access to this repository." />;
  if (isNotFound(repoQ.error)) return <EmptyState title="Repository not found" description="This repository may have been removed." cta={{ label: "Repositories", to: "/repos" }} />;
  if (repoQ.isError) return <ErrorState title="Could not load repository" onRetry={() => void repoQ.refetch()} />;
  if (repoQ.isLoading) {
    return (
      <div className="page stack page--mid">
        <CardSkeleton />
      </div>
    );
  }
  if (!repoQ.data) return <EmptyState title="Repository not found" />;
  const r = repoQ.data;
  const langs = parseLanguages(r.detected_languages);

  return (
    <div className="page stack page--mid">
      {scan.dialog}
      <DeleteWithRecordsDialog open={deleteOpen} onOpenChange={setDeleteOpen} kind="repo" name={r.name}
        recordCount={scansQ.data?.total} pending={del.isPending} onConfirm={(purge) => del.mutate(purge)} />
      {collectionId && <Link to="/collections/$collectionId" params={{ collectionId }} className="text-sm text-muted-foreground hover:underline">Back to collection</Link>}
      <PageHeader
        title={r.name}
        description={<span className="flex flex-wrap items-center gap-x-4 gap-y-1"><span className="break-all">{r.source_path}</span><span className="inline-flex items-center gap-1 text-xs"><GitBranchIcon className="size-3.5" />{r.default_branch || "main"}</span></span>}
        actions={latest && canModify(me.data, r.user_id) && <button type="button" onClick={() => scan.configure({ repo_id: repoId, collection_id: collectionId, profile: "standard" })} disabled={scan.busy}
          className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"><PlayIcon className="size-4" />Scan repository</button>}
      />


      <Tabs value={search.tab ?? "findings"} onValueChange={(tab) => void navigate({ to: "/repos/$repoId", params: { repoId }, search: { ...search, tab: tab === "findings" ? undefined : tab as "scans" | "tests" | "settings" }, hash: "" })}>
        <TabsList aria-label="Repository views" className="flex h-auto flex-wrap justify-start"><TabsTrigger value="findings">Findings</TabsTrigger><TabsTrigger value="tests">Tests</TabsTrigger><TabsTrigger value="scans">Scan history</TabsTrigger><TabsTrigger value="settings">Settings</TabsTrigger></TabsList>
        <TabsContent value="findings" className="mt-5 space-y-5">
          {scansQ.isError ? <p role="alert" className="text-sm text-status-error">Could not load scan coverage. <button type="button" onClick={() => void scansQ.refetch()} className="underline">Retry coverage</button></p> : latest ? (
            <section aria-label="Latest scan" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/20 px-4 py-3">
              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-2 text-sm"><span className="font-medium">Latest scan</span><ScanStatusPill status={latest.status} scan={latest} gateStatus={latest.status === "completed" ? gateQ.isError ? "unavailable" : gateQ.data?.evaluation.status ?? "pending" : undefined} size="sm" />{gateQ.isError && <button type="button" className="text-xs underline" onClick={() => void gateQ.refetch()}>Retry verdict</button>}</div>
                <p className="text-xs text-muted-foreground">{latest.branch || r.default_branch} · {formatRelativeTime(latest.created_at)} · {parseToolList(latest.tools_completed).length} of {parseToolList(latest.tools_selected).length} tools completed{parseToolList(latest.tools_failed).length > 0 ? ` · ${parseToolList(latest.tools_failed).length} failed` : ""}</p>
              </div>
              <div className="flex flex-wrap items-center gap-4 text-sm">
                {baselineQ.isError ? <button onClick={() => void baselineQ.refetch()} className="underline">Retry baseline</button> : baselineQ.isLoading ? <span className="text-muted-foreground">Loading baseline…</span> : baselineQ.data?.length ? newQ.isError ? <button onClick={() => void newQ.refetch()} className="underline">Retry new count</button> : <Link to="/repos/$repoId" params={{ repoId }} search={{ ...search, scope: "new", page: 1, q: undefined, severity: undefined }} hash="findings" className="font-medium underline underline-offset-4">{newQ.data === undefined ? "Loading new findings…" : `${newQ.data} new`}{" "}<span className="font-normal text-muted-foreground">since baseline</span></Link> : <span className="text-muted-foreground">No baseline yet</span>}
                <Link to="/scans/$scanId" params={{ scanId: latest.id }} className="font-medium hover:underline">View scan details →</Link>
              </div>
            </section>
          ) : scansQ.isLoading ? <LoadingState title="Loading scan history" /> : <EmptyState icon={PlayIcon} title="Ready for your first scan" description="Scan this repository to discover findings and measure scanner coverage." cta={canModify(me.data, r.user_id) ? { label: "Scan repository", onClick: () => scan.configure({ repo_id: repoId, collection_id: collectionId, profile: "standard" }) } : undefined} />}
          {latest && <div id="findings" className="scroll-mt-20"><CurrentFindings repoId={repoId} hasBaseline={!!baselineQ.data?.length} baselineLoading={baselineQ.isLoading} /></div>}
        </TabsContent>
        <TabsContent value="tests" className="mt-5 space-y-6">
          {scansQ.isError ? <p role="alert">Could not load scans. <button className="underline" onClick={() => void scansQ.refetch()}>Retry scans</button></p> : (latest || search.test_scan) ? <>
            <label className="block max-w-full text-sm">Scan and commit<Select value={String((search.test_scan ?? latest?.id) ?? '') || '__empty__'} onValueChange={(value) => void navigate({ to: "/repos/$repoId", params: { repoId }, search: { ...search, test_scan: (value === '__empty__' ? '' : value) } })}><SelectTrigger aria-label="Test coverage scan" className="mt-1 h-9 w-full min-w-0 rounded-md border border-border bg-background px-3 text-sm"><SelectValue /></SelectTrigger><SelectContent>{search.test_scan && !scansQ.data?.rows.some((s) => s.id === search.test_scan) &&<SelectItem value={String(search.test_scan)}>Selected scan {search.test_scan}</SelectItem>}{scansQ.data?.rows.map((s) =><SelectItem key={s.id} value={String(s.id)}>{s.branch} · {s.commit_sha?.slice(0, 12) || s.id.slice(0, 8)} · {formatRelativeTime(s.created_at)}</SelectItem>)}</SelectContent></Select></label>
            <RepositoryTestCoverage key={search.test_scan ?? latest?.id} scanId={(search.test_scan ?? latest?.id)!} repoId={repoId} editable={canModify(me.data, r.user_id)} />
          </> : <p className="text-sm text-muted-foreground">{scansQ.isLoading ? "Loading scans…" : "Scan this repository first, then import measured test coverage for the scanned commit."}</p>}
        </TabsContent>
        <TabsContent value="scans" className="mt-5 space-y-6">
          {latestCompleted && <a href={`/api/v1/scans/${latestCompleted.id}/sarif`} className="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm hover:bg-muted/50"><DownloadIcon className="size-4" />Export latest completed scan (SARIF)</a>}
          <RepoScanHistory repoId={repoId} />
          <TrendsSection trends={trendsQ.data ?? []} loading={trendsQ.isLoading} defaultBranch={r.default_branch || "main"} />
        </TabsContent>
        <TabsContent value="settings" className="mt-5 space-y-6">
      <section aria-label="Repository details" className="glass-card p-5 grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 text-sm">
        <Field label="Source" value={r.source_type} mono />
        <Field label="Default branch" value={r.default_branch || "main"} />
        {r.source_type === "ssh" && (
          <>
            <Field label="Remote node" value={r.remote_node_id || "—"} mono />
            <Field label="Remote path" value={r.remote_path || r.source_path} mono />
          </>
        )}
        <Field label="Last commit" value={r.last_commit_sha || "—"} mono />
        <Field
          label="Dirty state"
          value={r.last_dirty_state || (r.last_commit_sha ? "unknown" : "—")}
        />
        <Field
          label="Languages"
          value={
            langs.length === 0 ? (
              <em className="text-muted-foreground">not yet detected</em>
            ) : (
              langs
                .map(([l, n]) => `${l} (${n})`)
                .join(", ")
            )
          }
        />
        <Field
          label="Frameworks"
          value={<FrameworksChips raw={r.detected_frameworks} />}
        />
        <Field
          label="First seen"
          value={r.created_at ? new Date(r.created_at).toLocaleString() : "—"}
        />
        <Field
          label="Last detection"
          value={
            r.detected_at ? new Date(r.detected_at).toLocaleString() : "—"
          }
        />
      </section>

          {canModify(me.data, r.user_id) && <button type="button" onClick={() => sync.mutate()} disabled={sync.isPending || scan.busy} className="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm hover:bg-muted/50 disabled:opacity-50"><RefreshCwIcon className={`size-4 ${sync.isPending ? "animate-spin" : ""}`} />{sync.isPending ? "Syncing…" : `Sync ${r.default_branch || "main"}`}</button>}
          <ScheduleCard repoId={repoId} defaultBranch={r.default_branch || "main"} />
          <section className="glass-card p-5 space-y-3">
            <RepositoryCISnippet repoId={repoId} />
            {r.source_type === "github" && <div className="space-y-2 text-sm text-muted-foreground"><p>In GitHub → Settings → Webhooks, add this URL for push and pull request events:</p><code className="block break-all rounded-md bg-muted/50 p-3 text-xs">{`${window.location.origin}/api/v1/webhooks/github`}</code><p>Configure the webhook secret in <Link to="/settings" search={{ tab: "scanners", advanced: true }} className="underline">Settings → Scanners → Advanced</Link>.</p></div>}
          </section>
          <RepoSuppressions repoId={repoId} />
          {canModify(me.data, r.user_id) && <section className="rounded-lg border border-destructive/30 p-5 space-y-3"><h2 className="text-sm font-medium">Delete repository</h2><p className="text-sm text-muted-foreground">Remove this repository from Wolf. You can choose whether to keep its scan records.</p><button type="button" onClick={() => setDeleteOpen(true)} className="inline-flex h-9 items-center gap-2 rounded-md border border-destructive/40 px-3 text-sm text-destructive hover:bg-destructive/10"><Trash2Icon className="size-4" />Delete repository</button></section>}
        </TabsContent>
      </Tabs>
    </div>
  );
}

const INTERVALS: { minutes: number; label: string }[] = [
  { minutes: 15, label: "15m" },
  { minutes: 60, label: "1h" },
  { minutes: 360, label: "6h" },
  { minutes: 1440, label: "daily" },
];

function ScheduleCard({
  repoId,
  defaultBranch,
}: {
  repoId: string;
  defaultBranch: string;
}) {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["schedules"],
    queryFn: async () => {
      try {
        const r = await api.get<ScanSchedule[] | { items: ScanSchedule[] }>(
          "/schedules",
        );
        if (Array.isArray(r.data)) return r.data;
        return r.data?.items ?? [];
      } catch (e) {
        if (isNotFound(e)) return null;
        throw e;
      }
    },
  });
  const existing = q.data?.find((s) => s.repo_id === repoId);
  const [interval, setInterval] = useState(60);
  const [profile, setProfile] = useState<"fast" | "standard" | "release">(
    "standard",
  );
  const [quietStart, setQuietStart] = useState("");
  const [quietEnd, setQuietEnd] = useState("");
  const [enabled, setEnabled] = useState(true);

  useEffect(() => {
    if (!existing) return;
    setInterval(existing.interval_minutes || 60);
    if (
      existing.profile === "fast" ||
      existing.profile === "standard" ||
      existing.profile === "release"
    ) {
      setProfile(existing.profile);
    }
    setQuietStart(existing.quiet_start || "");
    setQuietEnd(existing.quiet_end || "");
    setEnabled(existing.enabled);
  }, [existing]);

  const save = useMutation({
    mutationFn: async () => {
      const body: Partial<ScanSchedule> = {
        repo_id: repoId,
        interval_minutes: interval,
        branch: defaultBranch,
        profile,
        quiet_start: quietStart || undefined,
        quiet_end: quietEnd || undefined,
        enabled,
      };
      if (existing?.id) {
        return (
          await api.put<ScanSchedule>(`/schedules/${existing.id}`, {
            ...body,
            id: existing.id,
          })
        ).data;
      }
      return (await api.post<ScanSchedule>("/schedules", body)).data;
    },
    onSuccess: () => {
      toast.success("Schedule saved");
      qc.invalidateQueries({ queryKey: ["schedules"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Schedule save failed"),
  });

  if (q.error instanceof ApiError && q.error.status === 403) return <PermissionState description="You do not have access to repository schedules." />;
  if (q.isError) return <ErrorState title="Could not load schedule" onRetry={() => void q.refetch()} />;
  if (q.isLoading) return <LoadingState title="Loading schedule" />;
  if (q.data === null) return null;

  return (
    <section className="glass-card p-5 space-y-3">
      <h2 className="text-sm font-medium">Schedule</h2>
      <div className="flex flex-wrap items-end gap-3 text-sm">
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Interval</span>
          <Select value={String(interval)} onValueChange={(value) => setInterval(Number((value === '__empty__' ? '' : value)))}><SelectTrigger className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm" aria-label="Schedule interval"><SelectValue /></SelectTrigger><SelectContent>
            {INTERVALS.map((opt) => (<SelectItem key={opt.minutes} value={String(opt.minutes)}>
                {opt.label}
              </SelectItem>
            ))}
          </SelectContent></Select>
        </label>
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Profile</span>
          <Select value={String(profile)} onValueChange={(value) =>
              setProfile((value === '__empty__' ? '' : value) as "fast" | "standard" | "release")
            }><SelectTrigger className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm" aria-label="Scan profile"><SelectValue /></SelectTrigger><SelectContent>
            <SelectItem value={"fast"}>Fast</SelectItem>
            <SelectItem value={"standard"}>Standard</SelectItem>
            <SelectItem value={"release"}>Release</SelectItem>
          </SelectContent></Select>
        </label>
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Quiet start</span>
          <input
            type="time"
            value={quietStart}
            onChange={(e) => setQuietStart(e.target.value)}
            className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </label>
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Quiet end</span>
          <input
            type="time"
            value={quietEnd}
            onChange={(e) => setQuietEnd(e.target.value)}
            className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </label>
        <label className="inline-flex items-center gap-2 h-9 text-sm">
          <Checkbox checked={enabled} onCheckedChange={(value) => setEnabled(value === true)} />
          Enabled
        </label>
        <button
          type="button"
          onClick={() => save.mutate()}
          disabled={save.isPending || q.isLoading}
          className="inline-flex items-center h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
      </div>
    </section>
  );
}

function CurrentFindings({ repoId, hasBaseline, baselineLoading }: { repoId: string; hasBaseline: boolean; baselineLoading: boolean }) {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const pagination = { pageIndex: (search.page ?? 1) - 1, pageSize: search.per_page ?? 25 };
  const sort = search.sort ?? "severity";
  const order = search.order ?? "desc";
  const scope = search.scope ?? (hasBaseline ? "new" : "all");
  const update = (changes: Partial<RepoSearch>) => void navigate({ search: (previous) => ({ ...previous, ...changes }) });
  const q = useQuery({
    queryKey: ["findings", "repo", repoId, pagination, search.q, search.severity, sort, order, scope],
    queryFn: async () => {
      const params = new URLSearchParams({ repo_id: repoId, status: "open", page: String(pagination.pageIndex + 1), per_page: String(pagination.pageSize), sort, order });
      if (search.q) params.set("q", search.q);
      if (search.severity) params.set("severity", search.severity);
      if (scope === "new") params.set("baseline_state", "new,resurfaced");
      const r = await api.get<Finding[]>(`/findings?${params}`);
      return { rows: r.data ?? [], total: r.meta?.total ?? r.data?.length ?? 0 };
    },
    placeholderData: keepPreviousData,
  });
  const columns: Column<Finding>[] = [
    { key: "severity", header: "Severity", width: "8rem", accessor: (f) => <SeverityBadge severity={f.severity} size="sm" /> },
    { key: "title", header: "Finding", accessor: (f) => <Link to="/findings/$findingId" params={{ findingId: f.id }} className="block w-44 whitespace-normal font-medium hover:underline sm:w-auto sm:min-w-52 sm:max-w-lg">{findingTitle(f)}{findingRuleHint(f) && <span className="mt-1 block truncate font-mono text-xs font-normal text-muted-foreground" title={f.rule_id}>{findingRuleHint(f)}</span>}</Link> },
    { key: "file_path", header: "Location", accessor: (f) => <span title={f.file_path} className="block max-w-xs truncate font-mono text-xs text-muted-foreground">{f.file_path ? `${f.file_path}${f.line_start ? `:${f.line_start}` : ""}` : "Repository"}</span> },
    { key: "baseline", header: "Baseline", sortable: false, accessor: (f) => <span className="text-xs text-muted-foreground">{f.baseline_state || "Not compared"}</span> },
    { key: "tool_name", header: "Scanner", accessor: (f) => <span className="whitespace-nowrap text-xs">{f.tool_name}</span> },
  ];
  if (q.error instanceof ApiError && q.error.status === 403) return <PermissionState description="You do not have access to these findings." />;
  return (
    <section aria-label="Current findings" className="space-y-4">
      <div><h2 className="text-base font-semibold">{scope === "new" ? "New since baseline" : "Current findings"}{q.data && !q.isError ? <span className="ml-2 text-sm font-normal text-muted-foreground">{q.data.total.toLocaleString()}</span> : null}</h2><p className="mt-1 text-sm text-muted-foreground">{scope === "new" ? "Open findings introduced or resurfaced since the automatic baseline. Choose All open for existing debt." : "Open findings from the latest scans of each branch. Repeated scan observations are deduplicated; findings still need review."}</p></div>
      <div className="flex flex-wrap gap-2" aria-label="Findings scope">{([['new', 'New since baseline'], ['all', 'All open']] as const).map(([value, label]) => <button key={value} type="button" aria-pressed={scope === value} onClick={() => update({ scope: value, page: 1 })} className={`h-8 rounded-md border border-border px-3 text-xs ${scope === value ? 'bg-accent text-foreground' : 'text-muted-foreground hover:bg-accent'}`}>{label}</button>)}</div>
      <DataTable data={q.data?.rows ?? EMPTY_FINDINGS} columns={columns} keyExtractor={(f) => f.id} persistKey="repo-findings" density="compact" pageSize={pagination.pageSize}
        loading={baselineLoading || q.isLoading || q.isPlaceholderData} isError={q.isError} onRetry={() => void q.refetch()} emptyMessage={scope === "new" ? "No new or resurfaced findings. Choose All open to review existing findings." : "No open findings match these filters."}
        searchPlaceholder="Search findings, files, or scanners…"
        toolbar={<>
          <Select value={String(search.severity ?? "") || '__empty__'} onValueChange={(value) => update({ severity: (value === '__empty__' ? '' : value) || undefined, page: 1 })}><SelectTrigger aria-label="Filter by severity" className="h-8 w-auto min-w-36 rounded-md border border-border bg-background px-2 text-xs"><SelectValue /></SelectTrigger><SelectContent><SelectItem value={String("") || '__empty__'}>All severities</SelectItem>{SEVERITIES.map((s) =><SelectItem key={s} value={String(s)}>{s[0].toUpperCase() + s.slice(1)}</SelectItem>)}</SelectContent></Select>
          <Select value={String(pagination.pageSize)} onValueChange={(value) => update({ per_page: Number((value === '__empty__' ? '' : value)), page: 1 })}><SelectTrigger aria-label="Findings per page" className="h-8 w-auto min-w-36 rounded-md border border-border bg-background px-2 text-xs"><SelectValue /></SelectTrigger><SelectContent>{[25, 50, 100].map((n) =><SelectItem key={n} value={String(n)}>{n} per page</SelectItem>)}</SelectContent></Select>
        </>}
        serverSide={{ rowCount: q.data?.total ?? 0, pagination, onPaginationChange: (next) => update({ page: next.pageIndex + 1 }), search: search.q ?? "", onSearchChange: (value) => update({ q: value || undefined, page: 1 }), sorting: [{ id: sort, desc: order === "desc" }], onSortingChange: ([next]) => update({ sort: next?.id, order: next?.desc ? undefined : "asc", page: 1 }) }}
      />
    </section>
  );
}

function RepoScanHistory({ repoId }: { repoId: string }) {
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 });
  const q = useQuery({
    queryKey: ["scans", "repo-history", repoId, pagination],
    queryFn: async () => {
      const r = await api.get<Scan[]>(`/scans?repo_id=${repoId}&roots=true&page=${pagination.pageIndex + 1}&per_page=${pagination.pageSize}`);
      return { rows: r.data ?? [], total: r.meta?.total ?? r.data?.length ?? 0 };
    },
    placeholderData: keepPreviousData,
    refetchInterval: 15_000,
  });
  const columns: Column<Scan>[] = [
    { key: "date", header: "Started", sortable: false, accessor: (s) => <Link to="/scans/$scanId" params={{ scanId: s.id }} className="whitespace-nowrap text-sm hover:underline">{new Date(s.created_at).toLocaleString()}</Link> },
    { key: "branch", header: "Branch", sortable: false, accessor: (s) => <span className="font-mono text-xs">{s.branch || "—"}</span> },
    { key: "status", header: "Coverage", sortable: false, accessor: (s) => <ScanStatusPill status={s.status} scan={s} size="sm" /> },
    { key: "tools", header: "Tools completed", sortable: false, accessor: (s) => <span className="tabular-nums">{parseToolList(s.tools_completed).length} / {parseToolList(s.tools_selected).length}</span> },
    { key: "findings", header: "Findings", sortable: false, align: "right", accessor: (s) => <span className="tabular-nums">{s.finding_count.toLocaleString()}</span> },
  ];
  return <section aria-label="Scan history" className="space-y-4"><div><h2 className="text-base font-semibold">Scan history</h2><p className="mt-1 text-sm text-muted-foreground">Each run is a snapshot. Open a scan to inspect its tools, coverage, and results.</p></div><DataTable data={q.data?.rows ?? EMPTY_SCANS} columns={columns} keyExtractor={(s) => s.id} searchable={false} density="compact" pageSize={20} loading={q.isLoading || q.isPlaceholderData} isError={q.isError} onRetry={() => void q.refetch()} emptyMessage="No scans yet." serverSide={{ rowCount: q.data?.total ?? 0, pagination, onPaginationChange: setPagination }} /></section>;
}

interface RepoSuppression {
  id: string;
  scope_type: string;
  scope_value: string;
  reason: string;
  status: string;
  created_at: string;
}

function RepoSuppressions({ repoId }: { repoId: string }) {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["suppressions", repoId],
    queryFn: async () => {
      const r = await api.get<RepoSuppression[]>(
        `/suppressions?repo_id=${repoId}`,
      );
      return r.data ?? [];
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/suppressions/${id}`),
    onSuccess: () => {
      toast.success("Suppression revoked — next scan can show it again");
      qc.invalidateQueries({ queryKey: ["suppressions", repoId] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Revoke failed"),
  });
  const rows = q.data ?? [];
  return (
    <section className="glass-card overflow-hidden">
      <div className="px-5 py-3 border-b border-border">
        <h2 className="text-sm font-medium">Repo suppressions</h2>
        <p className="text-[11px] text-muted-foreground">
          These hide matching findings on every future scan of this repo. Agent
          “muted this run” rows are not listed here unless a durable
          suppression was written. Revoke anything you still want to see.
        </p>
      </div>
      {q.isLoading ? (
        <p className="px-5 py-3 text-sm text-muted-foreground">Loading…</p>
      ) : q.isError ? <ErrorState title="Could not load suppressions" onRetry={() => void q.refetch()} /> : rows.length === 0 ? (
        <p className="px-5 py-3 text-sm text-muted-foreground">
          No durable suppressions.
        </p>
      ) : (
        <ul className="divide-y divide-border/20 text-sm">
          {rows.map((s) => (
            <li
              key={s.id}
              className="px-5 py-2 flex items-start justify-between gap-3"
            >
              <div className="min-w-0">
                <div className="font-mono text-xs truncate">
                  {s.scope_type}: {s.scope_value}
                </div>
                <p className="mt-0.5 text-[11px] text-muted-foreground truncate">
                  {s.reason}
                </p>
              </div>
              <button
                type="button"
                onClick={() => revoke.mutate(s.id)}
                disabled={revoke.isPending}
                className="shrink-0 text-xs text-destructive hover:underline disabled:opacity-50"
              >
                Revoke
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`break-words text-sm ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  );
}

interface SyncRepoResult extends Repo {
  branch: string;
  previous_commit_sha?: string;
  changed: boolean;
}

// detected_languages is JSON-encoded: {"go": 219, "python": 2, ...}
function parseLanguages(s: string | undefined): Array<[string, number]> {
  if (!s) return [];
  try {
    const obj = JSON.parse(s) as Record<string, number>;
    return Object.entries(obj).sort(([, a], [, b]) => b - a);
  } catch {
    return [];
  }
}

// TrendsSection renders a stacked-bar chart of findings-by-severity
// over the repo's scan history. With a branch filter, it's the
// "trend on this repo/branch over all the runs" view. The x-axis is
// per-scan timestamp (so multiple scans in one day each get a bar).
// Hovering shows the severity breakdown; clicking a bar would be the
// natural follow-up but we keep it static here — the scan list below
// already provides drill-through.
function TrendsSection({
  trends,
  loading,
  defaultBranch,
}: {
  trends: TrendPoint[];
  loading: boolean;
  defaultBranch: string;
}) {
  const [branchFilter, setBranchFilter] = useState<string>(defaultBranch);
  if (loading) {
    return (
      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">Trend</h2>
        <div className="text-sm text-muted-foreground">Loading trend…</div>
      </section>
    );
  }
  if (!trends || trends.length === 0) {
    return (
      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">Trend</h2>
        <p className="text-sm text-muted-foreground">
          Run two or more scans on this repo to see a trend.
        </p>
      </section>
    );
  }
  // Available branches across the trend window — keep "all" as the default
  // top option, then list each branch we've seen.
  const branches = Array.from(new Set(trends.map((p) => p.branch))).filter(Boolean);
  const filtered =
    branchFilter === "__all__"
      ? trends
      : trends.filter((p) => p.branch === branchFilter);
  const data = filtered.map((p) => ({
    // Short timestamp label so the x-axis fits.
    name: new Date(p.completed_at).toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }),
    scan_id: p.scan_id,
    critical: p.critical,
    high: p.high,
    medium: p.medium,
    low: p.low,
    info: p.info,
    total: p.total,
  }));
  return (
    <section className="glass-card p-5">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-medium">
          Trend ({filtered.length} scan{filtered.length === 1 ? "" : "s"})
        </h2>
        {branches.length > 1 && (
          <Select value={String(branchFilter)} onValueChange={(value) => setBranchFilter((value === '__empty__' ? '' : value))}><SelectTrigger className="text-xs bg-muted/40 border border-border rounded px-2 h-7" aria-label="Trend branch"><SelectValue /></SelectTrigger><SelectContent>
            <SelectItem value={"__all__"}>all branches</SelectItem>
            {branches.map((b) => (<SelectItem key={b} value={String(b)}>
                {b}
              </SelectItem>
            ))}
          </SelectContent></Select>
        )}
      </div>
      <div className="h-64 w-full">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 5, right: 8, left: 0, bottom: 0 }}>
            <CartesianGrid strokeOpacity={0.1} vertical={false} />
            <XAxis
              dataKey="name"
              tick={{ fontSize: 10 }}
              interval="preserveStartEnd"
            />
            <YAxis tick={{ fontSize: 10 }} allowDecimals={false} />
            <Tooltip
              contentStyle={{
                background: "rgba(20,20,20,0.95)",
                border: "1px solid rgba(255,255,255,0.1)",
                fontSize: 12,
              }}
            />
            <Legend wrapperStyle={{ fontSize: 11 }} iconType="square" />
            {/* Stack severities so the bar height = total. Colors match
                the rest of the UI's severity palette. */}
            <Bar dataKey="critical" stackId="s" fill="#dc2626" name="Critical" />
            <Bar dataKey="high" stackId="s" fill="#f97316" name="High" />
            <Bar dataKey="medium" stackId="s" fill="#eab308" name="Medium" />
            <Bar dataKey="low" stackId="s" fill="#3b82f6" name="Low" />
            <Bar dataKey="info" stackId="s" fill="#71717a" name="Info" />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </section>
  );
}
