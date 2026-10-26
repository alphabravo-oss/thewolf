// Scan detail with full findings exploration: filter by tool/severity,
// sort, paginate, export to JSON/CSV.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { keepPreviousData, useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  AlertTriangleIcon,
  BugIcon,
  CheckCircle2Icon,
  DownloadIcon,
  FilterXIcon,
  LoaderIcon,
  RefreshCwIcon,
  StopCircleIcon,
  XCircleIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type { PaginationState, SortingState } from "@tanstack/react-table";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useSSE } from "@/lib/sse";
import type { Finding, Scan, ScanScope, Severity } from "@/lib/types";
import { parseToolList } from "@/lib/types";
import { CardSkeleton, ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ScanWorkerNotice } from "@/components/scan-worker-notice";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { SeverityBadge } from "@/components/severity-badge";
import { severityRank } from "@/lib/severity";
import { CheckboxFilterRow } from "@/components/checkbox-filter-row";
import { useScanWithPreflight } from "@/components/scan-preflight";
import { useIsAdmin, useMe, displayLabel } from "@/lib/me";
import { useAutofixEnabled } from "@/lib/fixes";
import { useRuntimeCapabilities } from "@/lib/runtime-capabilities";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ScanAgentsPanel } from "@/components/fixes/scan-agents-panel";

export const Route = createFileRoute("/_authed/scans/$scanId/")({
  component: ScanDetailPage,
});


// Shape returned by GET /api/scans/{id}/tools (added in server-side
// per-tool status work).
interface ToolStatusEntry {
  name: string;
  status:
    | "completed"
    | "failed"
    | "running"
    | "queued"
    | "cancelled"
    | "pending"
    | "skipped"
    | "partial";
  finding_count: number;
  raw_count?: number;
  has_output: boolean;
  error?: string;
}

function hasPartialResults(tool: ToolStatusEntry) {
  return tool.status === "partial" || (tool.status === "failed" && (tool.raw_count ?? tool.finding_count) > 0);
}

// Severity filter pills, most-severe first.
const SEVERITY_ORDER: Severity[] = [
  "critical",
  "high",
  "medium",
  "low",
  "info",
];

const PAGE_SIZE = 50;

function ScanDetailPage() {
  const { scanId } = Route.useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const retryTools = useScanWithPreflight();
  const isAdmin = useIsAdmin();
  const me = useMe();
  const { enabled: autofixEnabled } = useAutofixEnabled();
  const capabilities = useRuntimeCapabilities();
  const [connectedScan, setConnectedScan] = useState<string | null>(null);
  const streamConnected = connectedScan === scanId;
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (refreshTimer.current) clearTimeout(refreshTimer.current);
    refreshTimer.current = null;
  }, [scanId]);
  const [output, setOutput] = useState<{ scanId: string; lines: string[] }>({ scanId, lines: [] });

  const scanQ = useQuery({
    queryKey: ["scan", scanId],
    queryFn: async () => {
      const r = await api.get<Scan>(`/scans/${scanId}`);
      return r.data;
    },
    // Poll only until SSE connects or while it is unavailable.
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return !streamConnected && (s === "running" || s === "pending") ? 3000 : false;
    },
  });

  const scanLive =
    scanQ.data?.status === "pending" || scanQ.data?.status === "running";
  useSSE<{ type?: string; tool_name?: string; line?: string }>({
    path: `/scans/${scanId}/stream`,
    enabled: !!scanLive,
    onOpen: () => {
      setConnectedScan(scanId);
      void qc.invalidateQueries({ queryKey: ["scan", scanId] });
    },
    onError: () => setConnectedScan(null),
    onEvent: (ev) => {
      if (ev.type === "tool_output" && typeof ev.line === "string") {
        const line = `[${ev.tool_name ?? "scanner"}] ${ev.line}`.slice(0, 2048);
        setOutput((previous) => ({ scanId, lines: [...(previous.scanId === scanId ? previous.lines : []), line].slice(-200) }));
      } else if (["scan_complete", "scan_cancelled"].includes(ev.type ?? "")) {
        if (refreshTimer.current) clearTimeout(refreshTimer.current);
        refreshTimer.current = null;
        void qc.invalidateQueries({ queryKey: ["scan", scanId] });
      } else if (["scan_progress", "scan_status", "tools_selected"].includes(ev.type ?? "") && !refreshTimer.current) {
        // One refresh per burst: 33 tools must not exhaust the API rate limit.
        refreshTimer.current = setTimeout(() => {
          refreshTimer.current = null;
          void qc.invalidateQueries({ queryKey: ["scan", scanId] });
        }, 1000);
      }
    },
  });

  const toolsQ = useQuery({
    queryKey: ["scan", scanId, "tools"],
    queryFn: async () => {
      const r = await api.get<ToolStatusEntry[]>(`/scans/${scanId}/tools`);
      return r.data ?? [];
    },
    refetchInterval: () => {
      const status = scanQ.data?.status;
      return !streamConnected && (status === "running" || status === "pending") ? 3000 : false;
    },
  });

  const gateQ = useQuery({
    queryKey: ["scan", scanId, "gate"],
    queryFn: async () => {
      const r = await api.get<ScanGate>(`/scans/${scanId}/gate`);
      return r.data;
    },
    enabled:
      scanQ.data?.status === "completed" || scanQ.data?.status === "failed",
  });

  const retry = useMutation({
    mutationFn: async () => {
      const r = await api.post<Scan>(`/scans/${scanId}/retry`);
      return r.data;
    },
    onSuccess: (data) => {
      toast.success("Retry started");
      if (data?.id) {
        navigate({ to: "/scans/$scanId", params: { scanId: data.id } });
      }
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Retry failed"),
  });

  const [includeSuppressed, setIncludeSuppressed] = useState(false);
  const [filterTool, setFilterTool] = useState<string>("");
  // Severity/category filters track what's *excluded* (unchecked). An empty
  // set means "show everything" — which is also the correct default before
  // findings have loaded, so we don't need to wait for data to initialize.
  const [excludedSeverities, setExcludedSeverities] = useState<Set<string>>(
    new Set(),
  );
  const [excludedCategories, setExcludedCategories] = useState<Set<string>>(
    new Set(),
  );
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });
  const [sorting, setSorting] = useState<SortingState>([{ id: "severity", desc: true }]);
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: PAGE_SIZE,
  });

  const statsQ = useQuery({
    queryKey: ["scan", scanId, "findings", "stats"],
    queryFn: async () => {
      const r = await api.get<{
        total: number;
        by_severity: Record<string, number>;
        by_tool: Record<string, number>;
        by_category: Record<string, number>;
      }>(`/scans/${scanId}/findings/stats`);
      return r.data;
    },
    refetchInterval: () => {
      const s = scanQ.data?.status;
      return !streamConnected && (s === "running" || s === "pending") ? 3000 : false;
    },
  });

  const includedSeverities = SEVERITY_ORDER.filter((s) => !excludedSeverities.has(s));
  const categoryKeys = Object.keys(statsQ.data?.by_category ?? {}).sort();
  const includedCategories = categoryKeys.filter((c) => !excludedCategories.has(c));

  const findingsQ = useQuery({
    queryKey: [
      "scan",
      scanId,
      "findings",
      includeSuppressed,
      filterTool,
      includedSeverities.join(","),
      includedCategories.join(","),
      pagination.pageIndex,
      pagination.pageSize,
      sorting,
      debouncedSearch,
    ],
    queryFn: async () => {
      const q = new URLSearchParams({
        page: String(pagination.pageIndex + 1),
        per_page: String(pagination.pageSize),
        sort: sorting[0]?.id ?? "severity",
        order: sorting[0]?.desc === false ? "asc" : "desc",
      });
      if (debouncedSearch.trim()) q.set("q", debouncedSearch.trim());
      if (includeSuppressed) q.set("include_suppressed", "true");
      if (filterTool) q.set("tool", filterTool);
      if (excludedSeverities.size > 0) {
        q.set("severity", includedSeverities.join(",") || "_");
      }
      if (excludedCategories.size > 0) {
        q.set("category", includedCategories.join(",") || "_");
      }
      const r = await api.get<Finding[]>(`/scans/${scanId}/findings?${q.toString()}`);
      const items = r.data ?? [];
      return {
        items,
        total: r.meta?.total ?? items.length,
        suppressed: r.meta?.suppressed ?? 0,
      };
    },
    placeholderData: keepPreviousData,
    refetchInterval: () => {
      const s = scanQ.data?.status;
      return !streamConnected && (s === "running" || s === "pending") ? 3000 : false;
    },
  });

  useEffect(() => {
    setPagination((p) => (p.pageIndex === 0 ? p : { ...p, pageIndex: 0 }));
  }, [filterTool, excludedSeverities, excludedCategories, includeSuppressed, debouncedSearch]);

  useEffect(() => {
    const status = scanQ.data?.status;
    if (status && status !== "running" && status !== "pending") {
      void qc.invalidateQueries({ queryKey: ["scan", scanId, "findings"] });
      void qc.invalidateQueries({ queryKey: ["scan", scanId, "tools"] });
    }
  }, [scanQ.data?.status, scanId, qc]);

  const findings = findingsQ.data?.items ?? [];
  const serverSuppressed = findingsQ.data?.suppressed ?? 0;
  const serverTotal = findingsQ.data?.total ?? 0;


  const toolCounts = useMemo(() => {
    const m = statsQ.data?.by_tool ?? {};
    return Object.entries(m).sort(([a], [b]) => a.localeCompare(b));
  }, [statsQ.data]);

  const categoryCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const [k, n] of Object.entries(statsQ.data?.by_category ?? {})) m.set(k, n);
    return m;
  }, [statsQ.data]);
  const categoryOptions = useMemo(
    () => Array.from(categoryCounts.keys()).sort(),
    [categoryCounts],
  );

  const severityCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const [k, n] of Object.entries(statsQ.data?.by_severity ?? {})) {
      if (n > 0) m.set(k, n);
    }
    return m;
  }, [statsQ.data]);


  const findingColumns: Column<Finding>[] = [
    {
      key: "severity",
      header: "Severity",
      width: "8rem",
      sortAccessor: (f) => severityRank[f.severity] ?? 0,
      accessor: (f) => <SeverityBadge severity={f.severity} size="sm" />,
    },
    {
      key: "tool_name",
      header: "Tool",
      width: "10rem",
      sortAccessor: (f) => f.tool_name,
      accessor: (f) => (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setFilterTool(f.tool_name);
          }}
          className="font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
          title={`Filter by ${f.tool_name}`}
        >
          {f.tool_name}
        </button>
      ),
    },
    {
      key: "title",
      header: "Title",
      sortAccessor: (f) => f.title,
      accessor: (f) => (
        <span className="min-w-0">
          <Link
            to="/findings/$findingId"
            params={{ findingId: f.id }}
            className="hover:underline"
            onClick={(e) => e.stopPropagation()}
          >
            {f.title}
          </Link>
          {f.rule_id && (
            <span className="ml-2 font-mono text-[10px] text-muted-foreground">[{f.rule_id}]</span>
          )}
        </span>
      ),
    },
    {
      key: "file_path",
      header: "File",
      sortAccessor: (f) => `${f.file_path}:${String(f.line_start ?? 0).padStart(8, "0")}`,
      accessor: (f) => (
        <span
          className="block max-w-xs truncate font-mono text-xs text-muted-foreground"
          title={f.file_path}
        >
          {f.file_path}
        </span>
      ),
    },
    {
      key: "line_start",
      header: "Line",
      align: "right",
      width: "6rem",
      sortAccessor: (f) => f.line_start ?? 0,
      accessor: (f) => (
        <span className="tabular-nums text-muted-foreground">{f.line_start || "—"}</span>
      ),
    },
  ];

  const exportFindings = async (format: "json" | "csv") => {
    const q = new URLSearchParams({ per_page: "200", sort: sorting[0]?.id ?? "severity", order: sorting[0]?.desc === false ? "asc" : "desc" });
    if (search.trim()) q.set("q", search.trim());
    if (includeSuppressed) q.set("include_suppressed", "true");
    if (filterTool) q.set("tool", filterTool);
    if (excludedSeverities.size > 0) {
      q.set("severity", includedSeverities.join(",") || "_");
    }
    if (excludedCategories.size > 0) {
      q.set("category", includedCategories.join(",") || "_");
    }
    const r = await api.get<Finding[]>(`/scans/${scanId}/findings?${q.toString()}`);
    const rows = r.data ?? [];
    const stamp = new Date().toISOString().replace(/[:.]/g, "-");
    const base = `${scanQ.data?.repo?.name ?? scanId.slice(0, 8)}-${stamp}`;
    if (format === "json") {
      downloadBlob(
        JSON.stringify(rows, null, 2),
        `${base}.json`,
        "application/json",
      );
    } else {
      const headers = [
        "id",
        "severity",
        "tool",
        "rule_id",
        "title",
        "file",
        "line",
        "status",
      ];
      const lines = [headers.join(",")];
      for (const f of rows) {
        lines.push(
          [
            f.id,
            f.severity,
            f.tool_name,
            f.rule_id ?? "",
            csvEscape(f.title),
            csvEscape(f.file_path),
            f.line_start ?? "",
            f.status,
          ].join(","),
        );
      }
      downloadBlob(lines.join("\n"), `${base}.csv`, "text/csv");
    }
  };

  const clearFilters = () => {
    setFilterTool("");
    setExcludedSeverities(new Set());
    setExcludedCategories(new Set());
    setSearch("");
  };

  // Toggle helpers: flip a value's membership in an "excluded" set.
  const toggleExcluded = (
    setter: React.Dispatch<React.SetStateAction<Set<string>>>,
    value: string,
  ) => {
    setter((prev) => {
      const next = new Set(prev);
      if (next.has(value)) next.delete(value);
      else next.add(value);
      return next;
    });
  };

  if (scanQ.isError) return <div className="page stack"><p role="alert">Could not load this scan. <button className="underline" onClick={() => void scanQ.refetch()}>Retry</button></p></div>;
  if (scanQ.isLoading || !scanQ.data) {
    return (
      <div className="page stack">
        <CardSkeleton />
      </div>
    );
  }
  const scan = scanQ.data;
  const completed = parseToolList(scan.tools_completed);
  const selected = parseToolList(scan.tools_selected);
  const tools = toolsQ.data ?? [];
  const partial = tools.filter(hasPartialResults);
  const failed = tools.filter((tool) => tool.status === "failed" && !hasPartialResults(tool));
  const stopped = tools.filter((tool) => tool.status === "cancelled");
  // Anything that did not give full coverage can be rerun on its own, including tools the user stopped.
  const retryable = tools.filter((tool) => tool.status === "failed" || tool.status === "cancelled" || hasPartialResults(tool));
  const done = tools.filter((tool) => tool.status === "completed");
  const active = tools.filter((tool) => ["running", "pending", "queued"].includes(tool.status));
  const running = active.filter((tool) => tool.status === "running");
  const missingImages = tools.filter((tool) => /not present locally|image.*not found|no such image/i.test(tool.error ?? ""));
  const incomplete = scan.completeness === "incomplete" || retryable.length > 0 || selected.length === 0 || selected.some((name) => !completed.includes(name)) || (toolsQ.isSuccess && done.length === 0);
  const launchRetryTools = () => retryTools.launch({ repo_id: scan.repo_id, collection_id: scan.collection_id, branch: scan.branch, profile: scan.profile, tools: retryable.map((tool) => tool.name) });
  const verdict = scanLive
    ? scan.status === "pending" ? "Queued: waiting for a scan worker" : running.length > 0 ? `Scanning: ${running.slice(0, 3).map((tool) => tool.name).join(", ")}${running.length > 3 ? ` and ${running.length - 3} more` : ""}` : "Scanning: preparing scanners and publishing results"
    : scan.status === "failed" ? `Scan failed: ${/no such file or directory/i.test(scan.failure_message ?? "") ? "the repository source is unavailable" : (scan.failure_message || scan.completion_reason || "analysis could not finish").split("\n")[0]}`
    : scan.status === "cancelled" ? "Scan cancelled. Results collected before cancellation are shown."
    : toolsQ.isPending ? "Checking scanner coverage…"
    : toolsQ.isError ? "Coverage unavailable: scanner results could not be loaded"
    : incomplete ? `Incomplete coverage: ${tools.length === 0 || selected.length === 0 ? "no scanners ran" : retryable.length > 0 ? `${[failed.length && `${failed.length} failed`, partial.length && `${partial.length} partial`, stopped.length && `${stopped.length} stopped`].filter(Boolean).join(", ")} of ${tools.length} scanners` : "not all selected scanners finished"} — results so far are shown`
    : gateVerdict(gateQ.data, gateQ.isError);

  return (
    <div className="page stack">
      {retryTools.dialog}
      <header className="space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <Link to="/scans" className="size-9 grid place-items-center rounded-md hover:bg-muted/50" aria-label="Back to scans"><ArrowLeftIcon className="size-4" /></Link>
          <h1 className="min-w-0 flex-1 text-xl font-semibold break-words">
            <Link to="/repos/$repoId" params={{ repoId: scan.repo_id }} className="hover:underline">{scan.repo?.name ?? "Repository"}</Link>
            <span className="font-normal text-muted-foreground"> · {scan.branch || "Default branch"} · <span className="capitalize">{scan.profile || "Custom"}</span></span>
          </h1>
          <ScanStatusPill scan={scan} status={scan.status} incomplete={incomplete || toolsQ.isError} gateStatus={gateQ.data?.evaluation?.status ?? gateQ.data?.result?.status ?? (gateQ.isError ? "unavailable" : "pending")} />
        </div>
        <p role="status" aria-live="polite" className="min-h-6 text-base font-medium break-words">{verdict}</p>
        <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span><ScanTimestamps scan={scan} /></span>
          <span>{completed.length}/{selected.length} tools completed · Attempt {scan.attempt || 1}</span>
          {scan.failure_code && <span className="font-mono">{scan.failure_code}</span>}
          {scan.commit_sha && <span title={scan.commit_sha}>Commit <span className="font-mono">{scan.commit_sha.slice(0, 7)}</span></span>}
          {me.data?.id === scan.user_id && <span>Requested by {displayLabel(me.data)}</span>}
          {scan.origin_scan_id && <Link to="/scans/$scanId" params={{ scanId: scan.origin_scan_id }} className="underline">Original scan</Link>}
          <Link to="/repos/$repoId" params={{ repoId: scan.repo_id }} search={{ tab: "tests", test_scan: scanId, collection_id: scan.collection_id }} className="hover:underline">Test coverage</Link>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {!scanLive && retryable.length > 0 && <Button onClick={() => void launchRetryTools()} disabled={retryTools.busy} aria-describedby="retry-tools-scope"><RefreshCwIcon className="size-4" />{retryTools.busy ? "Checking scanners…" : `Retry ${retryable.length} ${retryable.length === 1 ? "tool" : "tools"}`}</Button>}
          {(scan.status === "failed" || scan.status === "cancelled") && <Button variant="outline" onClick={() => retry.mutate()} disabled={retry.isPending || retryTools.busy}><RefreshCwIcon className="size-4" />{retry.isPending ? "Retrying…" : "Retry whole scan"}</Button>}
          {scanLive && <CancelScanButton scanId={scanId} />}
        </div>
        {!scanLive && retryable.length > 0 && <details className="text-xs text-muted-foreground" id="retry-tools-scope"><summary className="cursor-pointer">Retry scope: {retryable.length} failed, partial or stopped {retryable.length === 1 ? "tool" : "tools"} · new scan on {scan.branch || "the default branch"}</summary><p className="mt-1 break-words">{retryable.map((tool) => tool.name).join(", ")}. Completed tools are not rerun. Original results remain in this scan.</p></details>}
      </header>

      <section aria-label="Scanner coverage" className="rounded-lg border border-border px-4 py-3 space-y-2">
        <ToolsPanel tools={toolsQ.data} loading={toolsQ.isPending} error={toolsQ.isError} onRefresh={() => void toolsQ.refetch()} scanId={scanId} scanStatus={scan.status} />
        {(scanLive || tools.length > 0) && <div className="space-y-1"><div role="progressbar" aria-label="Scanner progress" aria-valuemin={0} aria-valuemax={Math.max(tools.length, 1)} aria-valuenow={tools.length - active.length} className="h-1 overflow-hidden rounded-full bg-muted"><div className={`h-full transition-[width] motion-reduce:transition-none ${scanLive ? "bg-primary" : "bg-muted-foreground/50"}`} style={{ width: `${tools.length ? (tools.length - active.length) / tools.length * 100 : 0}%` }} /></div><p className="text-xs text-muted-foreground">{tools.length - active.length} of {tools.length} scanners finished{!scanLive && active.length > 0 ? " · unfinished tools did not report a result" : ""}</p></div>}
        {missingImages.length > 0 && <p className="text-xs text-muted-foreground">{missingImages.length} scanners need images that are not downloaded.{isAdmin && capabilities.data?.docker_image_management && !scanLive && retryable.length > 0 ? <> <button type="button" className="underline hover:text-foreground" disabled={retryTools.busy} onClick={() => void launchRetryTools()}>Prepare images & retry</button></> : !isAdmin ? " Ask an administrator to prepare the images." : ""}</p>}
        {scanLive && <ScanWorkerNotice />}
      </section>

      <section aria-label="Scan findings summary" className="rounded-lg border border-border bg-muted/20 p-4 space-y-3">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div aria-live="polite" aria-atomic="true" className="flex items-baseline gap-3">
            <h2 className="text-sm font-medium text-muted-foreground">{scanLive ? "Findings reported so far" : "Findings reported"}</h2>
            <p className="text-2xl font-semibold tracking-tight tabular-nums">{(statsQ.data?.total ?? scan.finding_count).toLocaleString()}</p>
          </div>
          <p className="text-xs text-muted-foreground">{scanLive ? "Live results · updates as tools report" : "Scanner results"} · includes suppressed findings</p>
        </div>
        <dl className="grid grid-cols-3 gap-2 sm:grid-cols-5">
          {SEVERITY_ORDER.map((severity) => <div key={severity} className="rounded-md border border-border bg-background/50 px-3 py-2"><dt><SeverityBadge severity={severity} size="sm" /></dt><dd className="mt-1 text-xl font-semibold tabular-nums">{statsQ.data ? (statsQ.data.by_severity[severity] ?? 0).toLocaleString() : "—"}</dd></div>)}
        </dl>
        {statsQ.isError && <p role="alert" className="text-xs text-status-warning">Severity totals could not refresh. <button type="button" onClick={() => void statsQ.refetch()} className="underline">Retry totals</button></p>}
      </section>

      <section aria-label="Scan findings">
        <div className="flex items-end justify-between gap-3 mb-3">
          <h2 className="text-base font-semibold">Findings</h2>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => exportFindings("json")}
              disabled={findings.length === 0}
              className="h-8 px-2.5 rounded-md hover:bg-muted/40 text-xs inline-flex items-center gap-1 disabled:opacity-50"
            >
              <DownloadIcon className="size-3.5" /> JSON
            </button>
            <button
              type="button"
              onClick={() => exportFindings("csv")}
              disabled={findings.length === 0}
              className="h-8 px-2.5 rounded-md hover:bg-muted/40 text-xs inline-flex items-center gap-1 disabled:opacity-50"
            >
              <DownloadIcon className="size-3.5" /> CSV
            </button>
          </div>
        </div>

        {findingsQ.isLoading && !findingsQ.data ? (
          <ListSkeleton rows={8} />
        ) : findingsQ.isError ? (
          <p role="alert" className="text-sm text-status-error">Could not load findings. <button type="button" onClick={() => void findingsQ.refetch()} className="underline">Retry findings</button></p>
        ) : findings.length === 0 && (statsQ.data?.total ?? serverTotal) === 0 && !search && !filterTool && excludedSeverities.size === 0 && excludedCategories.size === 0 ? (
          toolsSelectedCount(scan) > 0 ? (
            <EmptyState
              icon={BugIcon}
              title={scanLive ? "Waiting for findings" : "No findings reported"}
              description={scanLive ? "Results will appear as scanners finish their analysis." : "Review scanner coverage before treating this scan as a clean result."}
            />
          ) : null
        ) : (
          <>
            {serverSuppressed > 0 && !includeSuppressed && (
              <div className="mb-3 text-xs px-3 py-2 rounded-md bg-status-warning/10 border border-status-warning/20 text-status-warning flex items-center justify-between">
                <span>
                  {serverSuppressed.toLocaleString()} finding{serverSuppressed === 1 ? "" : "s"} hidden by suppression rules (vendor/, node_modules/, testdata/, generated files, lockfiles).
                </span>
                <button
                  type="button"
                  onClick={() => setIncludeSuppressed(true)}
                  className="underline hover:text-status-warning"
                >
                  Show all
                </button>
              </div>
            )}
            {includeSuppressed && (
              <div className="mb-3 text-xs px-3 py-2 rounded-md bg-status-info/10 border border-status-info/20 text-status-info flex items-center justify-between">
                <span>Showing suppressed findings — these are typically noise.</span>
                <button
                  type="button"
                  onClick={() => setIncludeSuppressed(false)}
                  className="underline hover:text-status-info"
                >
                  Hide suppressed
                </button>
              </div>
            )}

            <div className="space-y-2 mb-3 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <input
                  type="text"
                  name="finding_search"
                  autoComplete="off"
                  aria-label="Search findings"
                  value={search}
                  onChange={(e) => {
                    setSearch(e.target.value);
                  }}
                  placeholder="Search title, file, rule…"
                  className="h-8 px-2 rounded-md bg-background border border-muted/40 w-64"
                />
                <Select value={filterTool || "_all"} onValueChange={(value) => setFilterTool(value === "_all" ? "" : value)}>
                  <SelectTrigger aria-label="Filter findings by tool" className="h-8 w-48 text-xs"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="_all">All tools ({(statsQ.data?.total ?? serverTotal).toLocaleString()})</SelectItem>{toolCounts.map(([name, count]) => <SelectItem key={name} value={name}>{name} ({count})</SelectItem>)}</SelectContent>
                </Select>
                {(filterTool ||
                  excludedSeverities.size > 0 ||
                  excludedCategories.size > 0 ||
                  search) && (
                  <button
                    type="button"
                    onClick={clearFilters}
                    className="h-8 px-2 rounded-md hover:bg-muted/40 inline-flex items-center gap-1 text-muted-foreground"
                  >
                    <FilterXIcon className="size-3.5" />
                    Clear
                  </button>
                )}
                <div className="ml-auto text-muted-foreground tabular-nums">
                  {findings.length.toLocaleString()} of {serverTotal.toLocaleString()} shown
                </div>
              </div>
              <details>
                <summary className="cursor-pointer text-muted-foreground">Severity and category filters{excludedSeverities.size + excludedCategories.size > 0 ? ` · ${excludedSeverities.size + excludedCategories.size} excluded` : ""}</summary>
                <div className="mt-2 space-y-2">
              <CheckboxFilterRow
                label="Severity"
                options={SEVERITY_ORDER.filter((s) => severityCounts.has(s))}
                isChecked={(v) => !excludedSeverities.has(v)}
                onToggle={(v) => toggleExcluded(setExcludedSeverities, v)}
                counts={severityCounts}
              />
              {categoryOptions.length > 0 && (
                <CheckboxFilterRow
                  label="Category"
                  options={categoryOptions}
                  isChecked={(v) => !excludedCategories.has(v)}
                  onToggle={(v) => toggleExcluded(setExcludedCategories, v)}
                  counts={categoryCounts}
                />
              )}                </div>
              </details>
            </div>

            <DataTable
              data={findings}
              columns={findingColumns}
              keyExtractor={(f) => f.id}
              persistKey="scan-findings"
              density="compact"
              pageSize={PAGE_SIZE}
              searchable={false}
              loading={findingsQ.isPlaceholderData}
              emptyMessage="No findings match these filters"
              serverSide={{
                rowCount: serverTotal,
                pagination,
                onPaginationChange: setPagination,
                sorting,
                onSortingChange: (next) => { setSorting(next); setPagination((p) => ({ ...p, pageIndex: 0 })); },
              }}
            />

          </>
        )}
      </section>
      {(scan.failure_message || scan.failure_code) && <details className="rounded-md border border-border p-3 text-sm"><summary className="cursor-pointer font-medium">Failure details</summary><p className="mt-2 whitespace-pre-wrap break-words text-muted-foreground">{scan.failure_message || scan.failure_code}</p></details>}
      {output.scanId === scanId && output.lines.length > 0 && (
        <details className="rounded-md border border-border p-3"><summary className="cursor-pointer text-sm font-medium">Tool output · latest 200 lines</summary><pre aria-label="Live tool output" tabIndex={0} className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-all text-xs">{output.lines.join("\n")}</pre></details>
      )}
      {!!scan.scanner_scopes?.length && (
        <details className="rounded-md border border-border p-3 text-sm">
          <summary className="cursor-pointer font-medium">Requested and effective scan scope</summary>
          <ul className="mt-2 space-y-3">
            {scan.scanner_scopes.map((scope) => <li key={scope.tool_name} className="break-words">
              <strong>{scope.tool_name}</strong>
              <p>Requested: {scopeLabel(scope.requested_scope)}</p>
              <p>Effective: {scopeLabel(scope.effective_scope)}</p>
              {scope.scope_message && <p className="text-status-warning">{scope.scope_message}</p>}
            </li>)}
          </ul>
        </details>
      )}

      {autofixEnabled && !scanLive && <ScanAgentsPanel scanId={scan.origin_scan_id || scanId} repoId={scan.repo_id} originFindings={scan.origin_scan_id ? undefined : scan.finding_count} github={scan.source_type === "github" || scan.repo?.source_type === "github"} sourcePath={scan.source_path || scan.repo?.source_path} />}

    </div>
  );
}



type ScanGate = {
  evaluation?: { status?: string; reason?: string; matched_rules?: { action: string; finding_ids: string[] }[] };
  result?: { status?: string };
  finding_count?: number;
};

function gateVerdict(gate: ScanGate | undefined, error: boolean) {
  if (error) return "Scan completed: quality gate could not be checked";
  if (!gate) return "Scan completed: checking quality gate…";
  const status = gate.evaluation?.status ?? gate.result?.status;
  const failures = new Set(gate.evaluation?.matched_rules?.filter((rule) => rule.action === "fail").flatMap((rule) => rule.finding_ids ?? []) ?? []).size;
  if (status === "fail" || status === "failed") return `Gate failed: ${gate.evaluation?.reason || (failures ? `${failures} findings violate the quality policy` : "quality policy requirements were not met")}`;
  if (status === "warn" || status === "warning") return `Gate warning: ${gate.evaluation?.reason || "findings need review under the quality policy"}`;
  if (status === "pass") return "Passed: no issues violate the quality policy";
  return `Gate incomplete: ${gate.evaluation?.reason || "quality policy could not be fully evaluated"}`;
}

function toolSkipReason(error?: string) {
  if (!error) return undefined;
  return error.includes("not present locally") ? "image not pulled" : error;
}

function toolsSelectedCount(scan: { tools_selected?: string }): number {
  if (!scan.tools_selected) return 0;
  try {
    const arr = JSON.parse(scan.tools_selected);
    return Array.isArray(arr) ? arr.length : 0;
  } catch {
    return 0;
  }
}


function csvEscape(s: string): string {
  if (s == null) return "";
  if (s.includes(",") || s.includes('"') || s.includes("\n")) {
    return `"${s.replace(/"/g, '""')}"`;
  }
  return s;
}

function downloadBlob(content: string, filename: string, mime: string) {
  const blob = new Blob([content], { type: mime });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

// CancelScanButton — top-level "Cancel scan" with confirm. Hits
// DELETE /api/scans/{id}; the server-side cancel is sticky, so the
// findings persisted by tools that completed before cancellation are
// preserved and shown in a "scan cancelled" banner.
function CancelScanButton({ scanId }: { scanId: string }) {
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const m = useMutation({
    mutationFn: () => api.delete(`/scans/${scanId}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["scan", scanId] });
      qc.invalidateQueries({ queryKey: ["scan", scanId, "tools"] });
    },
  });
  useEffect(() => {
    if (confirming) confirmRef.current?.focus();
  }, [confirming]);
  if (confirming) {
    return (
      <div className="flex items-center gap-2">
        <button
          ref={confirmRef}
          type="button"
          onClick={() => m.mutate()}
          disabled={m.isPending}
          className="inline-flex items-center gap-1 px-3 h-9 rounded-md bg-status-error/20 ring-1 ring-status-error/40 text-status-error text-sm hover:bg-status-error/30 disabled:opacity-50"
        >
          <StopCircleIcon className="size-4" />
          {m.isPending ? "Cancelling…" : "Confirm cancel"}
        </button>
        <button
          type="button"
          onClick={() => setConfirming(false)}
          className="text-xs text-muted-foreground hover:text-foreground"
        >
          back
        </button>
      </div>
    );
  }
  return (
    <button
      type="button"
      onClick={() => setConfirming(true)}
      className="inline-flex items-center gap-1 px-3 h-9 rounded-md bg-status-error/10 ring-1 ring-status-error/30 text-status-error text-sm hover:bg-status-error/20"
      title="Cancel the entire scan. Findings from tools that already completed are preserved."
    >
      <StopCircleIcon className="size-4" /> Cancel scan
    </button>
  );
}

function ToolsPanel({ tools, loading, error, onRefresh, scanId, scanStatus }: {
  tools: ToolStatusEntry[] | undefined;
  loading: boolean;
  error: boolean;
  onRefresh: () => void;
  scanId: string;
  scanStatus: string;
}) {
  if (loading) return <p className="text-sm text-muted-foreground">Loading scanner coverage…</p>;
  if (error || !tools) return <p role="alert" className="text-sm text-status-warning">Scanner coverage unavailable. <button className="underline" onClick={onRefresh}>Retry coverage</button></p>;
  const completed = tools.filter((tool) => tool.status === "completed");
  const failed = tools.filter((tool) => tool.status === "failed" && !hasPartialResults(tool));
  const partial = tools.filter(hasPartialResults);
  const skipped = tools.filter((tool) => tool.status === "skipped");
  const cancelled = tools.filter((tool) => tool.status === "cancelled");
  const active = tools.filter((tool) => ["running", "pending", "queued"].includes(tool.status));
  const scanActive = scanStatus === "running" || scanStatus === "pending";
  return <details>
    <summary className="cursor-pointer text-sm">
      <span className="font-medium">Tools ({tools.length})</span>
      <span className="ml-3 text-xs text-muted-foreground">{completed.length} completed · <span className={failed.length ? "text-status-error" : ""}>{failed.length} failed</span> · <span className={partial.length ? "text-status-warning" : ""}>{partial.length} partial</span> · {skipped.length} skipped{cancelled.length > 0 && ` · ${cancelled.length} stopped`}{active.length > 0 && ` · ${active.length} ${scanActive ? "active" : "not run"}`}</span>
      {partial.map((tool) => <span key={tool.name} className="mt-1 block text-xs text-status-warning">{tool.name}: <span>{tool.raw_count ?? tool.finding_count} findings retained · incomplete coverage</span></span>)}
    </summary>
    {tools.length === 0 && <p className="mt-2 text-sm text-muted-foreground">{scanActive ? "Scanners are being selected." : "No scanner execution was recorded. This is not a clean result."}</p>}
    <div className="mt-3 space-y-1">
      {[...failed, ...partial, ...active, ...cancelled].map((tool) => <ToolCard key={tool.name} tool={tool} scanId={scanId} cancellable={scanActive} />)}
      {[{ label: "Completed", tools: completed }, { label: "Skipped", tools: skipped }].filter((group) => group.tools.length > 0).map((group) => <details key={group.label} className="py-2 text-xs text-muted-foreground"><summary className="cursor-pointer">{group.label} tools ({group.tools.length})</summary><div className="mt-2 space-y-1">{group.tools.map((tool) => <ToolCard key={tool.name} tool={tool} scanId={scanId} />)}</div></details>)}
    </div>
  </details>;
}

function ToolCard({
  tool,
  scanId,
  cancellable,
}: {
  tool: ToolStatusEntry;
  scanId: string;
  cancellable?: boolean;
}) {
  const [showErr, setShowErr] = useState(false);
  const qc = useQueryClient();
  const cancel = useMutation({
    mutationFn: () =>
      api.delete(`/scans/${scanId}/tools/${encodeURIComponent(tool.name)}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["scan", scanId, "tools"] });
      qc.invalidateQueries({ queryKey: ["scan", scanId] });
    },
  });
  const partial = hasPartialResults(tool);
  const notRun = !cancellable && ["queued", "pending", "running"].includes(tool.status);
  const icon =
    notRun ? <StopCircleIcon className="size-3.5 text-muted-foreground shrink-0" /> :
    partial ? <AlertTriangleIcon className="size-3.5 text-status-warning shrink-0" /> : tool.status === "completed" ? (
      <CheckCircle2Icon className="size-3.5 text-status-success shrink-0" />
    ) : tool.status === "failed" ? (
      <XCircleIcon className="size-3.5 text-status-error shrink-0" />
    ) : tool.status === "skipped" ? (
      <StopCircleIcon className="size-3.5 text-muted-foreground shrink-0" />
    ) : tool.status === "cancelled" ? (
      <StopCircleIcon className="size-3.5 text-status-warning shrink-0" />
    ) : tool.status === "queued" ? (
      <LoaderIcon className="size-3.5 text-muted-foreground shrink-0" />
    ) : (
      <LoaderIcon className="size-3.5 text-status-info animate-spin shrink-0" />
    );
  const tone =
    partial ? "border-status-warning/30 bg-status-warning/5" : tool.status === "failed"
      ? "border-status-error/30 bg-status-error/5"
      : tool.status === "cancelled"
        ? "border-status-warning/25 bg-status-warning/5"
        : tool.status === "skipped"
          ? "border-border bg-muted/10"
          : tool.status === "running" || tool.status === "pending"
            ? "border-status-info/30 bg-status-info/5"
            : "border-border bg-muted/10";
  const meta =
    notRun ? "not run" : partial ? "partial" : tool.status === "completed"
      ? tool.finding_count > 0
        ? `${tool.finding_count} findings`
        : "0 findings"
      : tool.status === "queued"
        ? "queued"
        : tool.status === "cancelled"
          ? "cancelled"
          : tool.status === "skipped"
            ? "skipped"
            : tool.status === "failed"
              ? "failed"
              : "running";
  const canCancel =
    cancellable && (tool.status === "running" || tool.status === "queued");
  const reason = toolSkipReason(tool.error);
  const canShowErr =
    (tool.status === "failed" ||
      tool.status === "partial" ||
      tool.status === "cancelled" ||
      tool.status === "skipped") &&
    !!tool.error;

  return (
    <div className={`rounded-md border px-2 py-1.5 min-w-0 ${tone}`}>
      <div className="flex items-center gap-1.5 min-w-0">
        {icon}
        <span className="font-mono text-xs truncate">{tool.name}</span>
        <span className="text-[10px] tabular-nums text-muted-foreground shrink-0">
          {meta}
        </span>
        {canShowErr && (
          <button
            type="button"
            onClick={() => setShowErr(!showErr)}
            aria-expanded={showErr}
            aria-label={`${showErr ? "Hide" : "Show"} details for ${tool.name}`}
            className="text-[10px] underline text-muted-foreground hover:text-foreground shrink-0"
          >
            {showErr ? "Hide" : "Details"}
          </button>
        )}
        {canCancel && (
          <button
            type="button"
            onClick={() => cancel.mutate()}
            disabled={cancel.isPending}
            aria-label={`Cancel ${tool.name}; the rest of the scan keeps going`}
            title={`Cancel ${tool.name}; the rest of the scan keeps going`}
            className="inline-flex items-center justify-center size-4 rounded hover:bg-status-error/15 text-muted-foreground hover:text-status-error disabled:opacity-50 shrink-0"
          >
            <XIcon className="size-3" />
          </button>
        )}
      </div>
      {partial && <p className="mt-1 text-xs text-status-warning">{(tool.raw_count ?? tool.finding_count).toLocaleString()} findings retained · incomplete coverage</p>}
      {reason && (
        <p
          className="mt-1 text-[10px] text-muted-foreground truncate"
          title={tool.error}
        >
          {reason}
        </p>
      )}
      {showErr && tool.error && (
        <pre className="mt-1.5 text-[10px] font-mono text-status-error/80 whitespace-pre-wrap break-all max-h-32 overflow-y-auto">
          {tool.error}
        </pre>
      )}
    </div>
  );
}

// ScanTimestamps renders the "ran at … · took …" line under the scan
// header. Hovering each value shows the precise UTC ISO timestamp so
// operators can correlate with server logs / CI runs.
//
// State-machine:
//   pending   → "queued <relative>" (created_at only)
//   running   → "started <relative> · running <elapsed>"
//   terminal  → "ran <relative> · took <duration>"
function ScanTimestamps({ scan }: { scan: Scan }) {
  const created = scan.created_at;
  const started = scan.started_at;
  const completed = scan.completed_at;
  const isRunning = scan.status === "running" || scan.status === "pending";

  // Pick the "primary" timestamp to show as the user-facing anchor.
  const anchorISO = started ?? created;
  const anchorRel = relativeTime(anchorISO);
  const anchorAbs = formatAbsolute(anchorISO);

  // Duration: prefer started→completed (real runtime); fall back to
  // created→completed if started was never set (rare).
  let durationMs: number | null = null;
  if (completed && (started ?? created)) {
    durationMs = new Date(completed).getTime() - new Date(started ?? created).getTime();
  }

  if (isRunning) {
    return (
      <>
        <span title={anchorAbs}>started {anchorRel}</span>
        {started && <RunningElapsed startedAt={started} />}
      </>
    );
  }
  return (
    <>
      <span title={anchorAbs}>ran {anchorRel}</span>
      {durationMs !== null && (
        <>
          {" · "}
          <span title={`${anchorAbs} → ${formatAbsolute(completed!)}`}>
            took {formatDuration(durationMs)}
          </span>
        </>
      )}
    </>
  );
}

// RunningElapsed shows a live ticking duration while the scan is in
// flight. Updates every second; cleans up when the component unmounts
// or the scan transitions to a terminal state.
function RunningElapsed({ startedAt }: { startedAt: string }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const elapsed = now - new Date(startedAt).getTime();
  if (elapsed < 0) return null;
  return (
    <>
      {" · running "}
      {formatDuration(elapsed)}
    </>
  );
}

// relativeTime formats an ISO timestamp as "5 minutes ago", "2 hours
// ago", etc. — natural for the headline; the absolute value sits in
// the title= so operators can copy it.
function relativeTime(iso: string): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  const ago = (Date.now() - then) / 1000;
  if (ago < 0) return "just now";
  if (ago < 60) return `${Math.floor(ago)}s ago`;
  if (ago < 3600) return `${Math.floor(ago / 60)}m ago`;
  if (ago < 86400) {
    const h = Math.floor(ago / 3600);
    return `${h}h ago`;
  }
  const d = Math.floor(ago / 86400);
  if (d < 7) return `${d}d ago`;
  return new Date(iso).toLocaleDateString();
}

// formatAbsolute returns a locale-aware "May 14, 2026, 5:50:43 PM"
// for the title attribute / tooltip.
function formatAbsolute(iso: string): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString();
}

// formatDuration renders a millisecond delta as "2m 47s" or "1h 12m"
// — whatever is most readable for the magnitude. Sub-second values
// floor to 0s rather than emit "0.5s"; the page never refreshes
// fast enough for fractional seconds to be meaningful.
function formatDuration(ms: number): string {
  if (ms < 1000) return "0s";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) {
    const remS = s % 60;
    return remS === 0 ? `${m}m` : `${m}m ${remS}s`;
  }
  const h = Math.floor(m / 60);
  const remM = m % 60;
  return remM === 0 ? `${h}h` : `${h}h ${remM}m`;
}

function scopeLabel(scope: ScanScope | null | undefined) {
  if (!scope) return "No selectors recorded";
  const parts = scope.profile ? [`Profile ${scope.profile}`] : [];
  for (const [key, label] of [["tools", "Tools"], ["categories", "Categories"], ["include_paths", "Include"], ["exclude_paths", "Exclude"]] as const) {
    if (scope[key]?.length) parts.push(`${label}: ${scope[key].join(", ")}`);
  }
  return parts.join(" · ") || "No selectors recorded";
}
