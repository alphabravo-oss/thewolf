import { ApiError } from "./api";

export const COMMUNITY_LIMIT_COPY =
  "Community evaluation limit reached (5 repos, 3 users, or 1 concurrent scan). See Settings → License.";

export function isCommunityLimit(error: unknown): boolean {
  return error instanceof ApiError && error.code === "community_limit";
}

const ASSIGNMENT_SECRET =
  /(\b(?:authorization|credential|password|passwd|private[_-]?key|access[_-]?key|token|secret|api[_-]?key)\b["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;}]+)/gi;
const BEARER_SECRET = /\bBearer\s+[A-Za-z0-9._~+/=-]{6,}/gi;
const KNOWN_TOKEN =
  /\b(?:dckr_pat_|github_pat_|gh[pousr]_|sk-(?:proj-)?)[A-Za-z0-9_-]{6,}\b/g;

const DEFAULT_EVIDENCE_HOSTS = [
  "bitbucket.org",
  "docker.com",
  "docker.io",
  "gcr.io",
  "github.com",
  "gitlab.com",
  "ghcr.io",
  "go.dev",
  "microsoft.com",
  "npmjs.com",
  "npmjs.org",
  "pkg.dev",
  "pkg.go.dev",
  "pypi.org",
  "pythonhosted.org",
  "quay.io",
  "rubygems.org",
] as const;

export interface SafeEvidenceHrefOptions {
  baseUrl?: string;
  additionalHosts?: readonly string[];
}

export function safeDisplayText(value: unknown, maxLength = 4_096): string {
  if (typeof value !== "string" || maxLength <= 0) return "";
  const boundedInput = value.slice(0, Math.max(maxLength * 2, maxLength));
  const controlSafe = [...boundedInput]
    .filter((character) => {
      const code = character.charCodeAt(0);
      return (
        code === 9 || code === 10 || code === 13 || (code >= 32 && code !== 127)
      );
    })
    .join("");
  const redacted = controlSafe
    .replace(BEARER_SECRET, "Bearer [REDACTED]")
    .replace(ASSIGNMENT_SECRET, "$1[REDACTED]")
    .replace(KNOWN_TOKEN, "[REDACTED]");
  if (redacted.length <= maxLength) return redacted;
  return `${redacted.slice(0, Math.max(0, maxLength - 14))}… [truncated]`;
}

export function safeErrorMessage(
  error: unknown,
  fallback = "The operation could not be completed. Retry or review the operation audit.",
): string {
  if (!error || typeof error !== "object") return fallback;
  const candidate = error as {
    name?: unknown;
    status?: unknown;
    message?: unknown;
    code?: unknown;
  };
  if (candidate.name !== "ApiError") {
    return safeDisplayText(candidate.message, 320) || fallback;
  }
  if (candidate.code === "community_limit") {
    return COMMUNITY_LIMIT_COPY;
  }
  switch (candidate.status) {
    case 401:
      return "Your session expired. Sign in again to continue.";
    case 403:
      return "Your account does not have permission for this operation.";
    case 404:
      return "The requested resource is no longer available. Reload the page.";
    case 409:
    case 412:
      return "This resource changed on the server. Reload it before trying again.";
    case 422:
      return "The server rejected this request. Review the entered values and policy requirements.";
    case 429:
      return "The service is rate limiting requests. Wait briefly, then retry.";
    default:
      return typeof candidate.status === "number" && candidate.status >= 500
        ? "The service could not complete this operation. Retry or review service health."
        : fallback;
  }
}

export function safeBackendFailureMessage(
  errorClass: unknown,
  fallback = "The operation did not complete. Review bounded evidence and the operation audit before retrying.",
): string {
  if (typeof errorClass !== "string") return fallback;
  const normalized = errorClass.trim().toLowerCase();
  if (!/^[a-z][a-z0-9_.-]{0,63}$/.test(normalized)) return fallback;
  const label = normalized
    .replaceAll(/[_.-]+/g, " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
  return `${label}. Review bounded evidence and the operation audit before retrying.`;
}

export function safeEvidenceHref(
  value: unknown,
  options: SafeEvidenceHrefOptions = {},
): string | undefined {
  if (typeof value !== "string") return undefined;
  const candidate = value.trim();
  if (
    !candidate ||
    candidate.length > 2_048 ||
    [...candidate].some((character) => {
      const code = character.charCodeAt(0);
      return code < 32 || code === 127;
    })
  ) {
    return undefined;
  }
  const baseUrl =
    options.baseUrl ??
    (typeof window === "undefined" ? "https://wolf.invalid" : window.location.href);
  let base: URL;
  let parsed: URL;
  try {
    base = new URL(baseUrl);
    parsed = new URL(candidate, base);
  } catch {
    return undefined;
  }
  if (parsed.username || parsed.password) return undefined;

  const sameOrigin = parsed.origin === base.origin;
  const relativePath = candidate.startsWith("/") && !candidate.startsWith("//");
  if (sameOrigin && (parsed.protocol === "https:" || parsed.protocol === "http:")) {
    return relativePath ? candidate : parsed.href;
  }
  if (parsed.protocol !== "https:") return undefined;

  const configuredHosts =
    typeof import.meta.env.VITE_SCANNER_EVIDENCE_HOSTS === "string"
      ? import.meta.env.VITE_SCANNER_EVIDENCE_HOSTS.split(",")
      : [];
  const allowedHosts = [
    ...DEFAULT_EVIDENCE_HOSTS,
    ...configuredHosts,
    ...(options.additionalHosts ?? []),
  ]
    .map((host) => host.trim().toLowerCase().replace(/^\.+/, ""))
    .filter((host) => /^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(host));
  const hostname = parsed.hostname.toLowerCase();
  if (
    !allowedHosts.some(
      (host) => hostname === host || hostname.endsWith(`.${host}`),
    )
  ) {
    return undefined;
  }
  return parsed.href;
}
