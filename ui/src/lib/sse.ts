// Keep one EventSource through transient failures so the browser retains
// Last-Event-ID and honors the server's retry interval.
import { useEffect, useRef, useCallback, useState } from "react";

const BASE_URL = import.meta.env.VITE_API_URL ?? "/api/v1";

interface UseSSEOptions<T> {
  path: string;
  enabled?: boolean;
  onEvent?: (event: T) => void;
  onError?: (error: Event) => void;
  onOpen?: () => void;
}

export function useSSE<T = Record<string, unknown>>({
  path, enabled = true, onEvent, onError, onOpen,
}: UseSSEOptions<T>) {
  const [connected, setConnected] = useState(false);
  const [lastEvent, setLastEvent] = useState<T | null>(null);
  const sourceRef = useRef<EventSource | null>(null);
  const callbacks = useRef({ onEvent, onError, onOpen });
  useEffect(() => { callbacks.current = { onEvent, onError, onOpen }; });

  useEffect(() => {
    if (!enabled) return;
    const source = new EventSource(`${BASE_URL}${path}`, { withCredentials: true });
    sourceRef.current = source;
    source.onopen = () => {
      setConnected(true);
      callbacks.current.onOpen?.();
    };
    source.onmessage = (event) => {
      let data: T;
      try { data = JSON.parse(event.data) as T; } catch { return; }
      setLastEvent(data);
      callbacks.current.onEvent?.(data);
    };
    source.onerror = (event) => {
      setConnected(false);
      callbacks.current.onError?.(event);
    };
    return () => {
      source.onopen = source.onmessage = source.onerror = null;
      source.close();
      sourceRef.current = null;
      setConnected(false);
    };
  }, [enabled, path]);

  const disconnect = useCallback(() => {
    sourceRef.current?.close();
    setConnected(false);
  }, []);
  return { connected, lastEvent, disconnect };
}
