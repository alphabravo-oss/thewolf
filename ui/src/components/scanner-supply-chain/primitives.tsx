import { memo, type ReactNode } from "react";
import {
  AlertCircleIcon,
  BanIcon,
  CheckCircle2Icon,
  Clock3Icon,
  CloudOffIcon,
  LoaderCircleIcon,
  ShieldAlertIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { CardSkeleton, TableSkeleton } from "@/components/skeleton";
import { cn } from "@/lib/utils";
import { errorKind, type Risk } from "@/lib/scanner-supply-chain";
import { safeDisplayText, safeErrorMessage } from "@/lib/safe-display";

const SUCCESS_STATES = new Set([
  "healthy",
  "passed",
  "completed",
  "published",
  "promoted",
  "stable",
  "current",
  "verified",
  "ready",
  "approved",
  "signed",
  "delivered",
  "resolved",
]);
const FAILURE_STATES = new Set([
  "failed",
  "rejected",
  "revoked",
  "blocked",
  "unhealthy",
  "error",
  "rolled_back",
  "dead_letter",
]);
const WARNING_STATES = new Set([
  "paused",
  "pending",
  "degraded",
  "stale",
  "awaiting_approval",
  "excepted",
  "deprecated",
  "retry",
]);
const ACTIVE_STATES = new Set([
  "queued",
  "running",
  "discovering",
  "building",
  "testing",
  "publishing",
  "preparing",
  "canary",
  "verifying",
  "rolling_out",
  "rolling_back",
  "delivering",
  "open",
]);

export function humanize(value?: string): string {
  if (!value) return "Unknown";
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
}

export const StatusBadge = memo(function StatusBadge({
  state,
  className,
}: {
  state?: string;
  className?: string;
}) {
  const normalized = (state ?? "unknown").toLowerCase();
  const tone = SUCCESS_STATES.has(normalized)
    ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-400"
    : FAILURE_STATES.has(normalized)
      ? "border-red-500/30 bg-red-500/10 text-red-800 dark:text-red-400"
      : WARNING_STATES.has(normalized)
        ? "border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-300"
        : ACTIVE_STATES.has(normalized)
          ? "border-sky-500/30 bg-sky-500/10 text-sky-800 dark:text-sky-300"
          : "border-border bg-muted/30 text-muted-foreground";
  const Icon = SUCCESS_STATES.has(normalized)
    ? CheckCircle2Icon
    : FAILURE_STATES.has(normalized)
      ? BanIcon
      : ACTIVE_STATES.has(normalized)
        ? LoaderCircleIcon
        : Clock3Icon;
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center gap-1 rounded-full border px-2 text-xs font-medium",
        tone,
        className,
      )}
      aria-label={`Status: ${humanize(state)}`}
    >
      <Icon
        className={cn("size-3", ACTIVE_STATES.has(normalized) && "animate-spin")}
        aria-hidden="true"
      />
      {humanize(state)}
    </span>
  );
});

export const RiskBadge = memo(function RiskBadge({ risk }: { risk?: Risk }) {
  const tone =
    risk === "critical"
      ? "border-red-500/40 bg-red-500/15 text-red-800 dark:text-red-300"
      : risk === "high"
        ? "border-orange-500/40 bg-orange-500/15 text-orange-800 dark:text-orange-300"
        : risk === "medium"
          ? "border-amber-500/40 bg-amber-500/15 text-amber-800 dark:text-amber-300"
          : risk === "low"
            ? "border-sky-500/40 bg-sky-500/15 text-sky-800 dark:text-sky-300"
            : "border-border bg-muted/30 text-muted-foreground";
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center rounded-full border px-2 text-xs font-medium",
        tone,
      )}
      aria-label={`Risk: ${humanize(risk)}`}
    >
      {humanize(risk)}
    </span>
  );
});

export function Timestamp({
  value,
  fallback = "Never",
}: {
  value?: string;
  fallback?: string;
}) {
  if (!value) return <span className="text-muted-foreground">{fallback}</span>;
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) {
    return <span className="font-mono text-xs">{value}</span>;
  }
  return (
    <time dateTime={value} title={`${date.toISOString()} (UTC)`}>
      {date.toLocaleString()}
    </time>
  );
}

export function RelativeAge({ value }: { value?: string }) {
  if (!value) return <span className="text-muted-foreground">Unknown</span>;
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return <span>{value}</span>;
  const seconds = Math.max(0, Math.round((Date.now() - timestamp) / 1000));
  if (seconds < 60) return <span>just now</span>;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return <span>{minutes}m ago</span>;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return <span>{hours}h ago</span>;
  return <span>{Math.round(hours / 24)}d ago</span>;
}

export function PageHeading({
  title,
  description,
  actions,
}: {
  title: string;
  description: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
          {description}
        </p>
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
      ) : null}
    </header>
  );
}

export function PanelHeading({
  title,
  description,
  actions,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2 border-b border-border/60 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h2 className="text-sm font-semibold">{title}</h2>
        {description ? (
          <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
    </div>
  );
}

export function MetricCard({
  label,
  value,
  detail,
  state,
}: {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
  state?: "good" | "warning" | "danger" | "neutral";
}) {
  const line =
    state === "good"
      ? "bg-emerald-400"
      : state === "warning"
        ? "bg-amber-400"
        : state === "danger"
          ? "bg-red-400"
          : "bg-muted-foreground";
  return (
    <section className="relative overflow-hidden rounded-lg border border-border/70 bg-card p-4">
      <span className={cn("absolute inset-y-0 left-0 w-0.5", line)} />
      <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <div className="mt-2 text-2xl font-semibold tracking-tight">{value}</div>
      {detail ? (
        <div className="mt-1 text-xs text-muted-foreground">{detail}</div>
      ) : null}
    </section>
  );
}

export function PartialFailureBanner({
  failures,
}: {
  failures?: Array<{ resource: string; message: string }>;
}) {
  if (!failures?.length) return null;
  return (
    <div
      className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3"
      role="status"
    >
      <div className="flex items-start gap-2">
        <ShieldAlertIcon
          className="mt-0.5 size-4 shrink-0 text-amber-300"
          aria-hidden="true"
        />
        <div>
          <p className="text-sm font-medium text-amber-200">
            Some supply-chain data is incomplete
          </p>
          <ul className="mt-1 space-y-1 text-xs text-amber-100/80">
            {failures.map((failure, index) => (
              <li key={`${failure.resource}-${index}`}>
                <span className="font-medium">
                  {safeDisplayText(failure.resource, 128) || "Resource"}:
                </span>{" "}
                {safeDisplayText(failure.message, 512) ||
                  "Data could not be loaded. Retry or inspect the operation audit."}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}

export function ResourceState({
  loading,
  error,
  empty,
  emptyTitle,
  emptyDescription,
  onRetry,
  variant = "table",
  children,
}: {
  loading: boolean;
  error: unknown;
  empty?: boolean;
  emptyTitle?: string;
  emptyDescription?: string;
  onRetry?: () => void;
  variant?: "table" | "cards";
  children: ReactNode;
}) {
  if (loading) {
    return (
      <div
        role="status"
        aria-live="polite"
        aria-label="Loading scanner release data"
      >
        <span className="sr-only">Loading scanner release data…</span>
        {variant === "table" ? <TableSkeleton rows={6} /> : <CardSkeleton />}
      </div>
    );
  }
  if (error) {
    const kind = errorKind(error);
    const detail =
      kind === "forbidden"
        ? "Your account can view Wolf, but it does not have the scanner supply-chain read scope."
        : kind === "unauthorized"
          ? "Your session expired. Sign in again to continue."
          : kind === "unavailable"
            ? "Scanner release management is not available on this server or has not been enabled."
            : kind === "stale"
              ? "This resource changed on the server. Reload it before trying again."
              : safeErrorMessage(
                  error,
                  "The scanner supply-chain service could not be reached. Retry or review service health.",
                );
    return (
      <div
        className="rounded-lg border border-red-500/30 bg-red-500/10 p-5"
        role="alert"
      >
        <div className="flex items-start gap-3">
          {kind === "unavailable" ? (
            <CloudOffIcon className="mt-0.5 size-5 text-red-300" aria-hidden="true" />
          ) : (
            <AlertCircleIcon
              className="mt-0.5 size-5 text-red-300"
              aria-hidden="true"
            />
          )}
          <div className="min-w-0 flex-1">
            <p className="font-medium">
              {kind === "forbidden"
                ? "Additional permission required"
                : kind === "unavailable"
                  ? "Release management unavailable"
                  : "Unable to load this data"}
            </p>
            <p className="mt-1 break-words text-sm text-muted-foreground">{detail}</p>
            {onRetry ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="mt-3"
                onClick={onRetry}
              >
                Retry
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    );
  }
  if (empty) {
    return (
      <div
        className="rounded-lg border border-dashed border-border p-10 text-center"
        role="status"
      >
        <p className="text-sm font-medium">{emptyTitle ?? "Nothing here yet"}</p>
        <p className="mx-auto mt-1 max-w-lg text-sm text-muted-foreground">
          {emptyDescription ?? "Records will appear here when they are available."}
        </p>
      </div>
    );
  }
  return <>{children}</>;
}

export function CodeValue({
  children,
  title,
}: {
  children?: ReactNode;
  title?: string;
}) {
  return (
    <span
      title={title}
      className="inline-block max-w-full truncate font-mono text-xs text-muted-foreground"
    >
      {children ?? "—"}
    </span>
  );
}

export function JsonPreview({ value }: { value: unknown }) {
  const text =
    typeof value === "string" ? value : JSON.stringify(value ?? {}, null, 2);
  return (
    <pre className="max-h-[32rem] overflow-auto rounded-md border border-border/60 bg-background/70 p-4 font-mono text-xs leading-5 text-muted-foreground">
      {text}
    </pre>
  );
}
