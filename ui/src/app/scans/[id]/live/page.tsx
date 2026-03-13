"use client";

import { useState, useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useParams, useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { StatusBadge } from "@/components/status-badge";
import { useSSE } from "@/lib/sse";
import api, { getToken } from "@/lib/api";
import type { CollectionToolsResponse, RepoDetection, Scan, ScanProgressEvent, ScanToolStatus, SSEEvent, ToolLogEvent } from "@/lib/types";

interface ToolProgress {
  name: string;
  status: "pending" | "running" | "completed" | "failed";
  findings: number;
  elapsed: number;
  progress: number;
  error?: string;
}

function parseToolArray(val: string[] | string | undefined | null): string[] {
  if (!val) return [];
  if (Array.isArray(val)) return val;
  try { const parsed = JSON.parse(val); return Array.isArray(parsed) ? parsed : []; } catch { return []; }
}

export default function ScanLivePage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [tools, setTools] = useState<Map<string, ToolProgress>>(new Map());
  const [totalFindings, setTotalFindings] = useState(0);
  const [elapsed, setElapsed] = useState(0);
  const startTimeRef = useRef<number | null>(null);
  const [toolLogs, setToolLogs] = useState<Map<string, string[]>>(new Map());
  const [activeLogTool, setActiveLogTool] = useState<string | null>(null);
  const logEndRef = useRef<HTMLDivElement>(null);
  const logContainerRef = useRef<HTMLDivElement>(null);
  const userScrolledRef = useRef(false);
  const [repoSummary, setRepoSummary] = useState<RepoDetection[]>([]);
  const [scanMeta, setScanMeta] = useState<{ repoName?: string; branch?: string } | null>(null);
  const [scanStatus, setScanStatus] = useState<string>("pending");
  const [sseEnabled, setSSEEnabled] = useState(true);
  const [aiProgress, setAIProgress] = useState<{
    phase: string;
    tool?: string;
    step?: number;
    totalSteps?: number;
    progressPct: number;
  } | null>(null);
  const [aiCallLogs, setAICallLogs] = useState<{
    provider: string;
    model: string;
    phase: string;
    tool: string;
    durationMs: number;
    error: string;
  }[]>([]);

  // Fetch per-tool finding counts from /api/scans/{id}/tools
  const fetchToolStatuses = (scanId: string) => {
    api
      .get<ScanToolStatus[]>(`/scans/${scanId}/tools`)
      .then((res) => {
        const statuses = res.data ?? [];
        setTools((prev) => {
          const next = new Map(prev);
          let sum = 0;
          for (const ts of statuses) {
            const existing = next.get(ts.name);
            // Update finding count and status, preserve SSE-provided progress
            next.set(ts.name, {
              name: ts.name,
              status: ts.status as "running" | "completed" | "failed",
              findings: ts.finding_count,
              elapsed: existing?.elapsed ?? 0,
              progress: ts.status === "completed" || ts.status === "failed" ? 100 : existing?.progress ?? 0,
            });
            sum += ts.finding_count;
          }
          setTotalFindings(sum);
          return next;
        });
        return statuses;
      })
      .catch(() => []);
  };

  // Fetch tool output from persisted artifacts for completed scans
  const fetchToolOutputs = (scanId: string) => {
    const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api";
    api
      .get<ScanToolStatus[]>(`/scans/${scanId}/tools`)
      .then((res) => {
        const statuses = res.data ?? [];
        for (const ts of statuses) {
          if (!ts.has_output) continue;
          const token = getToken();
          const headers: Record<string, string> = {};
          if (token) headers["Authorization"] = `Bearer ${token}`;
          fetch(`${API_BASE}/scans/${scanId}/tools/${ts.name}/output`, {
            headers,
            credentials: "include",
          })
            .then((r) => (r.ok ? r.text() : null))
            .then((text) => {
              if (text) {
                const lines = text.split("\n").slice(-500);
                setToolLogs((prev) => {
                  const next = new Map(prev);
                  next.set(ts.name, lines);
                  return next;
                });
                setActiveLogTool((current) => current ?? ts.name);
              }
            })
            .catch(() => {});
        }
      })
      .catch(() => {});
  };

  // Poll scan details with useQuery
  const { data: scanData } = useQuery({
    queryKey: ["scan-live", id],
    queryFn: () => api.get<Scan>(`/scans/${id}`).then((r) => r.data),
    refetchInterval: (query) => {
      const s = query.state.data;
      if (s && (s.status === "completed" || s.status === "failed" || s.status === "cancelled")) {
        return false;
      }
      return 3_000;
    },
  });

  // Hydrate state from scan data
  useEffect(() => {
    if (!scanData) return;
    const scan = scanData;
    setScanMeta({ repoName: scan.repo?.name, branch: scan.branch });
    setScanStatus(scan.status);

    const selected = parseToolArray(scan.tools_selected);
    const completed = parseToolArray(scan.tools_completed);
    const failed = parseToolArray(scan.tools_failed);
    const running = parseToolArray(scan.tools_running);

    if (selected.length > 0) {
      setTools((prev) => {
        const next = new Map(prev);
        for (const name of selected) {
          const existing = next.get(name);
          let status: "pending" | "running" | "completed" | "failed" = "pending";
          if (completed.includes(name)) status = "completed";
          else if (failed.includes(name)) status = "failed";
          else if (running.includes(name)) status = "running";
          // Only update if no existing entry or if new status is more advanced
          if (!existing || (existing.status === "pending" && status !== "pending")) {
            next.set(name, {
              name,
              status,
              findings: existing?.findings ?? 0,
              elapsed: existing?.elapsed ?? 0,
              progress: status === "completed" || status === "failed" ? 100 : existing?.progress ?? 0,
            });
          }
        }
        return next;
      });
    }

    if (scan.finding_count > 0) {
      setTotalFindings((prev) => Math.max(prev, scan.finding_count));
    }

    const isTerminal = scan.status === "completed" || scan.status === "failed" || scan.status === "cancelled";
    fetchToolStatuses(id);

    if (isTerminal) {
      setSSEEnabled(false);
      fetchToolOutputs(id);
    }

    if (scan.collection_id) {
      api
        .get<CollectionToolsResponse>(`/collections/${scan.collection_id}/tools`)
        .then((toolsRes) => {
          setRepoSummary(toolsRes.data.repo_summary ?? []);
        })
        .catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scanData]);

  // Use scan's started_at for elapsed time so it survives page refresh
  useEffect(() => {
    if (scanData?.started_at) {
      startTimeRef.current = new Date(scanData.started_at).getTime();
    } else if (!startTimeRef.current) {
      startTimeRef.current = Date.now();
    }
  }, [scanData?.started_at]);

  useEffect(() => {
    const timer = setInterval(() => {
      if (startTimeRef.current) {
        const endTime = scanData?.completed_at ? new Date(scanData.completed_at).getTime() : Date.now();
        setElapsed(Math.round((endTime - startTimeRef.current) / 1000));
      }
    }, 1000);
    return () => clearInterval(timer);
  }, [scanData?.completed_at]);

  // Auto-scroll the log panel only — never scroll the page.
  // Stop auto-scrolling if the user has scrolled up to read history.
  useEffect(() => {
    if (userScrolledRef.current) return;
    const container = logContainerRef.current;
    if (container) {
      container.scrollTop = container.scrollHeight;
    }
  }, [toolLogs, activeLogTool]);

  // Reset scroll lock when user switches tools
  useEffect(() => {
    userScrolledRef.current = false;
    const container = logContainerRef.current;
    if (container) {
      container.scrollTop = container.scrollHeight;
    }
  }, [activeLogTool]);

  const { connected } = useSSE<SSEEvent>({
    path: `/scans/${id}/stream`,
    enabled: sseEnabled,
    onEvent: (event) => {
      if (event.type === "tools_selected") {
        const selEvent = event as unknown as { type: string; scan_id: string; tools: string[] };
        setTools((prev) => {
          const next = new Map(prev);
          for (const name of selEvent.tools) {
            if (!next.has(name)) {
              next.set(name, {
                name,
                status: "pending",
                findings: 0,
                elapsed: 0,
                progress: 0,
              });
            }
          }
          return next;
        });
      }
      if (event.type === "scan_progress") {
        const progress = event as unknown as ScanProgressEvent & { error?: string };
        setTools((prev) => {
          const next = new Map(prev);
          next.set(progress.tool_name, {
            name: progress.tool_name,
            status: progress.status,
            findings: progress.finding_count,
            elapsed: progress.elapsed_ms,
            progress: progress.progress_pct,
            error: progress.error || undefined,
          });
          return next;
        });
        // Use cumulative total from server if available, otherwise sum from tool cards.
        if (progress.total_findings != null && progress.total_findings > 0) {
          setTotalFindings(progress.total_findings);
        } else {
          setTools((current) => {
            let sum = 0;
            for (const t of current.values()) sum += t.findings;
            setTotalFindings(sum);
            return current;
          });
        }
      }
      if (event.type === "tool_output") {
        const logEvent = event as unknown as ToolLogEvent;
        setToolLogs((prev) => {
          const next = new Map(prev);
          const lines = next.get(logEvent.tool_name) || [];
          const updated = [...lines, logEvent.line].slice(-500);
          next.set(logEvent.tool_name, updated);
          return next;
        });
        setActiveLogTool((current) => current ?? logEvent.tool_name);
      }
      if (event.type === "ai_assessment") {
        const aiEvent = event as unknown as {
          type: string;
          phase: string;
          tool?: string;
          step?: number;
          total_steps?: number;
          progress_pct: number;
        };
        setAIProgress({
          phase: aiEvent.phase,
          tool: aiEvent.tool,
          step: aiEvent.step,
          totalSteps: aiEvent.total_steps,
          progressPct: aiEvent.progress_pct,
        });
        if (aiEvent.phase === "complete" || aiEvent.phase === "failed") {
          setTimeout(() => setAIProgress(null), 3000);
        }
      }
      if (event.type === "ai_log") {
        const logEvent = event as unknown as {
          type: string;
          provider: string;
          model: string;
          phase: string;
          tool: string;
          duration_ms: number;
          error: string;
        };
        setAICallLogs(prev => [...prev, {
          provider: logEvent.provider,
          model: logEvent.model,
          phase: logEvent.phase,
          tool: logEvent.tool,
          durationMs: logEvent.duration_ms,
          error: logEvent.error,
        }]);
      }
      if (event.type === "scan_status") {
        const statusEvent = event as unknown as { type: string; scan_id: string; status: string; finding_count: number };
        setScanStatus(statusEvent.status);
        if (statusEvent.finding_count > 0) {
          setTotalFindings((prev) => Math.max(prev, statusEvent.finding_count));
        }
      }
      if (event.type === "scan_complete") {
        setScanStatus("completed");
        setSSEEnabled(false);
      }
    },
  });

  const isFinished = scanStatus === "completed" || scanStatus === "failed" || scanStatus === "cancelled";
  const aiStillRunning = aiProgress != null && aiProgress.phase !== "complete" && aiProgress.phase !== "failed" && aiProgress.phase !== "cancelled";
  const [cancelling, setCancelling] = useState(false);

  const handleCancel = async () => {
    if (cancelling) return;
    setCancelling(true);
    try {
      await api.delete(`/scans/${id}`);
      setScanStatus("cancelled");
    } catch {
      // If cancel fails, allow retry
      setCancelling(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">
            {isFinished ? (scanStatus === "cancelled" ? "Scan Cancelled" : "Scan Complete") : "Live Scan Progress"}
          </h1>
          <div className="flex items-center gap-3 mt-1">
            <StatusBadge status={scanStatus as "running" | "completed" | "failed" | "pending" | "cancelled"} />
            {scanMeta?.repoName && (
              <span className="text-sm font-medium">{scanMeta.repoName}</span>
            )}
            {scanMeta?.branch && (
              <span className="text-xs font-mono text-muted-foreground bg-muted px-1.5 py-0.5 rounded">
                {scanMeta.branch}
              </span>
            )}
            {!isFinished && (
              <span className="text-muted-foreground">
                {elapsed}s elapsed
              </span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {(!isFinished || aiStillRunning) && (
            <Button
              variant="destructive"
              onClick={handleCancel}
              disabled={cancelling}
            >
              {cancelling ? "Cancelling..." : aiStillRunning && isFinished ? "Cancel AI" : "Cancel Scan"}
            </Button>
          )}
          <Button variant={isFinished ? "default" : "outline"} onClick={() => router.push(`/scans/${id}`)}>
            View Results
          </Button>
        </div>
      </div>

      {/* Repo Info Card */}
      {repoSummary.length > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Repository Overview</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {repoSummary.map((repo) => (
                <div key={repo.repo_id} className="space-y-1.5">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-sm">{repo.repo_name}</span>
                  </div>
                  {repo.languages && Object.keys(repo.languages).length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {Object.entries(repo.languages)
                        .sort(([, a], [, b]) => b - a)
                        .map(([lang, count]) => (
                          <span
                            key={lang}
                            className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-muted text-muted-foreground"
                          >
                            {lang}: {count} files
                          </span>
                        ))}
                    </div>
                  )}
                  <div className="flex gap-4 text-xs text-muted-foreground">
                    <span>{repo.source_files} source</span>
                    <span>{repo.test_files} tests</span>
                    <span>{repo.total_files - repo.source_files - repo.test_files} other</span>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-3 grid-cols-2 md:grid-cols-5">
        <Card>
          <CardContent className="pt-4 pb-3 text-center">
            <p className="text-2xl font-bold">{totalFindings}</p>
            <p className="text-xs text-muted-foreground">Findings</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-4 pb-3 text-center">
            <p className="text-2xl font-bold">{tools.size}</p>
            <p className="text-xs text-muted-foreground">
              {isFinished ? "Tools Ran" : `${Array.from(tools.values()).filter(t => t.status === "running").length} running / ${tools.size} total`}
            </p>
          </CardContent>
        </Card>
        {scanMeta?.repoName && (
          <Card>
            <CardContent className="pt-4 pb-3 text-center">
              <p className="text-sm font-semibold truncate">{scanMeta.repoName}</p>
              <p className="text-xs text-muted-foreground">Repository</p>
            </CardContent>
          </Card>
        )}
        {scanMeta?.branch && (
          <Card>
            <CardContent className="pt-4 pb-3 text-center">
              <p className="text-sm font-mono font-semibold truncate">{scanMeta.branch}</p>
              <p className="text-xs text-muted-foreground">Branch</p>
            </CardContent>
          </Card>
        )}
        {/* AI Assessment status card */}
        {(aiProgress || aiCallLogs.length > 0 || scanData?.ai_enabled) && (
          <Card className={aiStillRunning ? "border-blue-200 dark:border-blue-800" : ""}>
            <CardContent className="pt-4 pb-3 text-center">
              <div className="flex items-center justify-center gap-1.5">
                {aiStillRunning ? (
                  <span className="animate-spin inline-block w-3 h-3 border-2 border-blue-600 border-t-transparent rounded-full" />
                ) : aiProgress?.phase === "complete" || aiCallLogs.some(l => !l.error) ? (
                  <span className="text-green-600 text-sm">&#10003;</span>
                ) : aiProgress?.phase === "cancelled" ? (
                  <span className="text-orange-500 text-sm">&#10007;</span>
                ) : aiProgress?.phase === "failed" || aiCallLogs.some(l => l.error) ? (
                  <span className="text-red-600 text-sm">&#10007;</span>
                ) : (
                  <span className="text-muted-foreground text-sm">&#8943;</span>
                )}
                <p className="text-sm font-semibold">
                  {aiProgress
                    ? aiProgress.phase === "complete" ? "Done"
                      : aiProgress.phase === "failed" ? "Failed"
                      : aiProgress.phase === "cancelled" ? "Cancelled"
                      : aiProgress.phase === "summarizing" ? "Summarizing"
                      : aiProgress.step && aiProgress.totalSteps
                        ? `${aiProgress.step}/${aiProgress.totalSteps} tools`
                        : "Running"
                    : aiCallLogs.length > 0
                      ? `${aiCallLogs.length} calls`
                      : "Pending"}
                </p>
              </div>
              <p className="text-xs text-muted-foreground">
                {aiStillRunning && aiProgress?.tool
                  ? `Assessing: ${aiProgress.tool}`
                  : "AI Assessment"}
              </p>
            </CardContent>
          </Card>
        )}
      </div>

      <div className="space-y-2">
        <h3 className="text-sm font-medium">Tools</h3>
        {tools.size === 0 ? (
          <p className="text-sm text-muted-foreground">
            Waiting for scan to start...
          </p>
        ) : (
          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 gap-2">
            {Array.from(tools.values()).map((tool) => (
              <Card
                key={tool.name}
                className={`cursor-pointer transition-all p-3 ${
                  activeLogTool === tool.name ? "border-primary ring-1 ring-primary" : ""
                } ${tool.status === "running" ? "animate-pulse" : ""} ${tool.status === "pending" ? "opacity-60" : ""} ${tool.status === "failed" ? "border-red-300 dark:border-red-800 bg-red-50/50 dark:bg-red-950/20" : ""}`}
                onClick={() => setActiveLogTool(tool.name)}
              >
                <p className="font-mono text-xs truncate">{tool.name}</p>
                <p className="text-lg font-bold">{tool.status === "pending" ? "—" : tool.findings}</p>
                <p className="text-[10px] text-muted-foreground">
                  {tool.status === "pending" ? "queued" : tool.status === "failed" ? "failed" : "findings"}
                </p>
                <div className="mt-1">
                  <StatusBadge status={tool.status === "pending" ? "pending" : tool.status} />
                </div>
                {tool.status === "failed" && (() => {
                  const logs = toolLogs.get(tool.name) || [];
                  const fixLine = logs.find(l => l.startsWith("[FIX]"));
                  if (fixLine) {
                    // Show the diagnostic problem instead of raw error
                    const problem = fixLine.replace(/^\[FIX\]\s*\S+:\s*/, "");
                    return (
                      <p className="mt-1 text-[10px] text-blue-600 dark:text-blue-400 truncate" title={problem}>
                        {problem}
                      </p>
                    );
                  }
                  if (tool.error) {
                    return (
                      <p className="mt-1 text-[10px] text-red-600 dark:text-red-400 truncate" title={tool.error}>
                        {tool.error}
                      </p>
                    );
                  }
                  return null;
                })()}
              </Card>
            ))}
          </div>
        )}
      </div>

      {/* AI Assessment Progress removed — now shown as compact card in summary grid above */}

      {/* AI Call Logs */}
      {aiCallLogs.length > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">AI Calls ({aiCallLogs.length})</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-1">
              {aiCallLogs.map((log, i) => (
                <div key={i} className="flex items-center justify-between py-1 px-2 text-xs border-b last:border-0">
                  <div className="flex items-center gap-2">
                    <span className={`w-2 h-2 rounded-full ${log.error ? "bg-red-500" : "bg-green-500"}`} />
                    <span className="font-medium capitalize">{log.phase}</span>
                    {log.tool && (
                      <span className="font-mono text-muted-foreground bg-muted px-1 py-0.5 rounded">{log.tool}</span>
                    )}
                  </div>
                  <div className="flex items-center gap-3 text-muted-foreground">
                    <span>{log.provider}{log.model ? `/${log.model}` : ""}</span>
                    <span>{(log.durationMs / 1000).toFixed(1)}s</span>
                    {log.error && <span className="text-red-500">failed</span>}
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Tool Output Logs */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">
            Tool Output{activeLogTool ? `: ${activeLogTool}` : ""}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div
            ref={logContainerRef}
            onScroll={() => {
              const el = logContainerRef.current;
              if (el) {
                // User scrolled up if they're more than 40px from the bottom
                userScrolledRef.current = el.scrollHeight - el.scrollTop - el.clientHeight > 40;
              }
            }}
            className="bg-muted/50 rounded-md p-3 h-64 overflow-y-auto font-mono text-xs leading-relaxed">
            {activeLogTool && toolLogs.get(activeLogTool)?.length ? (
              <>
                {toolLogs.get(activeLogTool)!.map((line, i) => (
                  <div
                    key={i}
                    className={`whitespace-pre-wrap ${
                      line.startsWith("[FIX]")
                        ? "text-blue-600 dark:text-blue-400 font-semibold"
                        : line.startsWith("[ERROR]") || line.startsWith("[STDERR]")
                          ? "text-red-600 dark:text-red-400 font-semibold"
                          : "text-muted-foreground"
                    }`}
                  >
                    {line}
                  </div>
                ))}
                <div ref={logEndRef} aria-hidden />
              </>
            ) : (
              <p className="text-muted-foreground">
                {activeLogTool
                  ? tools.get(activeLogTool)?.status === "failed"
                    ? `${activeLogTool} failed${tools.get(activeLogTool)?.error ? `: ${tools.get(activeLogTool)!.error}` : ". No output captured."}`
                    : "Loading output..."
                  : isFinished
                    ? "Select a tool to view output."
                    : "Waiting for tool output..."}
              </p>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
