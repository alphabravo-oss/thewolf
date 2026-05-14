// Wolf API client. Same shape as the old Next.js version (`request<T>`,
// `api.{get,post,put,delete}`, `getToken/setToken/clearToken`,
// `ApiError`), but without Next-specific assumptions:
//
//   - BASE_URL falls back to "/api" so the Vite dev-server proxy (and the
//     Go server serving the SPA from the same origin) Just Works.
//   - typeof document checks remain because TanStack Router can prerender
//     route trees during build.
import type { ApiResponse } from "./types";

const BASE_URL = import.meta.env.VITE_API_URL ?? "/api";

export function getToken(): string | null {
  if (typeof document === "undefined") return null;
  const m = document.cookie.match(/(?:^|;\s*)wolf_token=([^;]*)/);
  return m ? decodeURIComponent(m[1]) : null;
}

export function setToken(token: string) {
  document.cookie = `wolf_token=${encodeURIComponent(token)}; path=/; max-age=${
    60 * 60 * 24 * 7
  }; SameSite=Lax`;
}

export function clearToken() {
  document.cookie = "wolf_token=; path=/; max-age=0";
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
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers as Record<string, string>),
  };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  const res = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers,
    credentials: "include",
  });
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

export default api;
