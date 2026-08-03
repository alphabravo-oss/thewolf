import { api } from "./api";
import { rememberOperationReceipt } from "./operation-receipts";
import {
  createIdempotencyKey,
  isValidScannerOperationId,
  isValidScannerTraceId,
} from "./scanner-supply-chain";

export const SCANNER_CUSTOM_BUILDS_PATH = "/v1/scanners/custom-builds";
export const CUSTOM_BUILD_VARIANTS = [
  "default",
  "jvm",
  "rust",
  "codeql",
] as const;
export const CUSTOM_BUILD_PLATFORMS = ["linux/amd64", "linux/arm64"] as const;

export type CustomBuildVariantName = (typeof CUSTOM_BUILD_VARIANTS)[number];
export type CustomBuildPlatform = (typeof CUSTOM_BUILD_PLATFORMS)[number];
export type CustomBuildState =
  | "queued"
  | "claimed"
  | "running"
  | "completed"
  | "partial"
  | "failed"
  | "cancelled";
export type CustomBuildVariantState =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface CustomBuild {
  id: string;
  variants: CustomBuildVariantName[];
  push: boolean;
  platforms: CustomBuildPlatform[];
  namespace?: string;
  reserved_version?: string;
  state: CustomBuildState;
  actor?: string;
  reason: string;
  attempt: number;
  max_attempts: number;
  available_at?: string;
  cancel_requested_at?: string;
  lease_expires_at?: string;
  heartbeat_at?: string;
  error_class?: string;
  version: number;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface CustomBuildVariant {
  id: string;
  build_id: string;
  variant: CustomBuildVariantName;
  ordinal: number;
  state: CustomBuildVariantState;
  refs: string[];
  digest?: string;
  loaded_locally: boolean;
  pushed: boolean;
  error_class?: string;
  version: number;
  started_at?: string;
  completed_at?: string;
  created_at?: string;
  updated_at?: string;
}

export interface CustomBuildInventory {
  build: CustomBuild;
  variants: CustomBuildVariant[];
  etag?: string;
}

export interface CustomBuildPage {
  items: CustomBuild[];
  next_cursor?: string;
}

export interface CustomBuildCreateInput {
  variants: Array<CustomBuildVariantName | "all">;
  push: boolean;
  platforms?: CustomBuildPlatform[];
  namespace?: string;
  reason: string;
}

export interface CustomBuildOperationReceipt {
  id: string;
  state: CustomBuildState;
  status_url?: string;
  events_url?: string;
  operation_id?: string;
  trace_id?: string;
}

type RawCustomBuild = Record<string, unknown>;
type RawCustomBuildInventory = {
  build?: RawCustomBuild;
  variants?: Array<Record<string, unknown>>;
};

export const scannerCustomBuildApi = {
  list: async (
    filters: {
      state?: CustomBuildState | "";
      cursor?: string;
      limit?: number;
    } = {},
  ): Promise<CustomBuildPage> => {
    const params = new URLSearchParams();
    if (filters.state) params.set("state", filters.state);
    if (filters.cursor) params.set("cursor", filters.cursor);
    if (filters.limit) params.set("limit", String(filters.limit));
    const query = params.toString();
    const response = await api.get<RawCustomBuild[]>(
      `${SCANNER_CUSTOM_BUILDS_PATH}${query ? `?${query}` : ""}`,
    );
    return {
      items: (response.data ?? []).map(normalizeBuild),
      next_cursor: response.meta?.next_cursor,
    };
  },

  create: async (
    input: CustomBuildCreateInput,
  ): Promise<CustomBuildOperationReceipt> => {
    const result = await api.postWithMetadata<CustomBuildOperationReceipt>(
      SCANNER_CUSTOM_BUILDS_PATH,
      input,
      { headers: { "Idempotency-Key": createIdempotencyKey() } },
    );
    const operationId = result.headers.get("X-Wolf-Operation-ID") ?? "";
    const traceId = result.headers.get("X-Wolf-Trace-ID") ?? "";
    const receipt = {
      ...result.response.data,
      operation_id: isValidScannerOperationId(operationId)
        ? operationId
        : undefined,
      trace_id: isValidScannerTraceId(traceId) ? traceId : undefined,
    };
    rememberOperationReceipt(receipt, "Custom scanner build");
    return receipt;
  },

  detail: async (id: string): Promise<CustomBuildInventory> => {
    const result = await api.getWithMetadata<RawCustomBuildInventory>(
      `${SCANNER_CUSTOM_BUILDS_PATH}/${encodeURIComponent(id)}`,
    );
    if (!result.response.data.build) {
      throw new Error("Custom build response did not include a build");
    }
    return {
      build: normalizeBuild(result.response.data.build),
      variants: (result.response.data.variants ?? [])
        .map(normalizeVariant)
        .filter((variant): variant is CustomBuildVariant => Boolean(variant)),
      etag: result.headers.get("ETag") ?? undefined,
    };
  },

  cancel: async (
    id: string,
    reason: string,
    etag: string | number,
  ): Promise<CustomBuild> => {
    const build = normalizeBuild(
      (
        await api.post<RawCustomBuild>(
          `${SCANNER_CUSTOM_BUILDS_PATH}/${encodeURIComponent(id)}/cancel`,
          { reason },
          {
            headers: {
              "Idempotency-Key": createIdempotencyKey(),
              "If-Match": String(etag),
            },
          },
        )
      ).data,
    );
    rememberOperationReceipt(
      {
        id: build.id,
        state: build.state,
        status_url: `${SCANNER_CUSTOM_BUILDS_PATH}/${encodeURIComponent(build.id)}`,
      },
      "Custom scanner build cancellation",
    );
    return build;
  },

  retry: async (
    id: string,
    reason: string,
    etag: string | number,
  ): Promise<CustomBuild> => {
    const build = normalizeBuild(
      (
        await api.post<RawCustomBuild>(
          `${SCANNER_CUSTOM_BUILDS_PATH}/${encodeURIComponent(id)}/retry`,
          { reason },
          {
            headers: {
              "Idempotency-Key": createIdempotencyKey(),
              "If-Match": String(etag),
            },
          },
        )
      ).data,
    );
    rememberOperationReceipt(
      {
        id: build.id,
        state: build.state,
        status_url: `${SCANNER_CUSTOM_BUILDS_PATH}/${encodeURIComponent(build.id)}`,
      },
      "Custom scanner build retry",
    );
    return build;
  },
};

export function validateCustomBuildInput(
  input: CustomBuildCreateInput,
): string[] {
  const errors: string[] = [];
  const variants: CustomBuildVariantName[] =
    input.variants.length === 1 && input.variants[0] === "all"
      ? [...CUSTOM_BUILD_VARIANTS]
      : input.variants.filter(
          (variant): variant is CustomBuildVariantName => variant !== "all",
        );
  if (variants.length === 0)
    errors.push("Choose at least one scanner variant.");
  if (!input.reason.trim()) errors.push("Reason is required.");
  if (input.reason.trim().length > 2_048) {
    errors.push("Reason must be 2,048 characters or fewer.");
  }
  if (
    input.push &&
    input.namespace &&
    (input.namespace.length > 255 || !/^[a-z0-9._-]+$/.test(input.namespace))
  ) {
    errors.push(
      "Registry namespace may contain only lowercase letters, numbers, periods, underscores, and hyphens.",
    );
  }
  if (input.push && variants.includes("codeql")) {
    errors.push(
      input.variants[0] === "all"
        ? "Build all cannot be pushed because CodeQL is local-only. Run a local all-variant build, or push Default, JVM, and Rust separately."
        : "CodeQL images are local-only and cannot be pushed or redistributed.",
    );
  }
  if (!input.push && (input.platforms?.length ?? 0) > 1) {
    errors.push(
      "Local builds support one platform because the result is loaded into this host.",
    );
  }
  if (
    input.platforms?.some(
      (platform) => !CUSTOM_BUILD_PLATFORMS.includes(platform),
    )
  ) {
    errors.push("Choose only supported Linux build platforms.");
  }
  return errors;
}

export function isTerminalCustomBuild(state?: CustomBuildState): boolean {
  return (
    state === "completed" ||
    state === "partial" ||
    state === "failed" ||
    state === "cancelled"
  );
}

export function customBuildRemediation(
  errorClass?: string,
): string | undefined {
  if (!errorClass) return undefined;
  const normalized = errorClass.toLowerCase();
  if (normalized.includes("credential") || normalized.includes("auth")) {
    return "Verify the configured DockerHub credential in Settings, then retry.";
  }
  if (normalized.includes("platform") || normalized.includes("buildx")) {
    return "Verify the host buildx builder supports the requested platform, then retry.";
  }
  if (normalized.includes("cancel")) {
    return "The build was cancelled. Retry when the maintenance window is ready.";
  }
  if (
    normalized.includes("registry") ||
    normalized.includes("push") ||
    normalized.includes("network")
  ) {
    return "Verify registry reachability and permissions, then retry.";
  }
  if (normalized.includes("lease") || normalized.includes("worker")) {
    return "Check custom-build worker health and capacity before retrying.";
  }
  return "Review the bounded build log, correct the reported build condition, then retry.";
}

function normalizeBuild(raw: RawCustomBuild): CustomBuild {
  return {
    id: stringValue(raw.id),
    variants: parseAllowlistedArray(raw.variants, CUSTOM_BUILD_VARIANTS),
    push: raw.push === true,
    platforms: parseAllowlistedArray(raw.platforms, CUSTOM_BUILD_PLATFORMS),
    namespace: optionalString(raw.namespace),
    reserved_version: optionalString(raw.reserved_version),
    state: customBuildState(raw.state),
    actor: optionalString(raw.actor),
    reason: stringValue(raw.reason),
    attempt: numberValue(raw.attempt),
    max_attempts: numberValue(raw.max_attempts),
    available_at: optionalString(raw.available_at),
    cancel_requested_at: optionalString(raw.cancel_requested_at),
    lease_expires_at: optionalString(raw.lease_expires_at),
    heartbeat_at: optionalString(raw.heartbeat_at),
    error_class: optionalString(raw.error_class),
    version: numberValue(raw.version),
    started_at: optionalString(raw.started_at),
    completed_at: optionalString(raw.completed_at),
    created_at: stringValue(raw.created_at),
    updated_at: stringValue(raw.updated_at),
  };
}

function normalizeVariant(
  raw: Record<string, unknown>,
): CustomBuildVariant | undefined {
  const variant = raw.variant;
  if (
    typeof variant !== "string" ||
    !CUSTOM_BUILD_VARIANTS.includes(variant as CustomBuildVariantName)
  ) {
    return undefined;
  }
  return {
    id: stringValue(raw.id),
    build_id: stringValue(raw.build_id),
    variant: variant as CustomBuildVariantName,
    ordinal: numberValue(raw.ordinal),
    state: customBuildVariantState(raw.state),
    refs: parseSafeRefs(raw.refs),
    digest: optionalString(raw.digest),
    loaded_locally: raw.loaded_locally === true,
    pushed: raw.pushed === true,
    error_class: optionalString(raw.error_class),
    version: numberValue(raw.version),
    started_at: optionalString(raw.started_at),
    completed_at: optionalString(raw.completed_at),
    created_at: optionalString(raw.created_at),
    updated_at: optionalString(raw.updated_at),
  };
}

function parseAllowlistedArray<T extends string>(
  value: unknown,
  allowed: readonly T[],
): T[] {
  let parsed = value;
  if (typeof value === "string") {
    try {
      parsed = JSON.parse(value);
    } catch {
      return [];
    }
  }
  if (!Array.isArray(parsed)) return [];
  return parsed.filter(
    (item): item is T =>
      typeof item === "string" && allowed.includes(item as T),
  );
}

function parseSafeRefs(value: unknown): string[] {
  let parsed = value;
  if (typeof value === "string") {
    try {
      parsed = JSON.parse(value);
    } catch {
      return [];
    }
  }
  if (!Array.isArray(parsed)) return [];
  return parsed
    .filter((item): item is string => typeof item === "string")
    .map((item) =>
      [...item]
        .filter((character) => {
          const code = character.charCodeAt(0);
          return code >= 32 && code !== 127;
        })
        .join("")
        .trim(),
    )
    .filter(Boolean)
    .slice(0, 20)
    .map((item) => item.slice(0, 512));
}

function customBuildState(value: unknown): CustomBuildState {
  return (
    typeof value === "string" &&
    [
      "queued",
      "claimed",
      "running",
      "completed",
      "partial",
      "failed",
      "cancelled",
    ].includes(value)
      ? value
      : "failed"
  ) as CustomBuildState;
}

function customBuildVariantState(value: unknown): CustomBuildVariantState {
  return (
    typeof value === "string" &&
    ["queued", "running", "completed", "failed", "cancelled"].includes(value)
      ? value
      : "failed"
  ) as CustomBuildVariantState;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function optionalString(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined;
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}
