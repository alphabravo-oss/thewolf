import { useEffect, useRef, useState } from "react";
import type { RolloutEvent } from "@/lib/scanner-supply-chain";
import { safeDisplayText } from "@/lib/safe-display";

type AggregateType =
  | "discovery"
  | "candidate"
  | "release"
  | "rollout"
  | "registry_job";
type StreamState = "connecting" | "live" | "reconnecting" | "stopped" | "error";

const API_BASE = (import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "");

// useScannerEvents reads the durable SSE stream with a real Last-Event-ID
// header. EventSource cannot set that header, so the small fetch reader below
// is used instead. A reconnect receives every event after the last persisted
// sequence; page reload intentionally replays the bounded aggregate history.
export function useScannerEvents(
  aggregate: AggregateType,
  id: string,
  terminal: boolean,
  { replayTerminal = false }: { replayTerminal?: boolean } = {},
): { events: RolloutEvent[]; state: StreamState; error?: string } {
  const [events, setEvents] = useState<RolloutEvent[]>([]);
  const [state, setState] = useState<StreamState>(
    terminal && !replayTerminal ? "stopped" : "connecting",
  );
  const [error, setError] = useState<string>();
  const lastSequence = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    lastSequence.current = 0;
    setEvents([]);
    setError(undefined);

    if (terminal && !replayTerminal) {
      setState("stopped");
      return () => controller.abort();
    }

    async function readStream() {
      let retryMilliseconds = 1_000;
      setState("connecting");
      while (!controller.signal.aborted) {
        try {
          const headers: Record<string, string> = {
            Accept: "text/event-stream",
          };
          if (lastSequence.current > 0) {
            headers["Last-Event-ID"] = String(lastSequence.current);
          }
          const response = await fetch(
            `${API_BASE}/v1/scanner-supply-chain/${aggregatePath(aggregate)}/${encodeURIComponent(id)}/events`,
            {
              credentials: "include",
              headers,
              signal: controller.signal,
            },
          );
          if (!response.ok || !response.body) {
            throw new Error(
              response.status === 403
                ? "Event stream permission denied"
                : `Event stream returned ${response.status}`,
            );
          }
          setState("live");
          setError(undefined);
          retryMilliseconds = 1_000;
          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          while (!controller.signal.aborted) {
            const chunk = await reader.read();
            if (chunk.done) break;
            buffer += decoder.decode(chunk.value, { stream: true });
            const frames = buffer.split(/\r?\n\r?\n/);
            buffer = frames.pop() ?? "";
            frames.forEach((frame) => {
              const parsed = parseFrame(frame);
              if (!parsed || parsed.type === "heartbeat") return;
              if (parsed.sequence <= lastSequence.current) return;
              lastSequence.current = parsed.sequence;
              setEvents((current) => {
                const next = [...current, parsed.event];
                return next.length > 500 ? next.slice(next.length - 500) : next;
              });
            });
          }
          if (terminal) {
            setState("stopped");
            break;
          }
          if (!controller.signal.aborted) setState("reconnecting");
        } catch (streamError) {
          if (controller.signal.aborted) break;
          setState("reconnecting");
          setError(
            streamError instanceof Error
              ? streamError.message
              : "Event stream disconnected",
          );
        }
        await abortableDelay(retryMilliseconds, controller.signal);
        retryMilliseconds = Math.min(retryMilliseconds * 2, 15_000);
      }
    }

    void readStream();
    return () => controller.abort();
  }, [aggregate, id, replayTerminal, terminal]);

  return { events, state, error };
}

function aggregatePath(aggregate: AggregateType): string {
  if (aggregate === "discovery") return "discovery-runs";
  if (aggregate === "registry_job") return "registry-jobs";
  return `${aggregate}s`;
}

export function parseFrame(
  frame: string,
):
  | { type: "heartbeat"; sequence: number }
  | { type: "event"; sequence: number; event: RolloutEvent }
  | undefined {
  let sequence = 0;
  let eventType = "message";
  const data: string[] = [];
  frame.split(/\r?\n/).forEach((line) => {
    if (line.startsWith("id:")) sequence = Number(line.slice(3).trim());
    else if (line.startsWith("event:")) eventType = line.slice(6).trim();
    else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  });
  if (eventType === "heartbeat") return { type: "heartbeat", sequence };
  if (!Number.isFinite(sequence) || sequence <= 0 || data.length === 0) {
    return undefined;
  }
  try {
    const event = JSON.parse(data.join("\n")) as RolloutEvent;
    if (!event || typeof event !== "object") return undefined;
    return {
      type: "event",
      sequence,
      event: {
        id: safeDisplayText(event.id, 128) || undefined,
        aggregate_type: safeDisplayText(event.aggregate_type, 64) || undefined,
        aggregate_id: safeDisplayText(event.aggregate_id, 256) || undefined,
        sequence,
        event_type: safeDisplayText(event.event_type || eventType, 128),
        prior_state: safeDisplayText(event.prior_state, 64) || undefined,
        new_state: safeDisplayText(event.new_state, 64) || undefined,
        actor: safeDisplayText(event.actor, 256) || undefined,
        reason: safeDisplayText(event.reason, 2_048) || undefined,
        trace_id: safeDisplayText(event.trace_id, 128) || undefined,
        operation_id: safeDisplayText(event.operation_id, 128) || undefined,
        parent_operation_id:
          safeDisplayText(event.parent_operation_id, 128) || undefined,
        created_at: safeDisplayText(event.created_at, 64),
      },
    };
  } catch {
    return undefined;
  }
}

function abortableDelay(
  milliseconds: number,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) {
      resolve();
      return;
    }
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
