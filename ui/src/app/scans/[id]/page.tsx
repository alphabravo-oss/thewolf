"use client";

import { useState, useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "next/navigation";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ClipboardCopy, Download } from "lucide-react";
import { StatusBadge } from "@/components/status-badge";
import { SeverityBadge } from "@/components/severity-badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import { CoverageCard } from "@/components/coverage-card";
import { Markdown } from "@/components/markdown";
import api, { getToken } from "@/lib/api";
import type { Scan, Finding, ScanArtifact, Severity, CoverageReport, AILog, ToolSummary, ScanRecommendation } from "@/lib/types";

interface ScanDetail extends Scan {
  findings: Finding[];
  artifacts: ScanArtifact[];
}

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api";

function triggerDownload(path: string) {
  const token = getToken();
  const separator = path.includes("?") ? "&" : "?";
  const url = `${API_BASE}${path}${token ? `${separator}token=${encodeURIComponent(token)}` : ""}`;
  const a = document.createElement("a");
  a.href = url;
  a.download = "";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

async function fetchAndCopy(path: string): Promise<boolean> {
  // Build an authenticated URL the same way triggerDownload does — this
  // guarantees the token is always present regardless of cookie scope.
  const token = getToken();
  const separator = path.includes("?") ? "&" : "?";
  const url = `${API_BASE}${path}${token ? `${separator}token=${encodeURIComponent(token)}` : ""}`;
  try {
    const res = await fetch(url, { credentials: "include" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const text = await res.text();
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

function scoreColor(score: number): string {
  if (score >= 8) return "text-red-600 dark:text-red-400";
  if (score >= 5) return "text-orange-600 dark:text-orange-400";
  if (score >= 3) return "text-yellow-600 dark:text-yellow-400";
  return "text-muted-foreground";
}

// Renders a single finding row with all scores and AI data
function FindingRow({ finding, defaultExpanded }: { finding: Finding; defaultExpanded?: boolean }) {
  const [expanded, setExpanded] = useState(defaultExpanded ?? false);

  return (
    <div className="border-b last:border-0">
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="flex items-start gap-3 py-2.5 px-2 w-full text-left hover:bg-muted/40 transition-colors"
      >
        <span className="text-muted-foreground text-xs mt-0.5 flex-shrink-0">
          {expanded ? "\u25BC" : "\u25B6"}
        </span>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <SeverityBadge severity={finding.severity} />
            <span className="text-sm font-medium truncate">{finding.title}</span>
          </div>
          <div className="flex items-center gap-3 mt-0.5 text-xs text-muted-foreground">
            <span className="font-mono">{finding.file_path}{finding.line_start > 0 ? `:${finding.line_start}` : ""}</span>
            {finding.rule_id && <span className="bg-muted px-1 py-0.5 rounded">{finding.rule_id}</span>}
          </div>
          {(finding.module_name || finding.function_name || finding.file_purpose) && (
            <div className="flex items-center gap-2 mt-0.5 text-xs text-muted-foreground">
              {finding.module_name && <span className="bg-blue-50 dark:bg-blue-950 px-1 py-0.5 rounded">{finding.module_name}</span>}
              {finding.function_name && <span className="bg-purple-50 dark:bg-purple-950 px-1 py-0.5 rounded">{finding.symbol_kind ? `${finding.symbol_kind}: ` : ""}{finding.function_name}</span>}
              {finding.file_purpose && <span className="bg-green-50 dark:bg-green-950 px-1 py-0.5 rounded">{finding.file_purpose}</span>}
              {finding.dependents_json && finding.dependents_json !== "[]" && (() => {
                try { const deps = JSON.parse(finding.dependents_json); return deps.length > 0 ? <span className="bg-orange-50 dark:bg-orange-950 px-1 py-0.5 rounded">{deps.length} dependents</span> : null; } catch { return null; }
              })()}
            </div>
          )}
        </div>
        <div className="flex items-center gap-3 flex-shrink-0 text-xs">
          {finding.composite_score > 0 && (
            <span className={`font-bold ${scoreColor(finding.composite_score / 10)}`} title="Composite score (0-100)">
              {Math.round(finding.composite_score)}
            </span>
          )}
          {finding.ai_context_score > 0 && (
            <span className={`${scoreColor(finding.ai_context_score)}`} title="AI context score (0-10)">
              AI: {finding.ai_context_score.toFixed(1)}
            </span>
          )}
          <Link
            href={`/findings/${finding.id}`}
            onClick={(e) => e.stopPropagation()}
            className="text-primary hover:underline"
          >
            Detail
          </Link>
        </div>
      </button>
      {expanded && (
        <div className="px-8 pb-3 space-y-2">
          {finding.description && finding.description !== finding.title && (
            <p className="text-xs text-muted-foreground">{finding.description}</p>
          )}
          {finding.code_snippet && (
            <div>
              <p className="text-xs font-medium text-muted-foreground mb-1">Code</p>
              <pre className="text-xs font-mono bg-muted/50 rounded p-2 overflow-x-auto max-h-40 whitespace-pre-wrap">
                {finding.code_snippet}
              </pre>
            </div>
          )}
          <div className="flex gap-6 text-xs">
            <div>
              <span className="text-muted-foreground">Tool Severity: </span>
              <span className="font-medium">{finding.tool_severity_score}</span>
            </div>
            <div>
              <span className="text-muted-foreground">Location Weight: </span>
              <span className="font-medium">{finding.location_weight.toFixed(1)}</span>
            </div>
            <div>
              <span className="text-muted-foreground">AI Context: </span>
              <span className={`font-medium ${scoreColor(finding.ai_context_score)}`}>
                {finding.ai_context_score > 0 ? finding.ai_context_score.toFixed(1) : "\u2014"}
              </span>
            </div>
            <div>
              <span className="text-muted-foreground">Composite: </span>
              <span className="font-bold">{Math.round(finding.composite_score)}</span>
            </div>
            {finding.cwe_id && (
              <div>
                <span className="text-muted-foreground">CWE: </span>
                <span className="font-medium">{finding.cwe_id}</span>
              </div>
            )}
          </div>
          {finding.ai_fix_suggestion && (
            <div className="bg-blue-50 dark:bg-blue-950/30 border border-blue-200 dark:border-blue-800 rounded p-2">
              <p className="text-xs font-medium text-blue-700 dark:text-blue-400 mb-1">AI Fix Suggestion</p>
              <p className="text-xs whitespace-pre-wrap">{finding.ai_fix_suggestion}</p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function parseScanArrays(s: ScanDetail): ScanDetail {
  if (typeof s.tools_selected === "string") {
    try { s.tools_selected = JSON.parse(s.tools_selected as unknown as string); } catch { s.tools_selected = []; }
  }
  if (typeof s.tools_completed === "string") {
    try { s.tools_completed = JSON.parse(s.tools_completed as unknown as string); } catch { s.tools_completed = []; }
  }
  if (typeof s.tools_failed === "string") {
    try { s.tools_failed = JSON.parse(s.tools_failed as unknown as string); } catch { s.tools_failed = []; }
  }
  return s;
}

export default function ScanResultsPage() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const [expandedAILog, setExpandedAILog] = useState<string | null>(null);
  const [expandedTool, setExpandedTool] = useState<string | null>(null);
  const [showRawOutput, setShowRawOutput] = useState<string | null>(null);
  const [toolOutput, setToolOutput] = useState<Record<string, string>>({});
  const [toolOutputLoading, setToolOutputLoading] = useState<string | null>(null);
  const [findingSort, setFindingSort] = useState<"score" | "severity">("score");
  const [expandedAISummary, setExpandedAISummary] = useState<string | null>(null);
  const [showZeroTools, setShowZeroTools] = useState(false);
  const [showAIDetails, setShowAIDetails] = useState(false);
  const [copiedTool, setCopiedTool] = useState<string | null>(null);
  const [copiedExport, setCopiedExport] = useState<string | null>(null);
  const [lazyFindings, setLazyFindings] = useState<Record<string, Finding[]>>({});
  const [lazyFindingsLoading, setLazyFindingsLoading] = useState<string | null>(null);
  const [cancellingAI, setCancellingAI] = useState(false);
  const [aiCancelled, setAICancelled] = useState(false);

  const handleCancelAI = useCallback(async () => {
    if (cancellingAI) return;
    setCancellingAI(true);
    try {
      await api.delete(`/scans/${id}`);
      setAICancelled(true);
      // Refetch scan data to pick up any state changes
      queryClient.invalidateQueries({ queryKey: ["scan", id] });
      queryClient.invalidateQueries({ queryKey: ["scan-ai-logs", id] });
    } catch {
      // Even if backend returns 409 (no active AI), treat as cancelled from UI perspective
      setAICancelled(true);
    }
    setCancellingAI(false);
  }, [id, cancellingAI, queryClient]);

  const handleCopyExport = useCallback(async (key: string, path: string) => {
    const ok = await fetchAndCopy(path);
    if (ok) {
      setCopiedExport(key);
      setTimeout(() => setCopiedExport(null), 2000);
    }
  }, []);

  const fetchToolFindings = useCallback(
    async (toolName: string) => {
      if (lazyFindings[toolName] !== undefined) return;
      setLazyFindingsLoading(toolName);
      try {
        const res = await api.get<Finding[]>(`/scans/${id}/findings?tool=${toolName}&per_page=5000`);
        setLazyFindings(prev => ({ ...prev, [toolName]: res.data ?? [] }));
      } catch {
        setLazyFindings(prev => ({ ...prev, [toolName]: [] }));
      }
      setLazyFindingsLoading(null);
    },
    [id, lazyFindings],
  );

  const fetchToolOutput = useCallback(
    async (toolName: string) => {
      if (toolOutput[toolName] !== undefined) return;
      setToolOutputLoading(toolName);
      try {
        const token = getToken();
        const headers: Record<string, string> = {};
        if (token) headers["Authorization"] = `Bearer ${token}`;
        const res = await fetch(`${API_BASE}/scans/${id}/tools/${toolName}/output`, {
          headers,
          credentials: "include",
        });
        const text = res.ok ? await res.text() : "(no raw output available)";
        setToolOutput((prev) => ({ ...prev, [toolName]: text }));
      } catch {
        setToolOutput((prev) => ({ ...prev, [toolName]: "(no raw output available)" }));
      } finally {
        setToolOutputLoading(null);
      }
    },
    [id, toolOutput],
  );

  const { data: scan, isLoading } = useQuery({
    queryKey: ["scan", id],
    queryFn: async () => {
      const scanRes = await api.get<ScanDetail>(`/scans/${id}`);
      const s = parseScanArrays(scanRes.data);
      s.findings = [];
      return s;
    },
    refetchInterval: (query) => {
      const s = query.state.data;
      if (!s) return false;
      if (s.status === "running" || s.status === "pending") return 5_000;
      // After completion, poll for AI summary but give up after 3 minutes
      if (s.status === "completed" && s.ai_enabled !== false && !s.ai_summary && !aiCancelled) {
        const completedAt = s.completed_at ? new Date(s.completed_at).getTime() : 0;
        if (completedAt && Date.now() - completedAt > 180_000) return false; // 3 min timeout
        return 5_000;
      }
      return false;
    },
  });

  // Fetch aggregate stats (severity/tool counts) without loading all findings.
  const { data: findingStats } = useQuery<{
    total: number;
    by_severity: Record<string, number>;
    by_tool: Record<string, number>;
  }>({
    queryKey: ["scan-finding-stats", id],
    queryFn: () => api.get(`/scans/${id}/findings/stats`).then((r) => (r.data as { total: number; by_severity: Record<string, number>; by_tool: Record<string, number> })),
    refetchInterval: (query) => {
      if (!scan) return false;
      if (scan.status === "running" || scan.status === "pending") return 5_000;
      if (!query.state.data) return 5_000;
      return false;
    },
  });

  const { data: coverage } = useQuery({
    queryKey: ["scan-coverage", id],
    queryFn: () => api.get<CoverageReport>(`/scans/${id}/coverage`).then((r) => r.data),
  });

  const { data: aiLogs = [] } = useQuery({
    queryKey: ["scan-ai-logs", id],
    queryFn: () => api.get<AILog[]>(`/scans/${id}/ai-logs`).then((r) => r.data ?? []),
    refetchInterval: (query) => {
      if (!scan) return false;
      if (scan.status === "running" || scan.status === "pending") return 5_000;
      // After completion, poll for AI logs if summary hasn't arrived
      if (scan.status === "completed" && scan.ai_enabled !== false && !scan.ai_summary) {
        const completedAt = scan.completed_at ? new Date(scan.completed_at).getTime() : 0;
        if (completedAt && Date.now() - completedAt > 180_000) return false;
        return 5_000;
      }
      return false;
    },
  });

  const { data: toolSummaries } = useQuery<ToolSummary[]>({
    queryKey: ["scan-tool-summaries", id],
    queryFn: () => api.get<ToolSummary[]>(`/scans/${id}/tool-summaries`).then(r => r.data ?? []),
    enabled: !!scan,
    refetchInterval: (query) => {
      if (!scan || scan.status === "pending") return false;
      if (scan.status === "running") return 5_000;
      // After completion, poll for summaries if AI is still running
      if (scan.ai_enabled !== false && !scan.ai_summary) {
        const completedAt = scan.completed_at ? new Date(scan.completed_at).getTime() : 0;
        if (completedAt && Date.now() - completedAt > 180_000) return false;
        return 5_000;
      }
      return false;
    },
  });

  // Auto-fetch tool output for failed tools so [FIX] lines are available.
  useQuery({
    queryKey: ["scan-failed-tool-outputs", id, scan?.tools_failed],
    queryFn: async () => {
      if (!scan?.tools_failed?.length) return null;
      const failed: string[] = Array.isArray(scan.tools_failed) ? scan.tools_failed : [];
      for (const tool of failed) {
        if (toolOutput[tool] !== undefined) continue;
        try {
          const token = getToken();
          const headers: Record<string, string> = {};
          if (token) headers["Authorization"] = `Bearer ${token}`;
          const res = await fetch(`${API_BASE}/scans/${id}/tools/${tool}/output`, {
            headers,
            credentials: "include",
          });
          const text = res.ok ? await res.text() : "";
          setToolOutput((prev) => ({ ...prev, [tool]: text }));
        } catch { /* ignore */ }
      }
      return null;
    },
    enabled: !!scan && Array.isArray(scan.tools_failed) && scan.tools_failed.length > 0,
  });

  const { data: recommendations } = useQuery<ScanRecommendation[]>({
    queryKey: ["scan-recommendations", id],
    queryFn: () => api.get<ScanRecommendation[]>(`/scans/${id}/recommendations`).then(r => r.data ?? []),
    enabled: !!scan,
    refetchInterval: (query) => {
      if (!scan || scan.status === "pending") return false;
      if (scan.status === "running") return 5_000;
      // After completion, poll for recommendations if AI is still running
      if (scan.ai_enabled !== false && !scan.ai_summary) {
        const completedAt = scan.completed_at ? new Date(scan.completed_at).getTime() : 0;
        if (completedAt && Date.now() - completedAt > 180_000) return false;
        return 5_000;
      }
      return false;
    },
  });

  if (isLoading) return <LoadingSpinner />;
  if (!scan) return <EmptyState title="Scan not found" />;

  const severities: Severity[] = ["critical", "high", "medium", "low", "info"];
  const sevOrder: Record<string, number> = { critical: 5, high: 4, medium: 3, low: 2, info: 1 };
  const findingsBySeverity = severities.map((sev) => ({
    severity: sev,
    count: findingStats?.by_severity?.[sev] ?? scan.findings?.filter((f) => f.severity === sev).length ?? 0,
  }));

  const toolBreakdown = scan.tools_selected?.map((tool) => {
    const loadedFindings = lazyFindings[tool] ?? [];
    const statsCount = findingStats?.by_tool?.[tool] ?? 0;
    const totalCount = Math.max(loadedFindings.length, statsCount);
    // Severity counts: use loaded findings if available, otherwise try tool summaries
    const ts = toolSummaries?.find(s => s.tool_name === tool);
    let sevCounts: Record<string, number>;
    if (loadedFindings.length > 0) {
      sevCounts = severities.reduce((acc, sev) => {
        acc[sev] = loadedFindings.filter((f) => f.severity === sev).length;
        return acc;
      }, {} as Record<string, number>);
    } else if (ts?.severity_counts) {
      try { sevCounts = JSON.parse(ts.severity_counts); } catch { sevCounts = {}; }
    } else {
      sevCounts = {};
    }
    return {
      tool,
      completed: scan.tools_completed?.includes(tool) ?? false,
      failed: scan.tools_failed?.includes(tool) ?? false,
      findings: loadedFindings,
      totalCount,
      sevCounts,
    };
  });

  const sortFindings = (findings: Finding[]) => {
    return [...findings].sort((a, b) => {
      if (findingSort === "score") {
        const scoreDiff = (b.composite_score ?? 0) - (a.composite_score ?? 0);
        if (scoreDiff !== 0) return scoreDiff;
        // Tiebreak by severity then file path
        const sevDiff = (sevOrder[b.severity] ?? 0) - (sevOrder[a.severity] ?? 0);
        if (sevDiff !== 0) return sevDiff;
        return (a.file_path ?? "").localeCompare(b.file_path ?? "");
      }
      const sevDiff = (sevOrder[b.severity] ?? 0) - (sevOrder[a.severity] ?? 0);
      if (sevDiff !== 0) return sevDiff;
      // Tiebreak by composite score then file path
      const scoreDiff = (b.composite_score ?? 0) - (a.composite_score ?? 0);
      if (scoreDiff !== 0) return scoreDiff;
      return (a.file_path ?? "").localeCompare(b.file_path ?? "");
    });
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">
            Scan Results{scan.repo?.name ? ` \u2014 ${scan.repo.name}` : ""}
          </h1>
          <div className="flex items-center gap-3 mt-1">
            <StatusBadge status={scan.status} />
            {scan.repo?.name && (
              <span className="text-sm font-medium">{scan.repo.name}</span>
            )}
            <span className="text-muted-foreground text-sm">
              Branch: {scan.branch}
            </span>
          </div>
          <div className="flex items-center gap-4 mt-2 text-sm text-muted-foreground">
            {scan.tools_selected && (
              <span>
                {scan.tools_selected.length} tools ({scan.tools_completed?.length ?? 0} completed
                {scan.tools_failed?.length > 0 && `, ${scan.tools_failed.length} failed`})
              </span>
            )}
            {scan.started_at && (
              <span>Started: {new Date(scan.started_at).toLocaleString()}</span>
            )}
            {scan.completed_at && (
              <span>Ended: {new Date(scan.completed_at).toLocaleString()}</span>
            )}
            {scan.started_at && (
              <span>
                Duration:{" "}
                {(() => {
                  const s = new Date(scan.started_at).getTime();
                  const e = scan.completed_at
                    ? new Date(scan.completed_at).getTime()
                    : Date.now();
                  const sec = Math.round((e - s) / 1000);
                  if (sec < 60) return `${sec}s`;
                  return `${Math.floor(sec / 60)}m ${sec % 60}s`;
                })()}
              </span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {scan.status === "running" && (
            <Button asChild>
              <Link href={`/scans/${id}/live`}>View Live</Link>
            </Button>
          )}
          {!scan.ai_summary && !aiCancelled && scan.status === "completed" && aiLogs.length > 0 && (() => {
            const completedAt = scan.completed_at ? new Date(scan.completed_at).getTime() : 0;
            const isStale = completedAt > 0 && Date.now() - completedAt > 180_000;
            if (isStale) return null; // AI is already dead, no point showing cancel
            return (
              <Button
                variant="destructive"
                size="sm"
                onClick={handleCancelAI}
                disabled={cancellingAI}
              >
                {cancellingAI ? "Cancelling..." : "Cancel AI"}
              </Button>
            );
          })()}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline">Export</Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-72">
              <DropdownMenuLabel>Findings Data</DropdownMenuLabel>
              <DropdownMenuItem onClick={() => triggerDownload(`/findings/export?scan_id=${id}&format=csv`)}>
                <Download className="w-4 h-4 mr-2 shrink-0" />
                <span className="flex-1">Download Findings CSV</span>
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => triggerDownload(`/findings/export?scan_id=${id}&format=json`)}>
                <Download className="w-4 h-4 mr-2 shrink-0" />
                <span className="flex-1">Download Findings JSON</span>
              </DropdownMenuItem>
              <DropdownMenuItem onClick={(e) => { e.preventDefault(); handleCopyExport("json", `/findings/export?scan_id=${id}&format=json`); }}>
                <ClipboardCopy className="w-4 h-4 mr-2 shrink-0" />
                <span className="flex-1">{copiedExport === "json" ? "Copied!" : "Copy Findings JSON"}</span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>Reports</DropdownMenuLabel>
              <DropdownMenuItem onClick={() => triggerDownload(`/scans/${id}/report`)}>
                <Download className="w-4 h-4 mr-2 shrink-0" />
                <span className="flex-1">Download Markdown Report</span>
              </DropdownMenuItem>
              <DropdownMenuItem onClick={(e) => { e.preventDefault(); handleCopyExport("md", `/scans/${id}/report`); }}>
                <ClipboardCopy className="w-4 h-4 mr-2 shrink-0" />
                <span className="flex-1">{copiedExport === "md" ? "Copied!" : "Copy Markdown Report"}</span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>Machine-Readable</DropdownMenuLabel>
              <DropdownMenuItem onClick={() => triggerDownload(`/scans/${id}/sarif`)}>
                <Download className="w-4 h-4 mr-2 shrink-0" />
                <span className="flex-1">Download SARIF</span>
              </DropdownMenuItem>
              <DropdownMenuItem onClick={(e) => { e.preventDefault(); handleCopyExport("sarif", `/scans/${id}/sarif`); }}>
                <ClipboardCopy className="w-4 h-4 mr-2 shrink-0" />
                <span className="flex-1">{copiedExport === "sarif" ? "Copied!" : "Copy SARIF to Clipboard"}</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {coverage && <CoverageCard coverage={coverage} />}

      <div className="grid gap-4 md:grid-cols-6">
        {findingsBySeverity.map(({ severity, count }) => (
          <Card key={severity}>
            <CardContent className="pt-6 text-center">
              <SeverityBadge severity={severity} />
              <p className="text-2xl font-bold mt-2">{count}</p>
            </CardContent>
          </Card>
        ))}
        {/* AI Assessment compact card */}
        {scan.ai_enabled !== false && (() => {
          const aiDone = !!scan.ai_summary;
          const aiHasErrors = aiLogs.some(l => l.error);
          const totalToolsWithFindings = findingStats?.by_tool ? Object.keys(findingStats.by_tool).length : 0;
          const assessedTools = new Set(aiLogs.filter(l => l.phase === "tool_assess" && !l.error).map(l => l.tool_name)).size;
          const hasSummaryLog = aiLogs.some(l => l.phase === "summary" && !l.error);
          const completedAt = scan.completed_at ? new Date(scan.completed_at).getTime() : 0;
          const aiStale = !aiDone && !aiCancelled && completedAt > 0 && Date.now() - completedAt > 180_000;
          const aiRunning = !aiDone && !aiStale && !aiCancelled;
          const totalTokens = aiLogs.reduce((s, l) => s + (l.prompt_tokens || 0) + (l.response_tokens || 0), 0);
          const totalDuration = aiLogs.reduce((s, l) => s + l.duration_ms, 0);
          const totalCost = aiLogs.reduce((s, l) => s + (l.cost_usd || 0), 0);
          const toolProgress = totalToolsWithFindings > 0 ? `${assessedTools}/${totalToolsWithFindings}` : aiLogs.length > 0 ? `${aiLogs.length} calls` : "";
          return (
          <Card
            className={`cursor-pointer transition-colors ${aiDone ? "border-blue-200 dark:border-blue-800" : aiCancelled ? "border-orange-200 dark:border-orange-800" : aiStale ? "border-orange-200 dark:border-orange-800" : aiHasErrors ? "border-red-200 dark:border-red-800" : "border-yellow-200 dark:border-yellow-800"}`}
            onClick={() => setShowAIDetails(prev => !prev)}
          >
            <CardContent className="pt-6 text-center">
              <div className="flex items-center justify-center gap-1.5 mb-2">
                {aiDone && !aiHasErrors ? (
                  <span className="text-green-600">&#10003;</span>
                ) : aiHasErrors && aiDone ? (
                  <span className="text-orange-500">&#9888;</span>
                ) : aiCancelled ? (
                  <span className="text-orange-500">&#10007;</span>
                ) : aiStale ? (
                  <span className="text-orange-500">&#9888;</span>
                ) : aiHasErrors && !aiDone ? (
                  <span className="text-red-600">&#10007;</span>
                ) : aiRunning ? (
                  <span className="animate-spin inline-block w-3.5 h-3.5 border-2 border-blue-600 border-t-transparent rounded-full" />
                ) : (
                  <span className="text-muted-foreground">&#8943;</span>
                )}
                <span className="text-xs font-bold">AI</span>
              </div>
              <p className="text-sm font-bold">
                {aiDone
                  ? toolProgress || "Done"
                  : aiCancelled
                    ? toolProgress || "Cancelled"
                  : aiStale
                    ? toolProgress || "Interrupted"
                    : assessedTools > 0
                      ? hasSummaryLog
                        ? "Summarizing"
                        : `${assessedTools}/${totalToolsWithFindings} tools`
                      : aiLogs.length > 0
                        ? `${aiLogs.length} calls...`
                        : "Pending"}
              </p>
              {aiLogs.length > 0 && (
                <p className="text-[10px] text-muted-foreground mt-0.5">
                  {(() => {
                    const inTok = aiLogs.reduce((s, l) => s + (l.prompt_tokens || 0), 0);
                    const outTok = aiLogs.reduce((s, l) => s + (l.response_tokens || 0), 0);
                    if (totalCost > 0) return `$${totalCost.toFixed(4)}`;
                    if (totalTokens > 0) return `${(inTok / 1000).toFixed(1)}k in / ${(outTok / 1000).toFixed(1)}k out`;
                    return `${(totalDuration / 1000).toFixed(1)}s`;
                  })()}
                </p>
              )}
            </CardContent>
          </Card>
          );
        })()}
      </div>

      {/* AI Details (shown when AI card is clicked) */}
      {showAIDetails && aiLogs.length > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">AI Assessment Details</CardTitle>
          </CardHeader>
          <CardContent>
            {(() => {
              const totalCost = aiLogs.reduce((s, l) => s + (l.cost_usd || 0), 0);
              return (
              <div className={`grid grid-cols-2 gap-4 text-center ${totalCost > 0 ? "md:grid-cols-6" : "md:grid-cols-5"}`}>
                <div>
                  <p className="text-xs text-muted-foreground">Provider / Model</p>
                  <p className="text-sm font-medium">{aiLogs[0]?.provider} / {aiLogs[0]?.model}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">AI Calls</p>
                  <p className="text-sm font-medium">{aiLogs.length}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Input Tokens</p>
                  <p className="text-sm font-medium">{aiLogs.reduce((s, l) => s + (l.prompt_tokens || 0), 0).toLocaleString()}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Output Tokens</p>
                  <p className="text-sm font-medium">{aiLogs.reduce((s, l) => s + (l.response_tokens || 0), 0).toLocaleString()}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Total Duration</p>
                  <p className="text-sm font-medium">{(aiLogs.reduce((s, l) => s + l.duration_ms, 0) / 1000).toFixed(1)}s</p>
                </div>
                {totalCost > 0 && (
                  <div>
                    <p className="text-xs text-muted-foreground">Cost</p>
                    <p className="text-sm font-medium">${totalCost.toFixed(4)}</p>
                  </div>
                )}
              </div>
              );
            })()}
            {aiLogs.length > 0 && (
              <div className="mt-3 border-t pt-3">
                <p className="text-xs text-muted-foreground mb-2">Per-call breakdown</p>
                <div className="space-y-1">
                  {aiLogs.map((log) => (
                    <div key={log.id} className={`flex items-center justify-between text-xs px-2 py-1.5 rounded ${log.error ? "bg-red-50 dark:bg-red-950/20" : "bg-muted/30"}`}>
                      <div className="flex items-center gap-2">
                        <span className={`inline-block w-1.5 h-1.5 rounded-full ${log.error ? "bg-red-500" : "bg-green-500"}`} />
                        <span className="font-medium capitalize">{log.phase}</span>
                        {log.tool_name && <span className="font-mono text-muted-foreground">{log.tool_name}</span>}
                      </div>
                      <div className="flex items-center gap-3 text-muted-foreground">
                        <span>{(log.prompt_tokens || 0).toLocaleString()} in</span>
                        <span>{(log.response_tokens || 0).toLocaleString()} out</span>
                        <span>{(log.duration_ms / 1000).toFixed(1)}s</span>
                        {log.cost_usd > 0 && <span>${log.cost_usd.toFixed(4)}</span>}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* AI Summary (shown below severity cards when available) */}
      {scan.ai_summary && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">AI Summary</CardTitle>
          </CardHeader>
          <CardContent>
            <Markdown className="text-sm">{scan.ai_summary}</Markdown>
          </CardContent>
        </Card>
      )}

      {recommendations && recommendations.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Recommendations</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {recommendations
              .sort((a, b) => a.priority - b.priority)
              .map((rec) => {
                const tools: string[] = (() => { try { return JSON.parse(rec.affected_tools); } catch { return []; } })();
                return (
                  <div key={rec.id} className="border rounded-lg p-3 space-y-1.5">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className={`inline-flex items-center justify-center w-6 h-6 rounded-full text-xs font-bold text-white ${
                        rec.priority <= 1 ? "bg-red-500" : rec.priority <= 2 ? "bg-orange-500" : rec.priority <= 3 ? "bg-yellow-500" : "bg-blue-500"
                      }`}>
                        {rec.priority}
                      </span>
                      <span className="text-xs font-medium bg-muted px-2 py-0.5 rounded capitalize">{rec.category}</span>
                      <span className="font-medium text-sm">{rec.title}</span>
                      <span className={`ml-auto text-xs px-2 py-0.5 rounded ${
                        rec.effort_estimate === "low" ? "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300" :
                        rec.effort_estimate === "high" ? "bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300" :
                        "bg-yellow-100 text-yellow-700 dark:bg-yellow-900 dark:text-yellow-300"
                      }`}>
                        {rec.effort_estimate} effort
                      </span>
                    </div>
                    {rec.description && <Markdown className="text-sm text-muted-foreground">{rec.description}</Markdown>}
                    {tools.length > 0 && (
                      <div className="flex gap-1 flex-wrap">
                        {tools.map((t) => (
                          <span key={t} className="text-xs bg-muted px-1.5 py-0.5 rounded font-mono">{t}</span>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
          </CardContent>
        </Card>
      )}

      {/* Findings by Tool — the main content area */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-sm">Findings by Tool</CardTitle>
          <div className="flex items-center gap-2 text-xs">
            <span className="text-muted-foreground">Sort:</span>
            <button
              type="button"
              onClick={() => setFindingSort("score")}
              className={`px-2 py-0.5 rounded ${findingSort === "score" ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:text-foreground"}`}
            >
              Score
            </button>
            <button
              type="button"
              onClick={() => setFindingSort("severity")}
              className={`px-2 py-0.5 rounded ${findingSort === "severity" ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:text-foreground"}`}
            >
              Severity
            </button>
          </div>
        </CardHeader>
        <CardContent>
          <div className="space-y-1">
            {toolBreakdown?.filter(t => t.totalCount > 0 || t.failed).map(({ tool, completed, failed, findings, totalCount, sevCounts }) => (
              <div key={tool} className="border rounded-md">
                {/* Tool header */}
                <button
                  type="button"
                  onClick={() => {
                    const next = expandedTool === tool ? null : tool;
                    setExpandedTool(next);
                    if (next && findings.length === 0 && totalCount > 0) {
                      fetchToolFindings(tool);
                    }
                  }}
                  className="flex items-center justify-between py-2.5 px-3 w-full text-left hover:bg-muted/40 rounded-md transition-colors"
                >
                  <div className="flex items-center gap-2">
                    <span className="text-muted-foreground text-xs">
                      {expandedTool === tool ? "\u25BC" : "\u25B6"}
                    </span>
                    <span className="font-mono text-sm font-medium">{tool}</span>
                    <StatusBadge status={failed ? "failed" : completed ? "completed" : "running"} />
                    {(() => {
                      if (totalCount === 0) return null;
                      if (scan.ai_enabled === false && !scan.ai_summary && aiLogs.length === 0) return null;
                      const hasToolSummary = toolSummaries?.some(s => s.tool_name === tool && s.summary_text);
                      const hasScanSummary = toolBreakdown?.length === 1 && scan.ai_summary;
                      const aiLog = aiLogs.find(l => l.tool_name === tool);
                      const aiError = aiLog?.error;
                      if (hasToolSummary || hasScanSummary) {
                        return <span className="text-xs font-bold text-green-600 dark:text-green-400" title="AI analysis complete">AI</span>;
                      }
                      if (aiError) {
                        return <span className="text-xs font-bold text-red-600 dark:text-red-400" title={`AI analysis failed: ${aiError}`}>AI</span>;
                      }
                      if (completed && !scan.ai_summary) {
                        return <span className="text-xs font-bold text-yellow-500 animate-pulse" title="AI analysis in progress">AI</span>;
                      }
                      return null;
                    })()}
                  </div>
                  <div className="flex items-center gap-2">
                    {/* Mini severity breakdown */}
                    {sevCounts.critical > 0 && <span className="text-xs px-1.5 py-0.5 rounded bg-red-100 dark:bg-red-900/40 text-red-700 dark:text-red-400 font-medium">{sevCounts.critical}C</span>}
                    {sevCounts.high > 0 && <span className="text-xs px-1.5 py-0.5 rounded bg-orange-100 dark:bg-orange-900/40 text-orange-700 dark:text-orange-400 font-medium">{sevCounts.high}H</span>}
                    {sevCounts.medium > 0 && <span className="text-xs px-1.5 py-0.5 rounded bg-yellow-100 dark:bg-yellow-900/40 text-yellow-700 dark:text-yellow-400 font-medium">{sevCounts.medium}M</span>}
                    {sevCounts.low > 0 && <span className="text-xs px-1.5 py-0.5 rounded bg-blue-100 dark:bg-blue-900/40 text-blue-700 dark:text-blue-400 font-medium">{sevCounts.low}L</span>}
                    {sevCounts.info > 0 && <span className="text-xs px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400 font-medium">{sevCounts.info}I</span>}
                    <span className="text-sm text-muted-foreground ml-1">{totalCount} total</span>
                  </div>
                </button>

                {/* Expanded: findings list + raw output toggle */}
                {expandedTool === tool && (
                  <div className="border-t">
                    {/* AI Summary — collapsible, per-tool or fallback to scan-level */}
                    {(() => {
                      const ts = toolSummaries?.find(s => s.tool_name === tool);
                      const summaryText = ts?.summary_text || (toolBreakdown?.length === 1 ? scan.ai_summary : null);
                      if (!summaryText) return null;
                      return (
                        <div className="border-b">
                          <div className="flex items-center">
                            <button
                              type="button"
                              onClick={() => setExpandedAISummary(prev => prev === tool ? null : tool)}
                              className="flex items-start gap-3 py-2.5 px-2 flex-1 text-left hover:bg-muted/40 transition-colors"
                            >
                              <span className="text-muted-foreground text-xs mt-0.5 flex-shrink-0">
                                {expandedAISummary === tool ? "▼" : "▶"}
                              </span>
                              <span className="text-sm font-medium">AI Summary</span>
                            </button>
                            <button
                              type="button"
                              onClick={(e) => {
                                e.stopPropagation();
                                navigator.clipboard.writeText(summaryText);
                                setCopiedTool(tool);
                                setTimeout(() => setCopiedTool(null), 2000);
                              }}
                              className="px-2 py-1 mr-2 text-xs text-muted-foreground hover:text-foreground transition-colors"
                              title="Copy AI summary to clipboard"
                            >
                              {copiedTool === tool ? "Copied!" : "Copy"}
                            </button>
                          </div>
                          {expandedAISummary === tool && (
                            <div className="px-8 pb-3">
                              <Markdown className="text-sm">{summaryText}</Markdown>
                            </div>
                          )}
                        </div>
                      );
                    })()}
                    {/* How to fix — shown for failed tools with diagnostic output */}
                    {failed && toolOutput[tool] && (() => {
                      const fixLines = toolOutput[tool]
                        .split("\n")
                        .filter(l => l.startsWith("[FIX]"))
                        .map(l => l.replace(/^\[FIX\]\s*\S+:\s*/, ""));
                      if (fixLines.length === 0) return null;
                      return (
                        <div className="border-b bg-blue-50/50 dark:bg-blue-950/20 px-4 py-3 space-y-1">
                          <p className="text-xs font-semibold text-blue-700 dark:text-blue-400">How to fix</p>
                          {fixLines.map((line, i) => (
                            <p key={i} className="text-xs text-blue-600 dark:text-blue-400 font-mono">
                              {line}
                            </p>
                          ))}
                        </div>
                      );
                    })()}

                    {/* Findings list */}
                    {(() => {
                      const displayFindings = findings.length > 0 ? findings : (lazyFindings[tool] ?? []);
                      if (lazyFindingsLoading === tool) {
                        return <p className="text-xs text-muted-foreground px-3 py-4">Loading findings...</p>;
                      }
                      if (displayFindings.length > 0) {
                        return (
                          <div className="divide-y">
                            {sortFindings(displayFindings).map((f) => (
                              <FindingRow key={f.id} finding={f} />
                            ))}
                          </div>
                        );
                      }
                      return <p className="text-xs text-muted-foreground px-3 py-4">No findings from this tool.</p>;
                    })()}

                    {/* Raw output toggle */}
                    <div className="border-t px-3 py-2">
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (showRawOutput === tool) {
                            setShowRawOutput(null);
                          } else {
                            setShowRawOutput(tool);
                            fetchToolOutput(tool);
                          }
                        }}
                        className="text-xs text-muted-foreground hover:text-foreground transition-colors"
                      >
                        {showRawOutput === tool ? "\u25BC Hide" : "\u25B6 Show"} Raw Tool Output
                      </button>
                      {showRawOutput === tool && (
                        <div className="mt-2 relative">
                          <div className="absolute top-1 right-1 flex gap-1">
                            <button
                              type="button"
                              onClick={(e) => {
                                e.stopPropagation();
                                triggerDownload(`/scans/${id}/tools/${tool}/output`);
                              }}
                              className="text-[10px] text-primary hover:underline bg-background/80 px-1.5 py-0.5 rounded"
                            >
                              Download
                            </button>
                          </div>
                          {toolOutputLoading === tool ? (
                            <p className="text-xs text-muted-foreground py-2">Loading...</p>
                          ) : (
                            <pre className="text-xs font-mono whitespace-pre-wrap bg-muted/30 rounded p-2 max-h-64 overflow-auto">
                              {toolOutput[tool] || "(no output)"}
                            </pre>
                          )}
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </div>
            ))}
            {/* Zero findings tools — collapsed */}
            {(() => {
              const zeroTools = toolBreakdown?.filter(t => t.totalCount === 0 && !t.failed) ?? [];
              if (zeroTools.length === 0) return null;
              return (
                <div className="mt-3">
                  <button
                    type="button"
                    onClick={() => setShowZeroTools(prev => !prev)}
                    className="flex items-center gap-2 text-xs text-muted-foreground hover:text-foreground transition-colors py-1"
                  >
                    <span>{showZeroTools ? "\u25BC" : "\u25B6"}</span>
                    <span>Zero findings ({zeroTools.length} tools)</span>
                  </button>
                  {showZeroTools && (
                    <div className="mt-1 space-y-0.5">
                      {zeroTools.map(({ tool, completed }) => (
                        <div key={tool} className="flex items-center gap-2 py-1.5 px-3 rounded-md bg-muted/30">
                          <span className="font-mono text-xs text-muted-foreground">{tool}</span>
                          <StatusBadge status={completed ? "completed" : "running"} />
                          <span className="text-xs text-muted-foreground ml-auto">0 findings</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })()}
          </div>
        </CardContent>
      </Card>

      {/* AI Call Logs */}
      {aiLogs.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">AI Call Logs ({aiLogs.length} calls)</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {aiLogs.map((log) => (
                <div key={log.id} className={`border rounded-md ${log.error ? "border-red-300 dark:border-red-800" : "border-green-300 dark:border-green-800"}`}>
                  <button
                    type="button"
                    onClick={() => setExpandedAILog(prev => prev === log.id ? null : log.id)}
                    className="flex items-center justify-between w-full px-3 py-2 text-left hover:bg-muted/50 rounded-md transition-colors"
                  >
                    <div className="flex items-center gap-2">
                      <span className="text-xs text-muted-foreground">
                        {expandedAILog === log.id ? "\u25BC" : "\u25B6"}
                      </span>
                      <span className={`inline-block w-2 h-2 rounded-full ${log.error ? "bg-red-500" : "bg-green-500"}`} />
                      <span className="text-sm font-medium capitalize">{log.phase}</span>
                      {log.tool_name && (
                        <span className="text-xs font-mono text-muted-foreground bg-muted px-1.5 py-0.5 rounded">
                          {log.tool_name}
                        </span>
                      )}
                    </div>
                    <div className="flex items-center gap-3 text-xs text-muted-foreground">
                      <span>{log.provider}{log.model ? `/${log.model}` : ""}</span>
                      <span>{(log.duration_ms / 1000).toFixed(1)}s</span>
                      <span>{log.prompt_tokens + log.response_tokens} tokens</span>
                    </div>
                  </button>
                  {expandedAILog === log.id && (
                    <div className="px-3 pb-3 space-y-2">
                      {log.error && (
                        <div className="bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded p-2">
                          <p className="text-xs font-medium text-red-700 dark:text-red-400">Error</p>
                          <p className="text-xs text-red-600 dark:text-red-400 whitespace-pre-wrap">{log.error}</p>
                        </div>
                      )}
                      <details className="group">
                        <summary className="text-xs font-medium cursor-pointer text-muted-foreground hover:text-foreground">
                          Prompt ({log.prompt_tokens} tokens)
                        </summary>
                        <pre className="mt-1 text-xs font-mono whitespace-pre-wrap bg-muted/50 rounded p-2 max-h-64 overflow-auto">
                          {log.prompt}
                        </pre>
                      </details>
                      {log.response && (
                        <details className="group">
                          <summary className="text-xs font-medium cursor-pointer text-muted-foreground hover:text-foreground">
                            Response ({log.response_tokens} tokens)
                          </summary>
                          <pre className="mt-1 text-xs font-mono whitespace-pre-wrap bg-muted/50 rounded p-2 max-h-64 overflow-auto">
                            {log.response}
                          </pre>
                        </details>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

    </div>
  );
}
