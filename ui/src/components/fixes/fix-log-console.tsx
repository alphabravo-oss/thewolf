// FixLogConsole — a live, monospace, auto-scrolling log panel for a running
// fix job. It mirrors the scanner BuildConsole frame (Nocturne .glass-card /
// .mono, the running/done/error header, the black/40 log box) but consumes the
// GET SSE relay at /fixes/{id}/stream via the shared useSSE hook (the worker
// runs out-of-process and the server tails its durable log artifact).
//
// Styling is global Nocturne classes only — no bespoke page CSS.
import { useEffect, useRef, useState } from "react";
import {
  CheckCircle2Icon,
  Loader2Icon,
  TerminalIcon,
  XCircleIcon,
} from "lucide-react";
import { useSSE } from "@/lib/sse";
import { isFixTerminal, type FixJobStatus, type FixStreamEvent } from "@/lib/fixes";

export function FixLogConsole({
  fixId,
  status,
  enabled = true,
}: {
  fixId: string;
  status: FixJobStatus | undefined;
  enabled?: boolean;
}) {
  const [lines, setLines] = useState<string[]>([]);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Only hold the stream open while the job is live; a terminal job's log is
  // already fully drained by the time the detail page renders it.
  const live = enabled && !isFixTerminal(status);

  useSSE<FixStreamEvent>({
    path: `/fixes/${fixId}/stream`,
    enabled: live,
    onEvent: (ev) => {
      if (ev.type === "fix_log" && ev.line) {
        setLines((prev) => [...prev, ev.line as string]);
      }
    },
  });

  // Auto-scroll to the newest line as output streams in.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  const running = status === "running" || status === "claimed" || status === "queued";
  const failed = status === "failed";
  const done = status === "succeeded";

  return (
    <div className="glass-card p-5 space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-sm font-medium">
          <TerminalIcon className="size-4 text-muted-foreground" />
          Fix log
          <span className="mono text-xs text-muted-foreground">{fixId.slice(0, 8)}</span>
        </div>
        {running && (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <Loader2Icon className="size-3.5 animate-spin" /> Running…
          </span>
        )}
        {done && (
          <span className="inline-flex items-center gap-1.5 text-xs text-emerald-400">
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
        className="mono text-[11px] leading-relaxed max-h-72 overflow-auto rounded-md border border-border/40 bg-black/40 p-3 whitespace-pre-wrap break-words"
      >
        {lines.length === 0 ? (
          <span className="text-muted-foreground">
            {live ? "Waiting for fix output…" : "No live log — the job has finished."}
          </span>
        ) : (
          lines.map((line, i) => (
            <div key={i} className="path text-foreground/80">
              {line}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
