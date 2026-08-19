// Worker-attached login/shell console. The process runs on the fixer worker
// (where OAuth files persist). PTY bytes stream over SSE into xterm.js;
// keystrokes go back through the DB stdin queue.
import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import {
  DownloadIcon,
  ExternalLinkIcon,
  Loader2Icon,
  TerminalIcon,
  XCircleIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { useSSE } from "@/lib/sse";
import {
  cancelFixerConsole,
  getFixerConsole,
  isFixerConsoleActive,
  sendFixerConsoleInput,
  startFixerConsole,
  type FixEngineStatus,
  type FixerConsole,
  type FixerConsoleStreamEvent,
} from "@/lib/fixes";

const LOGIN_ENGINES = [
  { id: "claude", label: "Claude" },
  { id: "codex", label: "Codex" },
  { id: "opencode", label: "OpenCode" },
] as const;

export function FixerConsolePanel({
  allowShell,
  worker = [],
}: {
  allowShell: boolean;
  worker?: FixEngineStatus[];
}) {
  const qc = useQueryClient();
  const [loginEngine, setLoginEngine] = useState<string>("claude");
  const [session, setSession] = useState<FixerConsole | null>(null);
  const [busy, setBusy] = useState(false);
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const live = isFixerConsoleActive(session?.status);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const term = new Terminal({
      convertEol: false,
      cursorBlink: true,
      fontFamily: '"JetBrains Mono Variable", ui-monospace, monospace',
      fontSize: 13,
      lineHeight: 1.25,
      theme: {
        background: "#0b0e14",
        foreground: "#e6edf3",
        cursor: "#7dd3fc",
        selectionBackground: "#1d4ed855",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;
    const onResize = () => fit.fit();
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, []);

  useSSE<FixerConsoleStreamEvent>({
    path: session ? `/fixes/consoles/${session.id}/stream` : "",
    enabled: !!session,
    onEvent: (ev) => {
      const term = termRef.current;
      if (ev.type === "console_data" && ev.data && term) {
        term.write(ev.data);
      }
      if (ev.type === "console_log" && ev.line && term) {
        term.writeln(ev.line);
      }
      if (ev.type === "console_status" || ev.type === "console_completed") {
        setSession((prev) =>
          prev
            ? {
                ...prev,
                status: ev.status ?? prev.status,
                last_url: ev.last_url ?? prev.last_url,
                error: ev.error ?? prev.error,
              }
            : prev,
        );
        if (ev.type === "console_completed") {
          void qc.invalidateQueries({ queryKey: ["fix-engines"] });
        }
      }
    },
  });

  useEffect(() => {
    const term = termRef.current;
    if (!term || !session || !live) return;
    term.focus();
    const sub = term.onData((data) => {
      void sendFixerConsoleInput(session.id, data).catch((e) => {
        toast.error(e instanceof Error ? e.message : "Send failed");
      });
    });
    return () => sub.dispose();
  }, [session?.id, live]);

  useEffect(() => {
    if (!session || !live) return;
    const t = window.setInterval(() => {
      getFixerConsole(session.id)
        .then((next) => setSession(next))
        .catch(() => undefined);
    }, 2000);
    return () => window.clearInterval(t);
  }, [session?.id, live]);

  async function start(
    kind: "login" | "shell" | "install",
    engine = loginEngine,
  ) {
    setBusy(true);
    try {
      if (kind !== "shell") setLoginEngine(engine);
      const cons = await startFixerConsole({
        kind,
        engine: kind === "shell" ? undefined : engine,
      });
      termRef.current?.reset();
      termRef.current?.focus();
      setSession(cons);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Could not start console");
    } finally {
      setBusy(false);
    }
  }

  async function stop() {
    if (!session) return;
    try {
      await cancelFixerConsole(session.id);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Cancel failed");
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        {LOGIN_ENGINES.map((item) => {
          const st = statusFor(item.id, worker);
          const installed = st?.installed === true;
          return (
            <Button
              key={item.id}
              size="sm"
              variant={loginEngine === item.id ? "default" : "outline"}
              disabled={busy || live}
              onClick={() => start(installed ? "login" : "install", item.id)}
            >
              {busy && loginEngine === item.id ? (
                <Loader2Icon className="size-3.5 animate-spin" />
              ) : !installed ? (
                <DownloadIcon className="size-3.5" />
              ) : null}
              {installed ? `Login with ${item.label}` : `Install ${item.label}`}
            </Button>
          );
        })}
        {allowShell && (
          <Button
            size="sm"
            variant="outline"
            disabled={busy || live}
            onClick={() => start("shell")}
          >
            Open worker shell
          </Button>
        )}
        {live && (
          <Button size="sm" variant="ghost" onClick={stop}>
            <XCircleIcon className="size-3.5" />
            Stop
          </Button>
        )}
        {session?.last_url && (
          <a
            href={session.last_url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-xs text-status-success underline"
          >
            Open login URL
            <ExternalLinkIcon className="size-3" />
          </a>
        )}
      </div>

      <div className="glass-card p-4 space-y-2">
        <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
          <span className="inline-flex items-center gap-1.5">
            <TerminalIcon className="size-3.5" />
            Worker terminal
            {session ? ` · ${session.status}` : ""}
          </span>
          {live && session?.status !== "running" && (
            <span className="inline-flex items-center gap-1">
              <Loader2Icon className="size-3 animate-spin" />
              waiting for fixer worker
            </span>
          )}
        </div>
        <div
          className="relative min-w-0 overflow-hidden rounded-md border border-border/40 bg-[#0b0e14]"
          onClick={() => termRef.current?.focus()}
        >
          <div ref={hostRef} className="h-80 w-full px-2 py-2" />
          {!session && (
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center px-6 text-center text-xs text-muted-foreground">
              Click Login. Type in this terminal — arrows, Enter, and paste
              go to the worker.
            </div>
          )}
        </div>
        {session?.error && (
          <p className="text-xs text-destructive">{session.error}</p>
        )}
      </div>
    </div>
  );
}

function statusFor(id: string, worker: FixEngineStatus[]) {
  return worker.find((row) => {
    if (row.command === id || row.name === id) return true;
    return id === "claude" && row.name === "claude-code";
  });
}
