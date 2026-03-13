"use client";

import { useEffect, useRef, useCallback, useState } from "react";

const BASE_URL =
  process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api";

interface UseSSEOptions<T> {
  path: string;
  enabled?: boolean;
  onEvent?: (event: T) => void;
  onError?: (error: Event) => void;
  onOpen?: () => void;
  reconnectInterval?: number;
}

export function useSSE<T = Record<string, unknown>>({
  path,
  enabled = true,
  onEvent,
  onError,
  onOpen,
  reconnectInterval = 3000,
}: UseSSEOptions<T>) {
  const [connected, setConnected] = useState(false);
  const [lastEvent, setLastEvent] = useState<T | null>(null);
  const sourceRef = useRef<EventSource | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const connectRef = useRef<() => void>(() => {});

  const onEventRef = useRef(onEvent);
  const onErrorRef = useRef(onError);
  const onOpenRef = useRef(onOpen);
  const enabledRef = useRef(enabled);

  useEffect(() => {
    onEventRef.current = onEvent;
    onErrorRef.current = onError;
    onOpenRef.current = onOpen;
    enabledRef.current = enabled;
  });

  useEffect(() => {
    if (!enabled) {
      // Close existing connection on cleanup (no synchronous setState)
      const currentSource = sourceRef.current;
      const currentTimer = reconnectTimerRef.current;
      return () => {
        currentSource?.close();
        if (currentTimer) clearTimeout(currentTimer);
      };
    }

    function doConnect() {
      if (sourceRef.current) {
        sourceRef.current.close();
      }

      const url = `${BASE_URL}${path}`;
      const source = new EventSource(url, { withCredentials: true });
      sourceRef.current = source;

      source.onopen = () => {
        setConnected(true);
        onOpenRef.current?.();
      };

      source.onmessage = (e) => {
        try {
          const data = JSON.parse(e.data) as T;
          setLastEvent(data);
          onEventRef.current?.(data);
        } catch {
          // ignore parse errors
        }
      };

      source.onerror = (e) => {
        setConnected(false);
        onErrorRef.current?.(e);
        source.close();

        reconnectTimerRef.current = setTimeout(() => {
          if (enabledRef.current) connectRef.current();
        }, reconnectInterval);
      };
    }

    connectRef.current = doConnect;
    doConnect();

    return () => {
      sourceRef.current?.close();
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
      }
      setConnected(false);
    };
  }, [enabled, path, reconnectInterval]);

  const disconnect = useCallback(() => {
    sourceRef.current?.close();
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current);
    }
    setConnected(false);
  }, []);

  return { connected, lastEvent, disconnect };
}
