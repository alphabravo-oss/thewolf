// BuildConsole — a monospace, auto-scrolling log panel for the server-driven
// scanner image build/push subsystem (Settings → Scanner Images).
//
// It is fed by streamBuild() (POST → SSE). The parent owns the build trigger
// (the per-variant "Rebuild (local)" / "Rebuild & push" button and the
// "Rebuild all" header action) and hands this component the variant + push
// flag to stream; BuildConsole renders the live log lines and the terminal
// success/error frame. A "running" spinner shows while the stream is open;
// the terminal frame reports loaded-locally vs. pushed refs, or the error.
//
// Styling is global Nocturne classes only (.glass-card / .path / .mono) — no
// bespoke page CSS.
import { useEffect, useRef, useState } from "react";
import {
  CheckCircle2Icon,
  Loader2Icon,
  TerminalIcon,
  XCircleIcon,
} from "lucide-react";
import {
  streamBuild,
  type BuildDone,
  type BuildError,
} from "@/lib/scanner-build";

// The build the parent wants streamed. A new (non-null) value starts a fresh
// stream; changing it (a different variant or push flag, or a re-click via a
// bumped `nonce`) restarts. Setting it back to null leaves the last log up.
export interface BuildTarget {
  // "default" | "jvm" | "rust" | "codeql" | "all"
  variant: string;
  push: boolean;
  // Bumped on every click so re-running the same variant restarts the stream.
  nonce: number;
}

type Phase = "idle" | "running" | "done" | "error";

export function BuildConsole({ target }: { target: BuildTarget | null }) {
  const [lines, setLines] = useState<string[]>([]);
  const [phase, setPhase] = useState<Phase>("idle");
  const [result, setResult] = useState<BuildDone | null>(null);
  const [error, setError] = useState<BuildError | string | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Each new target (variant / push / nonce) starts a fresh stream and aborts
  // the previous one on cleanup. We key the effect on the primitive fields so
  // a re-click (nonce bump) re-runs even with the same variant/push.
  const variant = target?.variant;
  const push = target?.push;
  const nonce = target?.nonce;

  useEffect(() => {
    if (!variant) return;
    const ctrl = new AbortController();
    setLines([]);
    setResult(null);
    setError(null);
    setPhase("running");

    streamBuild(variant, push ?? false, {
      signal: ctrl.signal,
      onLine: (line) => setLines((prev) => [...prev, line]),
      onDone: (done) => {
        setResult(done);
        setPhase("done");
      },
      onError: (err) => {
        setError(err);
        setPhase("error");
      },
    }).catch((e) => {
      if (ctrl.signal.aborted) return;
      setError(e instanceof Error ? e.message : "Build request failed");
      setPhase("error");
    });

    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [variant, push, nonce]);

  // Auto-scroll to the newest line as output streams in.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines, phase]);

  if (phase === "idle" && lines.length === 0) {
    return (
      <div className="glass-card p-5">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <TerminalIcon className="size-4" />
          <span>
            Build output appears here. Click <strong>Rebuild</strong> on a
            variant to start.
          </span>
        </div>
      </div>
    );
  }

  return (
    <div className="glass-card p-5 space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-sm font-medium">
          <TerminalIcon className="size-4 text-muted-foreground" />
          Build console
          {variant && (
            <span className="mono text-xs text-muted-foreground">
              {variant === "all" ? "all variants" : variant}
            </span>
          )}
        </div>
        {phase === "running" && (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <Loader2Icon className="size-3.5 animate-spin" /> Building…
          </span>
        )}
        {phase === "done" && (
          <span className="inline-flex items-center gap-1.5 text-xs text-emerald-400">
            <CheckCircle2Icon className="size-3.5" /> Done
          </span>
        )}
        {phase === "error" && (
          <span className="inline-flex items-center gap-1.5 text-xs text-destructive">
            <XCircleIcon className="size-3.5" /> Failed
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
            Waiting for build output…
          </span>
        ) : (
          lines.map((line, i) => (
            <div key={i} className="path text-foreground/80">
              {line}
            </div>
          ))
        )}
      </div>

      {/* Terminal summary. */}
      {phase === "done" && result && (
        <div className="rounded-md border border-emerald-500/30 bg-emerald-500/10 p-3 text-xs space-y-1">
          <div className="font-medium text-emerald-300">
            {result.pushed
              ? "Built & pushed to DockerHub"
              : result.loaded_locally
                ? "Built & loaded into the local Docker daemon"
                : "Build complete"}
          </div>
          {result.refs && result.refs.length > 0 && (
            <ul className="mono text-[11px] text-muted-foreground space-y-0.5">
              {result.refs.map((ref) => (
                <li key={ref} className="break-all">
                  {ref}
                </li>
              ))}
            </ul>
          )}
          {result.digest && (
            <div className="mono text-[11px] text-muted-foreground break-all">
              digest: {result.digest}
            </div>
          )}
        </div>
      )}
      {phase === "error" && error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive break-words">
          {typeof error === "string" ? error : error.error}
        </div>
      )}
    </div>
  );
}
