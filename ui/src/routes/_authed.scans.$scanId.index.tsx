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
import { EmptyState } from "@/components/empty-state";
import { ScanStatusPill } from "@/components/scan-status-pill";
import { SeverityBadge } from "@/components/severity-badge";
import { CheckboxFilterRow } from "@/components/checkbox-filter-row";
import { ScanReleaseControls } from "@/components/scan-release-controls";
import { canModify, useMe } from "@/lib/me";

export const Route = createFileRoute("/_authed/scans/$scanId/")({
  component: ScanDetailPage,
});

type SortKey = "severity" | "tool" | "file" | "title";

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
  const me = useMe();

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
  const [sortKey, setSortKey] = useState<SortKey>("severity");
  const [sortDesc, setSortDesc] = useState(true);
  const [page, setPage] = useState(1);

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

  const visible = useMemo(() => {
    const lowerSearch = search.trim().toLowerCase();
    const out = findings.filter((f) => {
      if (filterTool && f.tool_name !== filterTool) return false;
      if (excludedSeverities.has(f.severity)) return false;
      if (excludedCategories.has(f.category)) return false;
      if (lowerSearch) {
        const hay = `${f.title} ${f.file_path} ${f.rule_id ?? ""}`.toLowerCase();
        if (!hay.includes(lowerSearch)) return false;
      }
      return true;
    });
    out.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "severity":
          cmp =
            (SEVERITY_RANK[a.severity] ?? 0) -
            (SEVERITY_RANK[b.severity] ?? 0);
          break;
        case "tool":
          cmp = a.tool_name.localeCompare(b.tool_name);
          break;
        case "file":
          cmp = a.file_path.localeCompare(b.file_path);
          if (cmp === 0) cmp = (a.line_start ?? 0) - (b.line_start ?? 0);
          break;
        case "title":
          cmp = a.title.localeCompare(b.title);
          break;
      }
      return sortDesc ? -cmp : cmp;
    });
    return out;
  }, [
    findings,
    filterTool,
    excludedSeverities,
    excludedCategories,
    search,
    sortKey,
    sortDesc,
  ]);

  const totalPages = Math.max(1, Math.ceil(visible.length / PAGE_SIZE));
  const pageClamped = Math.min(page, totalPages);
  const pageFindings = visible.slice(
    (pageClamped - 1) * PAGE_SIZE,
    pageClamped * PAGE_SIZE,
  );

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDesc(!sortDesc);
    else {
      setSortKey(key);
      setSortDesc(true);
    }
  };

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
    setPage(1);
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
    setPage(1);
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
          </h1>
          <p className="text-xs text-muted-foreground">
            {completed.length}/{selected.length} tools completed
            {failed.length > 0 && (
              <span className="text-red-400"> · {failed.length} failed</span>
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

      {scan.status === "cancelled" && (
        <div
          className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-800 dark:text-amber-200"
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
        <div className="mb-4 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
          <div className="font-medium">No scanners ran</div>
          <div className="text-xs text-amber-200/80 mt-0.5">
            This scan completed without running any tools. The container scanner
            backend may not be configured — try{" "}
            <code className="font-mono text-xs">wolf doctor</code> from the CLI, or
            open the Scanners tab to install missing tools.
          </div>
        </div>
      )}

      <ScanReleaseControls
        scan={scan}
        authorized={
          me.data?.role === "admin" && canModify(me.data, scan.user_id)
        }
      />

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
              <div className="mb-3 text-xs px-3 py-2 rounded-md bg-amber-500/10 border border-amber-500/20 text-amber-200 flex items-center justify-between">
                <span>
                  {serverSuppressed.toLocaleString()} finding{serverSuppressed === 1 ? "" : "s"} hidden by suppression rules (vendor/, node_modules/, testdata/, generated files, lockfiles).
                </span>
                <button
                  type="button"
                  onClick={() => setIncludeSuppressed(true)}
                  className="underline hover:text-amber-100"
                >
                  Show all
                </button>
              </div>
            )}
            {includeSuppressed && (
              <div className="mb-3 text-xs px-3 py-2 rounded-md bg-blue-500/10 border border-blue-500/20 text-blue-200 flex items-center justify-between">
                <span>Showing suppressed findings — these are typically noise.</span>
                <button
                  type="button"
                  onClick={() => setIncludeSuppressed(false)}
                  className="underline hover:text-blue-100"
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
                    setPage(1);
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
                    setPage(1);
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
                  {visible.length.toLocaleString()} of{" "}
                  {findings.length.toLocaleString()} shown · page {pageClamped} of{" "}
                  {totalPages}
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

            <div className="glass-card overflow-hidden">
              <table className="w-full text-sm">
                <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
                  <tr>
                    <SortableTh
                      label="Severity"
                      active={sortKey === "severity"}
                      desc={sortDesc}
                      onClick={() => toggleSort("severity")}
                      className="text-left"
                    />
                    <SortableTh
                      label="Tool"
                      active={sortKey === "tool"}
                      desc={sortDesc}
                      onClick={() => toggleSort("tool")}
                      className="text-left"
                    />
                    <SortableTh
                      label="Title"
                      active={sortKey === "title"}
                      desc={sortDesc}
                      onClick={() => toggleSort("title")}
                      className="text-left"
                    />
                    <SortableTh
                      label="File"
                      active={sortKey === "file"}
                      desc={sortDesc}
                      onClick={() => toggleSort("file")}
                      className="text-left"
                    />
                    <th className="text-right px-4 py-2 font-medium">Line</th>
                  </tr>
                </thead>
                <tbody>
                  {pageFindings.map((f) => (
                    <tr
                      key={f.id}
                      className="border-t border-border/30 table-row-hover"
                    >
                      <td className="px-4 py-2">
                        <SeverityBadge severity={f.severity} />
                      </td>
                      <td className="px-4 py-2 font-mono text-xs">
                        <button
                          type="button"
                          onClick={() => {
                            setFilterTool(f.tool_name);
                            setPage(1);
                          }}
                          className="hover:underline hover:text-foreground text-muted-foreground"
                          title={`Filter by ${f.tool_name}`}
                        >
                          {f.tool_name}
                        </button>
                      </td>
                      <td className="px-4 py-2">
                        <Link
                          to="/findings/$findingId"
                          params={{ findingId: f.id }}
                          className="hover:underline"
                        >
                          {f.title}
                        </Link>
                        {f.rule_id && (
                          <span className="ml-2 text-[10px] font-mono text-muted-foreground">
                            [{f.rule_id}]
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-2 font-mono text-xs text-muted-foreground truncate max-w-xs">
                        {f.file_path}
                      </td>
                      <td className="px-4 py-2 text-right text-muted-foreground tabular-nums">
                        {f.line_start || "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {totalPages > 1 && (
              <div className="flex items-center justify-between mt-3 text-xs">
                <button
                  type="button"
                  onClick={() => setPage(Math.max(1, pageClamped - 1))}
                  disabled={pageClamped <= 1}
                  className="h-8 px-3 rounded-md hover:bg-muted/40 disabled:opacity-30"
                >
                  ← Prev
                </button>
                <div className="text-muted-foreground tabular-nums">
                  Showing {(pageClamped - 1) * PAGE_SIZE + 1}–
                  {Math.min(pageClamped * PAGE_SIZE, visible.length)} of {visible.length.toLocaleString()}
                </div>
                <button
                  type="button"
                  onClick={() => setPage(Math.min(totalPages, pageClamped + 1))}
                  disabled={pageClamped >= totalPages}
                  className="h-8 px-3 rounded-md hover:bg-muted/40 disabled:opacity-30"
                >
                  Next →
                </button>
              </div>
            )}
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

function SortableTh({
  label,
  active,
  desc,
  onClick,
  className,
}: {
  label: string;
  active: boolean;
  desc: boolean;
  onClick: () => void;
  className?: string;
}) {
  return (
    <th className={`px-4 py-2 font-medium ${className ?? ""}`}>
      <button
        type="button"
        onClick={onClick}
        className={`inline-flex items-center gap-1 hover:text-foreground ${active ? "text-foreground" : ""}`}
      >
        {label}
        {active ? <span>{desc ? "▼" : "▲"}</span> : null}
      </button>
    </th>
  );
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
          className="inline-flex items-center gap-1 px-3 h-9 rounded-md bg-red-500/20 ring-1 ring-red-500/40 text-red-200 text-sm hover:bg-red-500/30 disabled:opacity-50"
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
      className="inline-flex items-center gap-1 px-3 h-9 rounded-md bg-red-500/10 ring-1 ring-red-500/30 text-red-300 text-sm hover:bg-red-500/20"
      title="Cancel the entire scan. Findings from tools that already completed are preserved."
    >
      <StopCircleIcon className="size-4" /> Cancel scan
    </button>
  );
}

// ToolsPanel — collapsible per-tool status with finding counts and
// expandable error details for failures. The "Tools" section is the
// answer to "which tools actually ran, which succeeded, which failed,
// and why" without leaving the scan page.
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
  // If the user lands on the page while running, then the scan
  // finishes — leave the panel however the user has it. If they
  // never touched it (open===true and scanActive flips), we keep
  // it open so partial-results don't disappear from view. The
  // collapsed-when-done default only applies to fresh loads of
  // completed scans.
  if (loading || !tools) {
    return (
      <section className="glass-card p-5">
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

  return (
    <section className="glass-card p-5">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        aria-controls={contentId}
        className="flex items-center justify-between w-full"
      >
        <div className="flex items-center gap-2">
          {open ? (
            <ChevronDownIcon className="size-4 text-muted-foreground" />
          ) : (
            <ChevronRightIcon className="size-4 text-muted-foreground" />
          )}
          <h2 className="text-base font-semibold">Tools ({tools.length})</h2>
        </div>
        <div className="text-xs text-muted-foreground tabular-nums">
          <span className="text-green-400">{completed.length} done</span>
          {failed.length > 0 && (
            <span className="text-red-400 ml-2">{failed.length} failed</span>
          )}
          {cancelled.length > 0 && (
            <span className="text-amber-300 ml-2">{cancelled.length} cancelled</span>
          )}
          {active.length > 0 && (
            <span className="text-blue-300 ml-2">{active.length} active</span>
          )}
        </div>
      </button>

      {open && (
        <div id={contentId}>
          {active.length > 0 && (
            <div className="mt-4">
              <div className="text-[11px] uppercase tracking-wide text-muted-foreground mb-2">
                Running / queued ({active.length})
              </div>
              <div className="space-y-1">
                {active.map((t) => (
                  <ToolRow key={t.name} tool={t} scanId={scanId} cancellable={scanActive} />
                ))}
              </div>
            </div>
          )}
          {failed.length > 0 && (
            <div className="mt-4">
              <div className="text-[11px] uppercase tracking-wide text-muted-foreground mb-2">
                Failed ({failed.length})
              </div>
              <div className="space-y-1">
                {failed.map((t) => (
                  <ToolRow key={t.name} tool={t} scanId={scanId} />
                ))}
              </div>
            </div>
          )}
          {cancelled.length > 0 && (
            <div className="mt-4">
              <div className="text-[11px] uppercase tracking-wide text-muted-foreground mb-2">
                Cancelled ({cancelled.length})
              </div>
              <div className="space-y-1">
                {cancelled.map((t) => (
                  <ToolRow key={t.name} tool={t} scanId={scanId} />
                ))}
              </div>
            </div>
          )}
          {completed.length > 0 && (
            <div className="mt-4">
              <div className="text-[11px] uppercase tracking-wide text-muted-foreground mb-2">
                Completed ({completed.length})
              </div>
              <div className="grid grid-cols-2 md:grid-cols-3 gap-x-3 gap-y-1">
                {completed
                  .sort((a, b) => b.finding_count - a.finding_count)
                  .map((t) => (
                    <ToolRow key={t.name} tool={t} scanId={scanId} compact />
                  ))}
              </div>
            </div>
          )}
        </div>
      )}
    </section>
  );
}

function ToolRow({
  tool,
  scanId,
  compact,
  cancellable,
}: {
  tool: ToolStatusEntry;
  scanId: string;
  compact?: boolean;
  cancellable?: boolean;
}) {
  const [showErr, setShowErr] = useState(false);
  const qc = useQueryClient();
  const cancel = useMutation({
    mutationFn: () =>
      api.delete(`/scans/${scanId}/tools/${encodeURIComponent(tool.name)}`),
    onSuccess: () => {
      // Refresh both the per-tool list and the scan summary so the
      // cancelled tool flips immediately + findings totals update.
      qc.invalidateQueries({ queryKey: ["scan-tools", scanId] });
      qc.invalidateQueries({ queryKey: ["scan", scanId] });
    },
  });
  const icon =
    tool.status === "completed" ? (
      <CheckCircle2Icon className="size-3.5 text-green-400" />
    ) : tool.status === "failed" ? (
      <XCircleIcon className="size-3.5 text-red-400" />
    ) : tool.status === "cancelled" ? (
      <StopCircleIcon className="size-3.5 text-amber-300" />
    ) : tool.status === "queued" ? (
      <LoaderIcon className="size-3.5 text-muted-foreground" />
    ) : (
      <LoaderIcon className="size-3.5 text-blue-300 animate-spin" />
    );

  if (compact) {
    return (
      <div className="flex items-center gap-1.5 text-xs py-0.5">
        {icon}
        <span className="font-mono truncate flex-1 min-w-0">{tool.name}</span>
        <span className="text-muted-foreground tabular-nums">
          {tool.finding_count > 0 ? tool.finding_count : "—"}
        </span>
      </div>
    );
  }

  return (
    <div className="text-sm">
      <div className="flex items-center gap-2 py-1">
        {icon}
        <span className="font-mono flex-1 min-w-0 truncate">{tool.name}</span>
        {tool.status === "queued" && (
          <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
            queued
          </span>
        )}
        {tool.finding_count > 0 && (
          <span className="text-xs text-muted-foreground tabular-nums">
            {tool.finding_count} findings
          </span>
        )}
        {(tool.status === "failed" || tool.status === "cancelled") && tool.error && (
          <button
            type="button"
            onClick={() => setShowErr(!showErr)}
            aria-expanded={showErr}
            className="text-[11px] underline text-muted-foreground hover:text-foreground"
          >
            {showErr ? "Hide error" : "Show error"}
          </button>
        )}
        {cancellable && (tool.status === "running" || tool.status === "queued") && (
          <button
            type="button"
            onClick={() => cancel.mutate()}
            disabled={cancel.isPending}
            aria-label={`Cancel ${tool.name}; the rest of the scan keeps going`}
            title={`Cancel ${tool.name}; the rest of the scan keeps going`}
            className="inline-flex items-center justify-center size-5 rounded hover:bg-red-500/15 text-muted-foreground hover:text-red-300 disabled:opacity-50"
          >
            <XIcon className="size-3.5" />
          </button>
        )}
      </div>
      {showErr && tool.error && (
        <pre className="text-[11px] font-mono text-red-300/80 bg-red-500/5 border border-red-500/10 rounded p-2 ml-5 mb-1 whitespace-pre-wrap break-all max-h-48 overflow-y-auto">
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
