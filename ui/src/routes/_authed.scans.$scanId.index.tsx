// Scan detail with full findings exploration: filter by tool/severity,
// sort, paginate, export to JSON/CSV.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  BugIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  ChevronRightIcon,
  DownloadIcon,
  FilterXIcon,
  LoaderIcon,
  StopCircleIcon,
  XCircleIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { api } from "@/lib/api";
import type { Finding, Scan, Severity } from "@/lib/types";
import { parseToolList } from "@/lib/types";
import { CardSkeleton, ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { SeverityBadge } from "@/components/severity-badge";
import { CheckboxFilterRow } from "@/components/checkbox-filter-row";
import { ScanAgentsPanel } from "@/components/fixes/scan-agents-panel";

export const Route = createFileRoute("/_authed/scans/$scanId/")({
  component: ScanDetailPage,
});


// Shape returned by GET /api/scans/{id}/tools (added in server-side
// per-tool status work).
interface ToolStatusEntry {
  name: string;
  status: "completed" | "failed" | "running" | "queued" | "cancelled" | "pending";
  finding_count: number;
  raw_count?: number;
  has_output: boolean;
  error?: string;
}

const SEVERITY_RANK: Record<Severity, number> = {
  critical: 5,
  high: 4,
  medium: 3,
  low: 2,
  info: 1,
};

// Severity filter pills, most-severe first.
const SEVERITY_ORDER: Severity[] = [
  "critical",
  "high",
  "medium",
  "low",
  "info",
];

const PAGE_SIZE = 100;

function ScanDetailPage() {
  const { scanId } = Route.useParams();

  const scanQ = useQuery({
    queryKey: ["scan", scanId],
    queryFn: async () => {
      const r = await api.get<Scan>(`/scans/${scanId}`);
      return r.data;
    },
    // Poll while running so the page self-updates.
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s === "running" || s === "pending" ? 3000 : false;
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
      return status === "running" || status === "pending" ? 3000 : false;
    },
  });

  const [includeSuppressed, setIncludeSuppressed] = useState(false);

  // Fetch up to 20k findings — large but capped. The API also paginates
  // server-side; if a scan exceeds this we'd need cursor pagination.
  const findingsQ = useQuery({
    queryKey: ["scan", scanId, "findings", includeSuppressed],
    queryFn: async () => {
      // API caps per_page at 50000 — plenty for any single-scan view.
      // If you ever exceed that, switch to server-side pagination.
      const q = new URLSearchParams({ per_page: "50000" });
      if (includeSuppressed) q.set("include_suppressed", "true");
      const r = await api.get<Finding[]>(`/scans/${scanId}/findings?${q.toString()}`);
      return {
        items: r.data ?? [],
        total: r.meta?.total ?? 0,
        suppressed: r.meta?.suppressed ?? 0,
      };
    },
    // Poll while the scan is running so the table fills in as tools
    // complete (findings are persisted per-tool as each scanner finishes).
    // Stops polling once the scan reaches a terminal state.
    refetchInterval: () => {
      const s = scanQ.data?.status;
      return s === "running" || s === "pending" ? 3000 : false;
    },
  });

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

  const findings = findingsQ.data?.items ?? [];
  const serverSuppressed = findingsQ.data?.suppressed ?? 0;
  const serverTotal = findingsQ.data?.total ?? 0;
  // The DB persists per-tool findings *before* dedup, so the raw row
  // count == visible + suppressed. The scan record's finding_count is
  // post-dedup and may not match — we prefer the DB-reported numbers
  // because they reconcile with what the table actually shows.
  const rawTotal = serverTotal + (includeSuppressed ? 0 : serverSuppressed);

  // Distinct tools + per-tool counts → drives the tool filter.
  const toolCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const f of findings) m.set(f.tool_name, (m.get(f.tool_name) ?? 0) + 1);
    return Array.from(m.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [findings]);

  // Distinct categories present in this scan, with counts.
  const categoryCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const f of findings) m.set(f.category, (m.get(f.category) ?? 0) + 1);
    return m;
  }, [findings]);
  const categoryOptions = useMemo(
    () => Array.from(categoryCounts.keys()).sort(),
    [categoryCounts],
  );

  const severityCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const f of findings) m.set(f.severity, (m.get(f.severity) ?? 0) + 1);
    return m;
  }, [findings]);

  // Only the checkbox/tool filters and the free-text search are applied here.
  // Sorting and pagination moved into the shared DataTable, which owns that
  // state for every list in the console.
  const visible = useMemo(() => {
    const lowerSearch = search.trim().toLowerCase();
    return findings.filter((f) => {
      if (filterTool && f.tool_name !== filterTool) return false;
      if (excludedSeverities.has(f.severity)) return false;
      if (excludedCategories.has(f.category)) return false;
      if (lowerSearch) {
        const hay = `${f.title} ${f.file_path} ${f.rule_id ?? ""}`.toLowerCase();
        if (!hay.includes(lowerSearch)) return false;
      }
      return true;
    });
  }, [findings, filterTool, excludedSeverities, excludedCategories, search]);

  const findingColumns: Column<Finding>[] = [
    {
      key: "severity",
      header: "Severity",
      width: "8rem",
      sortAccessor: (f) => SEVERITY_RANK[f.severity] ?? 0,
      accessor: (f) => <SeverityBadge severity={f.severity} size="sm" />,
    },
    {
      key: "tool",
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
      key: "file",
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
      key: "line",
      header: "Line",
      align: "right",
      width: "6rem",
      sortAccessor: (f) => f.line_start ?? 0,
      accessor: (f) => (
        <span className="tabular-nums text-muted-foreground">{f.line_start || "—"}</span>
      ),
    },
  ];

  const exportFindings = (format: "json" | "csv") => {
    const stamp = new Date().toISOString().replace(/[:.]/g, "-");
    const base = `${scanQ.data?.repo?.name ?? scanId.slice(0, 8)}-${stamp}`;
    if (format === "json") {
      downloadBlob(
        JSON.stringify(visible, null, 2),
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
      for (const f of visible) {
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
  const failed = parseToolList(scan.tools_failed);

  return (
    <div className="page stack">
      <div className="flex items-center gap-3">
        <Link
          to="/scans"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div className="flex-1 min-w-0">
          <h1 className="text-xl font-semibold truncate">
            {scan.repo?.name ?? scan.id.slice(0, 8)}{" "}
            <span className="text-muted-foreground font-normal">
              · {scan.branch}
            </span>
            {scan.origin_scan_id ? (
              <span className="ml-2 text-xs font-normal text-muted-foreground">
                child of{" "}
                <Link
                  to="/scans/$scanId"
                  params={{ scanId: scan.origin_scan_id }}
                  className="font-mono hover:underline"
                >
                  {scan.origin_scan_id.slice(0, 8)}
                </Link>
              </span>
            ) : null}
          </h1>
          <p className="text-xs text-muted-foreground">
            {completed.length}/{selected.length} tools completed
            {failed.length > 0 && (
              <span className="text-status-error"> · {failed.length} failed</span>
            )}
            {" · "}
            {rawTotal > 0 ? (
              <>
                {serverTotal.toLocaleString()} visible
                {serverSuppressed > 0 && !includeSuppressed && (
                  <> · {serverSuppressed.toLocaleString()} suppressed</>
                )}
                {" · "}
                {rawTotal.toLocaleString()} total
              </>
            ) : (
              <>{scan.finding_count.toLocaleString()} findings</>
            )}
          </p>
          {/* When-it-ran line. Prefer started_at for the anchor (the
              moment the runner started executing tools), fall back to
              created_at if the scan never got past pending. Duration
              shows once we know both endpoints. */}
          <p className="text-xs text-muted-foreground mt-0.5">
            <ScanTimestamps scan={scan} />
          </p>
        </div>
        <ScanStatusPill status={scan.status} />
        {scan.status === "running" && <CancelScanButton scanId={scanId} />}
      </div>

      {(scan.status === "completed" ||
        scan.status === "cancelled" ||
        scan.origin_scan_id) &&
        scan.repo_id && (
          <ScanAgentsPanel
            scanId={scan.origin_scan_id || scanId}
            repoId={scan.repo_id}
            originFindings={scan.origin_scan_id ? undefined : scan.finding_count}
            github={
              scan.source_type === "github" ||
              scan.repo?.source_type === "github"
            }
            sourcePath={scan.source_path || scan.repo?.source_path}
          />
        )}

      {scan.status === "cancelled" && (
        <div
          className="rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-sm text-status-warning dark:text-status-warning"
          role="status"
          aria-live="polite"
        >
          <strong>Scan cancelled.</strong>{" "}
          Showing {scan.finding_count.toLocaleString()} findings from{" "}
          {completed.length} of {selected.length} tools that completed before
          cancellation.
        </div>
      )}

      {scan.status === "completed" && toolsSelectedCount(scan) === 0 && (
        <div className="mb-4 rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-sm text-status-warning">
          <div className="font-medium">No scanners ran</div>
          <div className="text-xs text-status-warning/80 mt-0.5">
            This scan completed without running any tools. The container scanner
            backend may not be configured — try{" "}
            <code className="font-mono text-xs">wolf doctor</code> from the CLI, or
            open Settings → Scanners to pull missing images.
          </div>
        </div>
      )}

      <ToolsPanel
        tools={toolsQ.data}
        loading={toolsQ.isLoading}
        scanId={scanId}
        scanStatus={scan.status}
      />

      <section>
        <div className="flex items-end justify-between gap-3 mb-3">
          <h2 className="text-base font-semibold">Findings</h2>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => exportFindings("json")}
              disabled={visible.length === 0}
              className="h-8 px-2.5 rounded-md hover:bg-muted/40 text-xs inline-flex items-center gap-1 disabled:opacity-50"
            >
              <DownloadIcon className="size-3.5" /> JSON
            </button>
            <button
              type="button"
              onClick={() => exportFindings("csv")}
              disabled={visible.length === 0}
              className="h-8 px-2.5 rounded-md hover:bg-muted/40 text-xs inline-flex items-center gap-1 disabled:opacity-50"
            >
              <DownloadIcon className="size-3.5" /> CSV
            </button>
          </div>
        </div>

        {findingsQ.isLoading ? (
          <ListSkeleton rows={8} />
        ) : findings.length === 0 ? (
          toolsSelectedCount(scan) > 0 ? (
            <EmptyState
              icon={BugIcon}
              title="No findings"
              description="This scan completed without any issues. Nice."
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
                <select
                  name="finding_tool_filter"
                  aria-label="Filter findings by tool"
                  value={filterTool}
                  onChange={(e) => {
                    setFilterTool(e.target.value);
                                  }}
                  className="h-8 px-2 rounded-md bg-background border border-muted/40"
                >
                  <option value="">All tools ({findings.length})</option>
                  {toolCounts.map(([name, count]) => (
                    <option key={name} value={name}>
                      {name} ({count})
                    </option>
                  ))}
                </select>
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
                  {visible.length.toLocaleString()} of {findings.length.toLocaleString()} shown
                </div>
              </div>
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
              )}
            </div>

            <DataTable
              data={visible}
              columns={findingColumns}
              keyExtractor={(f) => f.id}
              persistKey="scan-findings"
              density="compact"
              pageSize={PAGE_SIZE}
              searchable={false}
              emptyMessage="No findings match these filters"
            />

          </>
        )}
      </section>
    </div>
  );
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
      qc.invalidateQueries({ queryKey: ["scan-tools", scanId] });
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

// ToolsPanel — compact card grid of per-tool status. Names are short;
// keep status/count next to the name instead of stretching a one-column
// row across the page.
function ToolsPanel({
  tools,
  loading,
  scanId,
  scanStatus,
}: {
  tools: ToolStatusEntry[] | undefined;
  loading: boolean;
  scanId: string;
  scanStatus: string;
}) {
  // Auto-expand while the scan is in flight so the user can watch
  // tools tick through running → completed without an extra click.
  // Once the scan terminates, default to collapsed — at that point
  // the findings table is the headline and tools are drill-down.
  const scanActive = scanStatus === "running" || scanStatus === "pending";
  const [open, setOpen] = useState(scanActive);
  const contentId = useId();
  if (loading || !tools) {
    return (
      <section className="glass-card p-4">
        <h2 className="text-base font-semibold mb-2">Tools</h2>
        <div className="text-sm text-muted-foreground">Loading per-tool status…</div>
      </section>
    );
  }
  const completed = tools.filter((t) => t.status === "completed");
  const failed = tools.filter((t) => t.status === "failed");
  const cancelled = tools.filter((t) => t.status === "cancelled");
  const active = tools.filter(
    (t) => t.status === "running" || t.status === "queued" || t.status === "pending",
  );
  const rank = (s: ToolStatusEntry["status"]) =>
    s === "running" || s === "pending" ? 0 : s === "queued" ? 1 : s === "failed" ? 2 : s === "cancelled" ? 3 : 4;
  const ordered = [...tools].sort((a, b) => {
    const d = rank(a.status) - rank(b.status);
    if (d !== 0) return d;
    return b.finding_count - a.finding_count || a.name.localeCompare(b.name);
  });

  return (
    <section className="glass-card p-4">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        aria-controls={contentId}
        className="flex flex-wrap items-center gap-x-3 gap-y-1 w-full text-left"
      >
        {open ? (
          <ChevronDownIcon className="size-4 text-muted-foreground" />
        ) : (
          <ChevronRightIcon className="size-4 text-muted-foreground" />
        )}
        <h2 className="text-base font-semibold">Tools ({tools.length})</h2>
        <div className="text-xs text-muted-foreground tabular-nums flex flex-wrap gap-x-2">
          <span className="text-status-success">{completed.length} done</span>
          {failed.length > 0 && (
            <span className="text-status-error">{failed.length} failed</span>
          )}
          {cancelled.length > 0 && (
            <span className="text-status-warning">{cancelled.length} cancelled</span>
          )}
          {active.length > 0 && (
            <span className="text-status-info">{active.length} active</span>
          )}
        </div>
      </button>

      {open && (
        <div
          id={contentId}
          className="mt-3 grid grid-cols-[repeat(auto-fill,minmax(10.5rem,1fr))] gap-1.5"
        >
          {ordered.map((t) => (
            <ToolCard
              key={t.name}
              tool={t}
              scanId={scanId}
              cancellable={scanActive}
            />
          ))}
        </div>
      )}
    </section>
  );
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
      qc.invalidateQueries({ queryKey: ["scan-tools", scanId] });
      qc.invalidateQueries({ queryKey: ["scan", scanId] });
    },
  });
  const icon =
    tool.status === "completed" ? (
      <CheckCircle2Icon className="size-3.5 text-status-success shrink-0" />
    ) : tool.status === "failed" ? (
      <XCircleIcon className="size-3.5 text-status-error shrink-0" />
    ) : tool.status === "cancelled" ? (
      <StopCircleIcon className="size-3.5 text-status-warning shrink-0" />
    ) : tool.status === "queued" ? (
      <LoaderIcon className="size-3.5 text-muted-foreground shrink-0" />
    ) : (
      <LoaderIcon className="size-3.5 text-status-info animate-spin shrink-0" />
    );
  const tone =
    tool.status === "failed"
      ? "border-status-error/30 bg-status-error/5"
      : tool.status === "cancelled"
        ? "border-status-warning/25 bg-status-warning/5"
        : tool.status === "running" || tool.status === "pending"
          ? "border-status-info/30 bg-status-info/5"
          : "border-border bg-muted/10";
  const meta =
    tool.status === "completed"
      ? tool.finding_count > 0
        ? String(tool.finding_count)
        : "0"
      : tool.status === "queued"
        ? "queued"
        : tool.status === "cancelled"
          ? "cancelled"
          : tool.status === "failed"
            ? "failed"
            : "running";
  const canCancel =
    cancellable && (tool.status === "running" || tool.status === "queued");
  const canShowErr =
    (tool.status === "failed" || tool.status === "cancelled") && !!tool.error;

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
            className="text-[10px] underline text-muted-foreground hover:text-foreground shrink-0"
          >
            {showErr ? "hide" : "err"}
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
