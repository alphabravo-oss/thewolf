// FixLogConsole — a live, monospace, auto-scrolling log panel for a running
// fix job. It uses the Nocturne .glass-card / .mono frame (running/done/error
// header and black/40 log box) and consumes the
// GET SSE relay at /fixes/{id}/stream via the shared useSSE hook (the worker
// runs out-of-process and the server tails its durable log artifact).
//
// Styling is global Nocturne classes only — no bespoke page CSS.
import { useEffect, useMemo, useRef, useState } from "react";
import {
  CheckCircle2Icon,
  Loader2Icon,
  TerminalIcon,
  XCircleIcon,
} from "lucide-react";
import { useSSE } from "@/lib/sse";
import {
  findingLabel,
  isFixPaused,
  isFixTerminal,
  useFindingsMap,
  type FixJobStatus,
  type FixStreamEvent,
  type QueuedBehind,
} from "@/lib/fixes";

const FINDING_ID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;

export function FixLogConsole({
  fixId,
  status,
  queuedBehind,
  enabled = true,
}: {
  fixId: string;
  status: FixJobStatus | undefined;
  queuedBehind?: QueuedBehind;
  enabled?: boolean;
}) {
  const [lines, setLines] = useState<string[]>([]);
  const [drained, setDrained] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  const active =
    status === "queued" || status === "claimed" || status === "running";
  // Paused/terminal jobs still need one drain of the durable log. Do not
  // keep SSE open after that — EventSource reconnects and replays the
  // whole file, which looks like the agent is still running.
  const stream = enabled && (active || !drained);

  useSSE<FixStreamEvent>({
    path: `/fixes/${fixId}/stream`,
    enabled: stream,
    onEvent: (ev) => {
      if (ev.type === "fix_log" && ev.line) {
        setLines((prev) => [...prev, ev.line as string]);
      }
      if (
        ev.type === "fix_completed" ||
        (ev.type === "fix_status" &&
          (isFixTerminal(ev.status) || isFixPaused(ev.status)))
      ) {
        setDrained(true);
      }
    },
  });

  // Auto-scroll to the newest line as output streams in.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  const ids = useMemo(() => {
    const seen = new Set<string>();
    for (const line of lines) {
      for (const m of line.match(FINDING_ID) ?? []) seen.add(m);
    }
    return [...seen];
  }, [lines]);
  const findings = useFindingsMap(ids);

  const display = (line: string) =>
    line.replace(FINDING_ID, (id) => {
      const f = findings.data?.[id];
      return f ? findingLabel(f) : id;
    });

  const queued = status === "queued";
  const running = status === "running" || status === "claimed";
  const paused = isFixPaused(status);
  const failed = status === "failed";
  const done = status === "succeeded";
  const behindLabel = queuedBehind
    ? queuedBehind.kind === "console"
      ? `fixer console ${queuedBehind.id.slice(0, 8)}`
      : `agent ${queuedBehind.id.slice(0, 8)}`
    : "";

  return (
    <div className="glass-card p-5 space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-sm font-medium">
          <TerminalIcon className="size-4 text-muted-foreground" />
          Agent log
          <span className="mono text-xs text-muted-foreground">{fixId.slice(0, 8)}</span>
        </div>
        {queued && (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            Queued
          </span>
        )}
        {running && (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <Loader2Icon className="size-3.5 animate-spin" /> Running…
          </span>
        )}
        {paused && (
          <span className="inline-flex items-center gap-1.5 text-xs text-status-warning">
            Paused
          </span>
        )}
        {done && (
          <span className="inline-flex items-center gap-1.5 text-xs text-status-success">
            <CheckCircle2Icon className="size-3.5" /> Succeeded
          </span>
        )}
        {failed && (
          <span className="inline-flex items-center gap-1.5 text-xs text-destructive">
            <XCircleIcon className="size-3.5" /> Failed
          </span>
        )}
        {status === "cancelled" && (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <XCircleIcon className="size-3.5" /> Cancelled
          </span>
        )}
      </div>

      {/* The streamed log. Monospace, fixed height, auto-scrolling. */}
      <div
        ref={scrollRef}
        className="mono text-[11px] leading-relaxed max-h-[28rem] overflow-auto rounded-md border border-border/40 bg-black/40 p-3 whitespace-pre-wrap break-words"
      >
        {lines.length === 0 ? (
          <span className="text-muted-foreground">
            {queued
              ? behindLabel
                ? `Queued behind ${behindLabel}`
                : "Queued — waiting for the fixer worker"
              : active
                ? "Waiting for agent output…"
                : "No live log — the job has finished."}
          </span>
        ) : (
          lines.map((line, i) => (
            <div key={i} className="path text-foreground/80">
              {display(line)}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
