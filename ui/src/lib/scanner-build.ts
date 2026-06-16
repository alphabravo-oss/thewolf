// Typed client + streaming hook for the server-driven scanner image build/push
// subsystem (Settings → Scanner Images).
//
// Two pieces:
//   - useScannerImages(): per-variant local/remote digest status, reusing the
//     existing GET /scanners/images endpoint.
//   - streamBuild(variant, push, onLine): POSTs to the SSE build endpoint and
//     streams docker buildx output line-by-line. The build endpoints are POST
//     (the request body carries {push}), so the native EventSource (GET-only)
//     can't be used — we read the response body as a stream and parse the
//     `data:` / `event:` frames by hand. Auth rides the HttpOnly wolf_token
//     cookie via `credentials: "include"`.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

const BASE_URL = import.meta.env.VITE_API_URL ?? "/api";

// One row of GET /scanners/images — mirrors the backend imageStatus struct.
export interface ScannerImageStatus {
  image: string;
  local_digest?: string;
  remote_digest?: string;
  updates_available: boolean;
  local_error?: string;
  remote_error?: string;
}

// useScannerImages reuses GET /scanners/images for the per-variant status
// table. The list is keyed by full image ref (e.g. alphabravodevops/
// wolf-scanners:2.0.0); callers map it onto the variant rows they render.
export function useScannerImages() {
  return useQuery({
    queryKey: ["scanners", "images"],
    queryFn: async () => {
      const r = await api.get<ScannerImageStatus[]>("/scanners/images");
      return r.data ?? [];
    },
  });
}

// The terminal SSE frame emitted by the build endpoints. `event: done` carries
// BuildDone; `event: error` carries BuildError.
export interface BuildDone {
  variant: string;
  refs?: string[];
  digest?: string;
  loaded_locally: boolean;
  pushed: boolean;
}

export interface BuildError {
  variant: string;
  error: string;
}

// Callbacks the caller can hook into while a build streams. onLine fires for
// every `data:` log line; onDone / onError fire once at the terminal frame.
export interface StreamBuildHandlers {
  onLine: (line: string) => void;
  onDone?: (done: BuildDone) => void;
  onError?: (err: BuildError) => void;
  signal?: AbortSignal;
}

// streamBuild POSTs {push} to /scanners/images/{variant}/build (or
// /scanners/images/build-all when variant === "all") and streams the SSE
// response. Resolves when the stream closes. Network/HTTP failures reject; an
// `event: error` frame is surfaced via onError and resolves normally (the
// stream completed, the build itself failed).
export async function streamBuild(
  variant: string,
  push: boolean,
  handlers: StreamBuildHandlers,
  opts?: { multiArch?: boolean },
): Promise<void> {
  const path =
    variant === "all"
      ? "/scanners/images/build-all"
      : `/scanners/images/${encodeURIComponent(variant)}/build`;

  // A multi-arch build implies push (a manifest list can't be loaded locally);
  // the server forces it too, but we send push=true so the UI's cred check is
  // consistent.
  const multiArch = opts?.multiArch ?? false;
  const res = await fetch(`${BASE_URL}${path}`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      Accept: "text/event-stream",
    },
    body: JSON.stringify({ push: push || multiArch, multi_arch: multiArch }),
    signal: handlers.signal,
  });

  if (!res.ok || !res.body) {
    let message = res.statusText;
    try {
      const body = await res.json();
      message = body?.error?.message || message;
    } catch {
      // non-JSON body; keep statusText
    }
    throw new Error(message || `build request failed (${res.status})`);
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  // SSE frames are separated by a blank line. We accumulate bytes, split on
  // \n\n, and parse each complete frame's `event:` + `data:` fields.
  const flushFrame = (frame: string) => {
    let event = "message";
    const dataLines: string[] = [];
    for (const raw of frame.split("\n")) {
      if (raw.startsWith("event:")) {
        event = raw.slice("event:".length).trim();
      } else if (raw.startsWith("data:")) {
        dataLines.push(raw.slice("data:".length).replace(/^ /, ""));
      }
    }
    const data = dataLines.join("\n");
    if (event === "done") {
      try {
        handlers.onDone?.(JSON.parse(data) as BuildDone);
      } catch {
        // ignore malformed terminal frame
      }
    } else if (event === "error") {
      try {
        handlers.onError?.(JSON.parse(data) as BuildError);
      } catch {
        handlers.onError?.({ variant, error: data || "build failed" });
      }
    } else if (data.length > 0) {
      handlers.onLine(data);
    }
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let sep: number;
    while ((sep = buffer.indexOf("\n\n")) !== -1) {
      const frame = buffer.slice(0, sep);
      buffer = buffer.slice(sep + 2);
      if (frame.trim().length > 0) flushFrame(frame);
    }
  }
  // Flush any trailing partial frame (server closed without a final blank line).
  if (buffer.trim().length > 0) flushFrame(buffer);
}
