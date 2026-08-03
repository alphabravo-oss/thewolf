import { useEffect, useRef, useState } from "react";
import { safeDisplayText } from "@/lib/safe-display";

export type CustomBuildStreamState =
  | "connecting"
  | "live"
  | "reconnecting"
  | "polling"
  | "stopped";

export interface CustomBuildLogEntry {
  sequence: number;
  variant?: string;
  line: string;
}

const API_BASE = (import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "");
const MAX_VISIBLE_LOGS = 800;

// The fetch reader is intentional: EventSource cannot send Last-Event-ID on
// an explicit reconnect. Logs are UI-bounded while the server retains the
// durable source of truth. Terminal payload JSON is never admitted to state.
export function useCustomBuildEvents(
  buildId: string,
  terminal: boolean,
  enabled = true,
): {
  logs: CustomBuildLogEntry[];
  state: CustomBuildStreamState;
  error?: string;
} {
  const [logs, setLogs] = useState<CustomBuildLogEntry[]>([]);
  const [state, setState] = useState<CustomBuildStreamState>("connecting");
  const [error, setError] = useState<string>();
  const lastSequence = useRef(0);
  const terminalRef = useRef(terminal);
  terminalRef.current = terminal;

  useEffect(() => {
    const controller = new AbortController();
    lastSequence.current = 0;
    setLogs([]);
    setState("connecting");
    setError(undefined);
    if (!enabled) {
      setState("stopped");
      return () => controller.abort();
    }

    async function stream() {
      let retryMilliseconds = 500;
      let consecutiveDisconnects = 0;

      while (!controller.signal.aborted) {
        try {
          const headers: Record<string, string> = {
            Accept: "text/event-stream",
          };
          if (lastSequence.current > 0) {
            headers["Last-Event-ID"] = String(lastSequence.current);
          }
          const response = await fetch(
            `${API_BASE}/v1/scanners/custom-builds/${encodeURIComponent(buildId)}/events`,
            { credentials: "include", headers, signal: controller.signal },
          );
          if (!response.ok || !response.body) {
            throw new Error(
              response.status === 403
                ? "Build log permission denied"
                : response.status === 404
                  ? "Build log is unavailable"
                  : `Build log stream returned ${response.status}`,
            );
          }

          setState("live");
          setError(undefined);
          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          let receivedTerminal = false;
          let receivedFrame = false;

          while (!controller.signal.aborted) {
            const chunk = await reader.read();
            if (chunk.done) break;
            buffer += decoder.decode(chunk.value, { stream: true });
            const frames = buffer.split(/\r?\n\r?\n/);
            buffer = frames.pop() ?? "";
            for (const frame of frames) {
              const parsed = parseCustomBuildFrame(frame);
              if (!parsed || parsed.sequence <= lastSequence.current) continue;
              receivedFrame = true;
              lastSequence.current = parsed.sequence;
              if (parsed.kind === "terminal") {
                receivedTerminal = true;
                break;
              }
              setLogs((current) => {
                const next = [...current, parsed.entry];
                return next.length > MAX_VISIBLE_LOGS
                  ? next.slice(next.length - MAX_VISIBLE_LOGS)
                  : next;
              });
            }
            if (receivedTerminal) {
              await reader.cancel();
              break;
            }
          }

          if (receivedTerminal || terminalRef.current) {
            setState("stopped");
            break;
          }
          consecutiveDisconnects = receivedFrame
            ? 0
            : consecutiveDisconnects + 1;
          setState(consecutiveDisconnects >= 3 ? "polling" : "reconnecting");
        } catch (streamError) {
          if (controller.signal.aborted) break;
          consecutiveDisconnects += 1;
          setState(consecutiveDisconnects >= 3 ? "polling" : "reconnecting");
          setError(
            streamError instanceof Error
              ? streamError.message
              : "Build log stream disconnected",
          );
        }

        await abortableDelay(retryMilliseconds, controller.signal);
        retryMilliseconds = Math.min(retryMilliseconds * 2, 15_000);
      }
    }

    void stream();
    return () => controller.abort();
  }, [buildId, enabled]);

  return { logs, state, error };
}

export function parseCustomBuildFrame(
  frame: string,
):
  | { kind: "log"; sequence: number; entry: CustomBuildLogEntry }
  | { kind: "terminal"; sequence: number }
  | undefined {
  let sequence = 0;
  let eventType = "message";
  const data: string[] = [];
  frame.split(/\r?\n/).forEach((line) => {
    if (line.startsWith("id:")) sequence = Number(line.slice(3).trim());
    else if (line.startsWith("event:")) eventType = line.slice(6).trim();
    else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  });
  if (!Number.isFinite(sequence) || sequence <= 0) return undefined;
  if (eventType === "done" || eventType === "error") {
    return { kind: "terminal", sequence };
  }
  if (eventType !== "log" || data.length === 0) return undefined;
  const line = safeDisplayText(data.join("\n"), 8_192).trimEnd();
  const match = /^\[([a-z0-9_-]{1,32})\]\s?(.*)$/.exec(line);
  return {
    kind: "log",
    sequence,
    entry: {
      sequence,
      variant: match?.[1],
      line: safeDisplayText(match?.[2] ?? line, 8_192),
    },
  };
}

function abortableDelay(
  milliseconds: number,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve();
    const timeout = window.setTimeout(resolve, milliseconds);
    signal.addEventListener(
      "abort",
      () => {
        window.clearTimeout(timeout);
        resolve();
      },
      { once: true },
    );
  });
}
