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

const BASE_URL = import.meta.env.VITE_API_URL ?? "/api";

export function getToken(): string | null {
  return null;
}

export function setToken(_token: string) {
  // Compatibility no-op. The API now sets the wolf_token session cookie
  // with HttpOnly, Secure-on-HTTPS, and SameSite=Strict.
}

export function clearToken() {
  if (typeof document === "undefined") return;
  document.cookie = "wolf_token=; path=/; max-age=0; SameSite=Strict";
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

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<ApiResponse<T>> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers as Record<string, string>),
  };
  const res = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers,
    credentials: "include",
  });

  // Hard 401 = stale/missing cookie, rotated JWT signing secret, or logout.
  // Bounce authenticated areas to /login so the user gets a clean re-auth.
  if (res.status === 401) {
    clearToken();
    if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
      window.location.replace("/login");
    }
  }

  if (res.status === 204) return { data: null as T };
  const body: ApiResponse<T> = await res.json();
  if (!res.ok) {
    throw new ApiError(
      res.status,
      body.error?.code || "UNKNOWN",
      body.error?.message || res.statusText,
      body.error?.details,
    );
  }
  return body;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, data?: unknown) =>
    request<T>(path, {
      method: "POST",
      body: data ? JSON.stringify(data) : undefined,
    }),
  put: <T>(path: string, data?: unknown) =>
    request<T>(path, {
      method: "PUT",
      body: data ? JSON.stringify(data) : undefined,
    }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};

export async function hasSession(): Promise<boolean> {
  try {
    const res = await fetch(`${BASE_URL}/auth/me`, {
      method: "GET",
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    return res.ok;
  } catch {
    return false;
  }
}

export default api;
