import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/* ---------------------------------------------------------------------------
   Status → colour helpers.

   Ported from astronomer/frontend/src/lib/utils.ts so a "running" scan in Wolf
   is coloured exactly like a "running" workload in Astronomer. The Astronomer
   vocabulary is kept intact and extended with Wolf's own status unions
   (ScanStatus, FindingStatus, FixItemStatus, AgentStatus).

   Keys are normalised — lowercased with spaces/underscores/hyphens stripped —
   so `wont_fix`, `Wont Fix`, and `wontfix` all resolve to the same entry.
   --------------------------------------------------------------------------- */

type Tone = "success" | "warning" | "error" | "info" | "pending" | "neutral";

const statusTones: Record<string, Tone> = {
  // Astronomer vocabulary
  active: "success",
  healthy: "success",
  ready: "success",
  synced: "success",
  insync: "success",
  succeeded: "success",
  connected: "success",
  success: "success",
  allowed: "success",
  permitted: "success",
  enabled: "success",
  compliant: "success",

  degraded: "warning",
  outofsync: "warning",
  drifted: "warning",
  stale: "warning",
  readonly: "warning",
  migrationrequired: "warning",
  decommissioning: "warning",
  warning: "warning",

  unhealthy: "error",
  notready: "error",
  denied: "error",
  critical: "error",
  error: "error",

  // Wolf: ScanStatus / FixStatus
  running: "info",
  completed: "success",
  failed: "error",
  cancelled: "neutral",
  pending: "pending",

  // Wolf: FindingStatus
  open: "warning",
  fixed: "success",
  wontfix: "neutral",
  falsepositive: "neutral",

  // Wolf: FixItemStatus
  inprogress: "info",
  skipped: "neutral",

  // Wolf: AgentStatus
  paused: "warning",
  stopped: "neutral",

  // Wolf: severity, so a severity string can feed the same helpers
  high: "error",
  medium: "warning",
  low: "info",
  info: "neutral",
};

function toneFor(status: string): Tone {
  return statusTones[status.toLowerCase().replace(/[\s_-]/g, "")] ?? "neutral";
}

// Literal class strings, never interpolated: Tailwind scans source text for
// class names, so `bg-status-${tone}/10` would compile to nothing.
const TEXT_BY_TONE: Record<Tone, string> = {
  success: "text-status-success",
  warning: "text-status-warning",
  error: "text-status-error",
  info: "text-status-info",
  pending: "text-status-pending",
  neutral: "text-status-neutral",
};

const PILL_BY_TONE: Record<Tone, string> = {
  success: "bg-status-success/10 text-status-success",
  warning: "bg-status-warning/10 text-status-warning",
  error: "bg-status-error/10 text-status-error",
  info: "bg-status-info/10 text-status-info",
  pending: "bg-status-pending/10 text-status-pending",
  neutral: "bg-status-neutral/10 text-status-neutral",
};

const DOT_BY_TONE: Record<Tone, string> = {
  success: "bg-status-success",
  warning: "bg-status-warning",
  error: "bg-status-error",
  info: "bg-status-info",
  pending: "bg-status-pending",
  neutral: "bg-status-neutral",
};

/** Foreground-only status colour, e.g. for an icon or inline text. */
export function statusColor(status: string): string {
  return TEXT_BY_TONE[toneFor(status)];
}

/** Tinted pill background + matching text, as used by StatusBadge. */
export function statusBgColor(status: string): string {
  return PILL_BY_TONE[toneFor(status)];
}

/** Solid dot colour for the leading indicator inside a StatusBadge. */
export function statusDotColor(status: string): string {
  return DOT_BY_TONE[toneFor(status)];
}

/**
 * Threshold colouring for a 0–100 gauge: green under 70, amber to 90, red
 * above. Matches Astronomer's utilisation bars.
 */
export function gaugeColor(percentage: number): string {
  if (percentage >= 90) return "bg-status-error";
  if (percentage >= 70) return "bg-status-warning";
  return "bg-status-success";
}

export function gaugeTextColor(percentage: number): string {
  if (percentage >= 90) return "text-status-error";
  if (percentage >= 70) return "text-status-warning";
  return "text-foreground";
}

/* ---------------------------------------------------------------------------
   Formatting helpers.
   --------------------------------------------------------------------------- */

const RELATIVE_UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ["year", 31_536_000_000],
  ["month", 2_592_000_000],
  ["day", 86_400_000],
  ["hour", 3_600_000],
  ["minute", 60_000],
  ["second", 1000],
];

/**
 * "3 minutes ago" / "in 2 days". Astronomer reaches for date-fns here; Wolf has
 * no date library, so this uses Intl.RelativeTimeFormat for the same output
 * without adding a dependency. Unparseable input is returned untouched.
 */
export function formatRelativeTime(dateStr: string | number | Date): string {
  const then = dateStr instanceof Date ? dateStr : new Date(dateStr);
  const ms = then.getTime();
  if (Number.isNaN(ms)) return String(dateStr);

  const diff = ms - Date.now();
  const abs = Math.abs(diff);
  if (abs < 5000) return "just now";

  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  for (const [unit, size] of RELATIVE_UNITS) {
    if (abs >= size) return rtf.format(Math.round(diff / size), unit);
  }
  return rtf.format(Math.round(diff / 1000), "second");
}

/** Human-readable byte size, e.g. "1.5 GiB". */
export function formatBytes(bytes: number, decimals: number = 1): string {
  if (!bytes) return "0 B";
  const k = 1024;
  const sizes = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`;
}

/**
 * Percentage with trailing zeros stripped ("50%" not "50.0%"). Returns "—" for
 * absent values so callers can tell "no data" apart from a real 0%.
 */
export function formatPercentage(
  value: number | undefined | null,
  decimals: number = 1,
): string {
  if (value == null || Number.isNaN(value)) return "—";
  return `${parseFloat(value.toFixed(decimals))}%`;
}

/** Compact duration between two instants, e.g. "1m 12s". */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}
