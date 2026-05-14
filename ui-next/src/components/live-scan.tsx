// Live-scan visualization: per-tool status cards + a collapsible log
// viewer. Wired to the SSE stream the Go API publishes at
// /api/scans/{id}/events. Each tool moves: queued → running → done|failed
// with a glow that matches its status.
//
// Findings count is updated optimistically as `scan_progress` events
// arrive; full reconciliation happens when the SSE stream finishes.
import { useEffect, useRef, useState } from "react";
import {
  CheckCircle2Icon,
  CircleDashedIcon,
  LoaderIcon,
  XCircleIcon,
  ChevronDownIcon,
  ChevronRightIcon,
} from "lucide-react";
import { cn } from "@/lib/cn";
import type { ScanStatus } from "@/lib/types";

type ToolStatus = "queued" | "running" | "completed" | "failed" | "skipped";

interface ToolState {
  name: string;
  status: ToolStatus;
  findingCount: number;
  // elapsedMs comes from the server on OnToolDone (real measured value).
  // While the tool is running, the server emits 0 — we tick locally from
  // startedAt instead so the card actually counts up.
  elapsedMs: number;
  startedAt: number | null; // performance.now() when status flipped to running
  log: string[];
  expanded: boolean;
}

interface LiveScanProps {
  scanId: string;
  initialTools: string[];
  initialCompleted?: string[];
  initialFailed?: string[];
  initialRunning?: string[];
  scanStatus: ScanStatus;
}

export function LiveScan({
  scanId,
  initialTools,
  initialCompleted = [],
  initialFailed = [],
  initialRunning = [],
  scanStatus,
}: LiveScanProps) {
  const [tools, setTools] = useState<Record<string, ToolState>>(() => {
    const seed: Record<string, ToolState> = {};
    const completedSet = new Set(initialCompleted);
    const failedSet = new Set(initialFailed);
    const runningSet = new Set(initialRunning);
    for (const name of initialTools) {
      // Seed status from the scan record so reloading mid-scan, or joining
      // late, doesn't surface every already-finished tool as "queued".
      let status: ToolStatus = "queued";
      if (failedSet.has(name)) status = "failed";
      else if (completedSet.has(name)) status = "completed";
      else if (runningSet.has(name)) status = "running";
      seed[name] = {
        name,
        status,
        findingCount: 0,
        elapsedMs: 0,
        // If we joined mid-flight on a tool already in 'running', start
        // the clock now — we don't know the true start time but at
        // least the counter ticks forward visibly.
        startedAt: status === "running" ? performance.now() : null,
        log: [],
        expanded: false,
      };
    }
    return seed;
  });
  // Tick state forces a re-render every second so running tools'
  // elapsed displays update. Doesn't mutate the tools map — we read
  // performance.now() at render time against startedAt.
  const [, setTick] = useState(0);
  useEffect(() => {
    // Only tick while at least one tool is running. We re-check on
    // every state change because tools transition out of running.
    const anyRunning = Object.values(tools).some((t) => t.status === "running");
    if (!anyRunning) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [tools]);

  const eventSourceRef = useRef<EventSource | null>(null);

  useEffect(() => {
    if (scanStatus === "completed" || scanStatus === "failed" || scanStatus === "cancelled") {
      return;
    }
    const es = new EventSource(`/api/scans/${scanId}/events`, {
      withCredentials: true,
    });
    eventSourceRef.current = es;

    es.addEventListener("scan_progress", (evt) => {
      const data = JSON.parse((evt as MessageEvent).data);
      setTools((prev) => {
        const t = prev[data.tool_name];
        if (!t) return prev;
        const newStatus = data.status as ToolStatus;
        // Set startedAt on the running transition; clear once the tool
        // reaches a terminal state so the card stops ticking.
        let startedAt = t.startedAt;
        if (newStatus === "running" && startedAt === null) {
          startedAt = performance.now();
        } else if (newStatus !== "running") {
          startedAt = null;
        }
        return {
          ...prev,
          [data.tool_name]: {
            ...t,
            status: newStatus,
            findingCount: data.finding_count ?? t.findingCount,
            // Server's elapsed_ms is meaningful only on terminal status
            // (running broadcasts hardcode 0). Trust it when non-zero.
            elapsedMs: data.elapsed_ms > 0 ? data.elapsed_ms : t.elapsedMs,
            startedAt,
          },
        };
      });
    });

    es.addEventListener("tool_output", (evt) => {
      const data = JSON.parse((evt as MessageEvent).data);
      setTools((prev) => {
        const t = prev[data.tool_name];
        if (!t) return prev;
        return {
          ...prev,
          [data.tool_name]: {
            ...t,
            // Cap retained log per tool at 500 lines to avoid runaway memory.
            log: [...t.log.slice(-499), data.line],
          },
        };
      });
    });

    es.onerror = () => {
      // EventSource will auto-reconnect; we just log.
      console.warn("scan SSE error; will reconnect");
    };

    return () => {
      es.close();
      eventSourceRef.current = null;
    };
  }, [scanId, scanStatus]);

  function toggleExpand(name: string) {
    setTools((prev) => ({
      ...prev,
      [name]: { ...prev[name], expanded: !prev[name].expanded },
    }));
  }

  const items = Object.values(tools).sort((a, b) =>
    statusRank(a.status) - statusRank(b.status) || a.name.localeCompare(b.name),
  );

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
        {items.map((t) => (
          <ToolCard key={t.name} tool={t} onToggle={() => toggleExpand(t.name)} />
        ))}
      </div>
    </div>
  );
}

function statusRank(s: ToolStatus): number {
  return { running: 0, queued: 1, failed: 2, completed: 3, skipped: 4 }[s] ?? 5;
}

// liveElapsedSeconds returns the elapsed seconds to display on a tool
// card. For running tools, it ticks from `startedAt` (set when the SSE
// status flipped to running) so the user sees a counter actually
// advance. For terminal states (completed/failed) it uses the server-
// measured elapsedMs which is the source of truth.
function liveElapsedSeconds(t: ToolState): number {
  if (t.status === "running" && t.startedAt !== null) {
    return Math.max(0, Math.round((performance.now() - t.startedAt) / 1000));
  }
  return Math.round(t.elapsedMs / 1000);
}

function ToolCard({
  tool,
  onToggle,
}: {
  tool: ToolState;
  onToggle: () => void;
}) {
  const meta = statusMeta(tool.status);
  return (
    <div className={cn("glass-card border", meta.glow)}>
      <button
        type="button"
        onClick={onToggle}
        className="w-full flex items-center gap-3 px-4 py-3 text-left"
      >
        <meta.Icon className={cn("size-4 shrink-0", meta.iconCls)} />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm font-medium truncate">
              {tool.name}
            </span>
            <span className={cn("text-2xs", meta.labelCls)}>{meta.label}</span>
          </div>
          <div className="mt-1.5 gauge-bar">
            <div
              className={cn("gauge-bar-fill", meta.barCls)}
              style={{
                width:
                  tool.status === "completed"
                    ? "100%"
                    : tool.status === "running"
                      ? "60%"
                      : tool.status === "queued"
                        ? "10%"
                        : "100%",
              }}
            />
          </div>
        </div>
        <div className="text-right text-xs tabular-nums shrink-0">
          <div className="font-mono">{tool.findingCount}</div>
          <div className="text-muted-foreground/70">{liveElapsedSeconds(tool)}s</div>
        </div>
        {tool.log.length > 0 ? (
          tool.expanded ? (
            <ChevronDownIcon className="size-4 text-muted-foreground" />
          ) : (
            <ChevronRightIcon className="size-4 text-muted-foreground" />
          )
        ) : null}
      </button>
      {tool.expanded && tool.log.length > 0 && (
        <div className="px-4 pb-3">
          <div className="log-viewer max-h-64">
            {tool.log.map((line, i) => (
              <div key={i} className={logLineClass(line)}>
                {line}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function statusMeta(s: ToolStatus): {
  Icon: typeof CheckCircle2Icon;
  iconCls: string;
  label: string;
  labelCls: string;
  barCls: string;
  glow: string;
} {
  switch (s) {
    case "running":
      return {
        Icon: LoaderIcon,
        iconCls: "text-blue-400 animate-spin",
        label: "Running",
        labelCls: "text-blue-400",
        barCls: "bg-blue-500/70",
        glow: "glow-info",
      };
    case "completed":
      return {
        Icon: CheckCircle2Icon,
        iconCls: "text-emerald-400",
        label: "Done",
        labelCls: "text-emerald-400",
        barCls: "bg-emerald-500/70",
        glow: "glow-success",
      };
    case "failed":
      return {
        Icon: XCircleIcon,
        iconCls: "text-red-400",
        label: "Failed",
        labelCls: "text-red-400",
        barCls: "bg-red-500/70",
        glow: "glow-error",
      };
    case "skipped":
      return {
        Icon: CircleDashedIcon,
        iconCls: "text-zinc-500",
        label: "Skipped",
        labelCls: "text-zinc-500",
        barCls: "bg-zinc-500/40",
        glow: "",
      };
    case "queued":
    default:
      return {
        Icon: CircleDashedIcon,
        iconCls: "text-muted-foreground",
        label: "Queued",
        labelCls: "text-muted-foreground",
        barCls: "bg-muted-foreground/40",
        glow: "",
      };
  }
}

function logLineClass(line: string): string {
  if (line.includes("[ERROR]") || line.includes("[STDERR]")) return "log-error";
  if (line.includes("[WARN]") || line.includes("[WARNING]")) return "log-warn";
  if (line.includes("[SKIP]")) return "log-skip";
  if (line.includes("[FIX]")) return "log-fix";
  return "log-info";
}
