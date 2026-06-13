import type { Severity } from "./types";

// Centralized severity → tailwind helpers. Keeping this in one place means
// every badge / row / chart segment colors itself consistently.

export const severityRank: Record<Severity, number> = {
  critical: 5,
  high: 4,
  medium: 3,
  low: 2,
  info: 1,
};

export const severityLabel: Record<Severity, string> = {
  critical: "Critical",
  high: "High",
  medium: "Medium",
  low: "Low",
  info: "Info",
};

export const severityBadgeClass: Record<Severity, string> = {
  critical: "badge badge-critical",
  high: "badge badge-high",
  medium: "badge badge-medium",
  low: "badge badge-low",
  info: "badge badge-info",
};

export const severityRowAccent: Record<Severity, string> = {
  critical: "border-l-red-500",
  high: "border-l-orange-500",
  medium: "border-l-amber-500",
  low: "border-l-sky-500",
  info: "border-l-zinc-500",
};

export const severityChartColor: Record<Severity, string> = {
  critical: "#dc2626",
  high: "#ea580c",
  medium: "#d97706",
  low: "#0891b2",
  info: "#6b7280",
};
