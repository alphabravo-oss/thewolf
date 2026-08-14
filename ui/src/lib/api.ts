// Wolf API client. Same shape as the old Next.js version (`request<T>`,
// `api.{get,post,put,delete}`, `getToken/setToken/clearToken`,
// `ApiError`), but without Next-specific assumptions:
//
//   - BASE_URL falls back to "/api" so the Vite dev-server proxy (and the
//     Go server serving the SPA from the same origin) Just Works.
//   - typeof document checks remain because TanStack Router can prerender
//     route trees during build.
//   - Browser auth is carried by the server-set HttpOnly wolf_token cookie.
import type { ApiResponse } from "./types";

const BASE_URL = import.meta.env.VITE_API_URL ?? "/api/v1";

export function getToken(): string | null {
  return null;
}

export function setToken(_token: string) {
  // Compatibility no-op. The API now sets the wolf_token session cookie
  // with HttpOnly, Secure-on-HTTPS, and SameSite=Strict.
}

export function clearToken() {
  if (typeof document === "undefined") return;
  // Match the server cookie (HttpOnly + Secure on HTTPS) so a client
  // logout actually drops it. Restart 401s must not call this.
  const secure = typeof location !== "undefined" && location.protocol === "https:" ? "; Secure" : "";
  document.cookie = `wolf_token=; path=/; max-age=0; SameSite=Strict${secure}`;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: Record<string, unknown>,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ApiResult<T> {
  response: ApiResponse<T>;
  headers: Headers;
  status: number;
}

async function requestWithMetadata<T>(
  path: string,
  options: RequestInit = {},
): Promise<ApiResult<T>> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers as Record<string, string>),
  };
  const res = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers,
    credentials: "include",
  });

  // Only bounce to login when the session is actually gone. A 401 during
  // a wolf restart (or a 502 from Caddy) must not wipe a still-valid cookie.
  if (res.status === 401) {
    await bounceIfSessionGone();
  }

  if (res.status === 204) {
    return {
      response: { data: null as T },
      headers: res.headers,
      status: res.status,
    };
  }
  const body = (await res.json()) as ApiResponse<T> | T;
  if (!res.ok) {
    const errorBody = body as ApiResponse<T>;
    throw new ApiError(
      res.status,
      errorBody.error?.code || "UNKNOWN",
      errorBody.error?.message || res.statusText,
      errorBody.error?.details,
    );
  }
  // Durable command endpoints intentionally return their operation receipt at
  // the top level (`{id,state,status_url,...}`), while resource reads use the
  // established `{data: ...}` envelope. Normalize both shapes here so callers
  // retain one typed client contract.
  if (
    typeof body === "object" &&
    body !== null &&
    ("data" in body || "error" in body)
  ) {
    return {
      response: body as ApiResponse<T>,
      headers: res.headers,
      status: res.status,
    };
  }
  return {
    response: { data: body as T },
    headers: res.headers,
    status: res.status,
  };
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<ApiResponse<T>> {
  return (await requestWithMetadata<T>(path, options)).response;
}

async function download(path: string): Promise<{
  blob: Blob;
  filename?: string;
  headers: Headers;
}> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: "GET",
    credentials: "include",
    headers: { Accept: "application/vnd.wolf.scanner-release-bundle.v1+zstd" },
  });
  if (res.status === 401) {
    await bounceIfSessionGone();
  }
  if (!res.ok) {
    let code = "DOWNLOAD_FAILED";
    let message = res.statusText || "Download failed";
    try {
      const body = (await res.json()) as ApiResponse<unknown>;
      code = body.error?.code || code;
      message = body.error?.message || message;
    } catch {
      // Non-JSON proxy errors intentionally retain the generic status text.
    }
    throw new ApiError(res.status, code, message);
  }
  const disposition = res.headers.get("Content-Disposition") ?? "";
  const match = /filename="([^"]+)"/i.exec(disposition);
  return {
    blob: await res.blob(),
    filename: match?.[1],
    headers: res.headers,
  };
}

export const api = {
  get: <T>(path: string, options?: RequestInit) => request<T>(path, options),
  getWithMetadata: <T>(path: string, options?: RequestInit) =>
    requestWithMetadata<T>(path, options),
  post: <T>(path: string, data?: unknown, options?: RequestInit) =>
    request<T>(path, {
      ...options,
      method: "POST",
      body: data ? JSON.stringify(data) : undefined,
    }),
  postWithMetadata: <T>(path: string, data?: unknown, options?: RequestInit) =>
    requestWithMetadata<T>(path, {
      ...options,
      method: "POST",
      body: data ? JSON.stringify(data) : undefined,
    }),
  put: <T>(path: string, data?: unknown, options?: RequestInit) =>
    request<T>(path, {
      ...options,
      method: "PUT",
      body: data ? JSON.stringify(data) : undefined,
    }),
  patch: <T>(path: string, data?: unknown, options?: RequestInit) =>
    request<T>(path, {
      ...options,
      method: "PATCH",
      body: data ? JSON.stringify(data) : undefined,
    }),
  delete: <T>(path: string, options?: RequestInit) =>
    request<T>(path, { ...options, method: "DELETE" }),
  download,
};

export type SessionStatus = "ok" | "none" | "offline";

export async function sessionStatus(): Promise<SessionStatus> {
  try {
    const res = await fetch(`${BASE_URL}/auth/me`, {
      method: "GET",
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    if (res.ok) return "ok";
    if (res.status === 401) return "none";
    return "offline";
  } catch {
    return "offline";
  }
}

export async function hasSession(): Promise<boolean> {
  return (await waitForSession()) === "ok";
}

/** Retry through brief API downtime so a compose recreate does not look like logout. */
export async function waitForSession(
  attempts = 12,
  delayMs = 500,
): Promise<SessionStatus> {
  let last: SessionStatus = "offline";
  for (let i = 0; i < attempts; i++) {
    last = await sessionStatus();
    if (last !== "offline") return last;
    if (i < attempts - 1) {
      await new Promise((r) => setTimeout(r, delayMs));
    }
  }
  return last;
}

let bounceInFlight: Promise<void> | null = null;

async function bounceIfSessionGone() {
  if (typeof window === "undefined") return;
  if (window.location.pathname.startsWith("/login")) return;
  if (!bounceInFlight) {
    bounceInFlight = (async () => {
      const status = await waitForSession(6, 400);
      if (status === "none") {
        window.location.replace(
          `/login?from=${encodeURIComponent(window.location.pathname)}`,
        );
      }
    })().finally(() => {
      bounceInFlight = null;
    });
  }
  await bounceInFlight;
}

export default api;
