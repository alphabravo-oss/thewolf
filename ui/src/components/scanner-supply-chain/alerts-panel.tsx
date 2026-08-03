import { memo, useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangleIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  CircleAlertIcon,
  ExternalLinkIcon,
  HistoryIcon,
  ShieldCheckIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  CodeValue,
  humanize,
  MetricCard,
  PageHeading,
  PartialFailureBanner,
  ResourceState,
  StatusBadge,
  Timestamp,
} from "./primitives";
import {
  scannerSupplyChainApi,
  type ScannerAlert,
  type ScannerAlertFilters,
  type ScannerAlertKind,
  type ScannerAlertSeverity,
  type ScannerAlertState,
} from "@/lib/scanner-supply-chain";
import { cn } from "@/lib/utils";
import { safeDisplayText } from "@/lib/safe-display";

const PAGE_SIZE = 25;
const MAX_LIST_EVIDENCE = 4;
const MAX_DETAIL_EVIDENCE = 8;
const MAX_EVIDENCE_TEXT = 160;

const ALERT_KINDS: ScannerAlertKind[] = [
  "missed_discovery",
  "stale_stable_release",
  "queue_backlog",
  "lease_churn",
  "repeated_gate_failure",
  "mirror_drift",
  "rollout_failure",
  "signature_health",
];

const EVIDENCE_LABELS: Record<string, string> = {
  age_seconds: "Observed age",
  threshold_seconds: "Age threshold",
  last_completed_at: "Last completed",
  release_id: "Release",
  queue: "Queue",
  depth: "Queue depth",
  max_depth: "Depth threshold",
  oldest_age_seconds: "Oldest work age",
  max_age_seconds: "Age threshold",
  events: "Lease events",
  threshold: "Count threshold",
  window_seconds: "Evaluation window",
  expired_leases: "Expired leases",
  failures: "Gate failures",
  digest_parity_status: "Digest parity",
  rollout_id: "Rollout",
  state: "Rollout state",
  release_state: "Release state",
};

const DURATION_EVIDENCE = new Set([
  "age_seconds",
  "threshold_seconds",
  "oldest_age_seconds",
  "max_age_seconds",
  "window_seconds",
]);

export const AlertsPanel = memo(function AlertsPanel({
  alertId,
  onSelectAlert,
}: {
  alertId?: string;
  onSelectAlert: (alert?: string) => void;
}) {
  if (alertId) {
    return (
      <AlertDetail alertId={alertId} onBack={() => onSelectAlert(undefined)} />
    );
  }
  return <AlertList onSelectAlert={onSelectAlert} />;
});

function AlertList({
  onSelectAlert,
}: {
  onSelectAlert: (alert: string) => void;
}) {
  const {
    capabilities,
    loading: capabilitiesLoading,
    error: capabilitiesError,
  } = useScannerReleaseCapabilities();
  const [state, setState] = useState<ScannerAlertState | "all">("open");
  const [kind, setKind] = useState<ScannerAlertKind | "">("");
  const [severity, setSeverity] = useState<ScannerAlertSeverity | "">("");
  const [cursorStack, setCursorStack] = useState<Array<string | undefined>>([
    undefined,
  ]);
  const cursor = cursorStack.at(-1);
  const filters = useMemo<ScannerAlertFilters>(
    () => ({
      state,
      kind: kind || undefined,
      severity: severity || undefined,
      cursor,
      limit: PAGE_SIZE,
    }),
    [cursor, kind, severity, state],
  );
  const alerts = useQuery({
    queryKey: ["scanner-supply-chain", "alerts", filters],
    queryFn: () => scannerSupplyChainApi.alerts(filters),
    enabled: !capabilitiesLoading && capabilities.read,
    placeholderData: (previous) => previous,
  });
  const items = alerts.data?.items ?? [];
  const openCritical = items.filter(
    (alert) => alert.state === "open" && alert.severity === "critical",
  ).length;
  const openWarning = items.filter(
    (alert) => alert.state === "open" && alert.severity === "warning",
  ).length;
  const resolved = items.filter((alert) => alert.state === "resolved").length;
  const resetPage = useCallback(() => setCursorStack([undefined]), []);

  if (!capabilitiesLoading && !capabilities.read) {
    return <AlertsUnavailable />;
  }

  return (
    <div className="space-y-5">
      <PageHeading
        title="Scanner alerts"
        description="Current and resolved operational conditions evaluated from scanner release-control evidence."
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Loaded alerts"
          value={items.length}
          detail={`Bounded to ${PAGE_SIZE} per page`}
        />
        <MetricCard
          label="Critical here"
          value={openCritical}
          detail="Open critical conditions on this page"
          state={openCritical ? "danger" : "good"}
        />
        <MetricCard
          label="Warnings here"
          value={openWarning}
          detail="Open warning conditions on this page"
          state={openWarning ? "warning" : "good"}
        />
        <MetricCard
          label="Resolved here"
          value={resolved}
          detail="Resolved history on this page"
          state="neutral"
        />
      </div>

      <section
        className="grid gap-2 rounded-lg border border-border/70 bg-card p-3 md:grid-cols-3"
        aria-label="Alert filters"
      >
        <FilterSelect
          label="Lifecycle status"
          value={state}
          onChange={(value) => {
            setState(value as ScannerAlertState | "all");
            resetPage();
          }}
        >
          <option value="open">Active alerts</option>
          <option value="resolved">Resolved alerts</option>
          <option value="all">Active and resolved</option>
        </FilterSelect>
        <FilterSelect
          label="Condition"
          value={kind}
          onChange={(value) => {
            setKind(value as ScannerAlertKind | "");
            resetPage();
          }}
        >
          <option value="">All conditions</option>
          {ALERT_KINDS.map((value) => (
            <option key={value} value={value}>
              {humanize(value)}
            </option>
          ))}
        </FilterSelect>
        <FilterSelect
          label="Severity"
          value={severity}
          onChange={(value) => {
            setSeverity(value as ScannerAlertSeverity | "");
            resetPage();
          }}
        >
          <option value="">All severities</option>
          <option value="critical">Critical</option>
          <option value="warning">Warning</option>
        </FilterSelect>
      </section>

      <PartialFailureBanner
        failures={
          alerts.isPlaceholderData
            ? [
                {
                  resource: "Alert filters",
                  message:
                    "Showing the prior bounded page while the requested alert page loads.",
                },
              ]
            : undefined
        }
      />

      <ResourceState
        loading={capabilitiesLoading || alerts.isPending}
        error={capabilitiesError ?? alerts.error}
        empty={items.length === 0}
        emptyTitle={
          state === "open" ? "No active scanner alerts" : "No matching alerts"
        }
        emptyDescription={
          state === "open"
            ? "Configured alert conditions currently report no actionable scanner release risk."
            : "Change the lifecycle, condition, or severity filters to inspect other bounded alert history."
        }
        onRetry={() => alerts.refetch()}
        variant="cards"
      >
        <ul className="grid gap-3 lg:grid-cols-2">
          {items.map((alert) => (
            <li key={alert.id}>
              <AlertCard
                alert={alert}
                onOpen={() => onSelectAlert(alert.id)}
              />
            </li>
          ))}
        </ul>
      </ResourceState>

      <nav
        aria-label="Alert pages"
        className="flex items-center justify-between gap-3"
      >
        <Button
          type="button"
          variant="outline"
          disabled={cursorStack.length === 1 || alerts.isFetching}
          onClick={() =>
            setCursorStack((current) =>
              current.length > 1 ? current.slice(0, -1) : current,
            )
          }
        >
          <ArrowLeftIcon aria-hidden="true" /> Previous
        </Button>
        <span className="text-sm text-muted-foreground">
          Page {cursorStack.length}
        </span>
        <Button
          type="button"
          variant="outline"
          disabled={!alerts.data?.next_cursor || alerts.isFetching}
          onClick={() => {
            const next = alerts.data?.next_cursor;
            if (next) setCursorStack((current) => [...current, next]);
          }}
        >
          Next <ArrowRightIcon aria-hidden="true" />
        </Button>
      </nav>
    </div>
  );
}

const AlertCard = memo(function AlertCard({
  alert,
  onOpen,
}: {
  alert: ScannerAlert;
  onOpen: () => void;
}) {
  const remediation = alertRemediation(alert);
  return (
    <article className="h-full rounded-lg border border-border/70 bg-card p-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <AlertSeverityBadge severity={alert.severity} />
            <StatusBadge state={alert.state} />
          </div>
          <h2 className="mt-3 text-base font-semibold">
            {humanize(alert.kind)}
          </h2>
          <p className="mt-1 break-words text-sm text-muted-foreground">
            {safeDisplayText(alert.summary, 1_024)}
          </p>
        </div>
        <Button type="button" size="sm" variant="outline" onClick={onOpen}>
          Inspect alert
        </Button>
      </div>

      <dl className="mt-4 grid gap-3 border-t border-border/50 pt-3 sm:grid-cols-2">
        <DetailItem label="Scope">
          <span className="block">{humanize(alert.scope_type)}</span>
          <CodeValue>{alert.scope_id}</CodeValue>
        </DetailItem>
        <DetailItem label="Occurrences">
          {alert.trigger_count.toLocaleString()} across generation{" "}
          {alert.generation.toLocaleString()}
        </DetailItem>
        <DetailItem label="First seen">
          <Timestamp value={alert.first_triggered_at} />
        </DetailItem>
        <DetailItem label="Last seen">
          <Timestamp value={alert.last_triggered_at} />
        </DetailItem>
      </dl>

      <EvidenceSummary evidence={alert.evidence} limit={MAX_LIST_EVIDENCE} />

      <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-border/50 pt-3">
        <span className="text-xs text-muted-foreground">
          {alert.state === "resolved" ? (
            <>
              Resolved <Timestamp value={alert.resolved_at} />
            </>
          ) : (
            "Condition remains active"
          )}
        </span>
        <Button asChild size="sm" variant="ghost">
          <a href={remediation.href}>
            {remediation.label}
            <ExternalLinkIcon aria-hidden="true" />
          </a>
        </Button>
      </div>
    </article>
  );
});

function AlertDetail({
  alertId,
  onBack,
}: {
  alertId: string;
  onBack: () => void;
}) {
  const {
    capabilities,
    loading: capabilitiesLoading,
    error: capabilitiesError,
  } = useScannerReleaseCapabilities();
  const alert = useQuery({
    queryKey: ["scanner-supply-chain", "alert", alertId],
    queryFn: () => scannerSupplyChainApi.alert(alertId),
    enabled: !capabilitiesLoading && capabilities.read,
  });

  if (!capabilitiesLoading && !capabilities.read) {
    return <AlertsUnavailable />;
  }

  const remediation = alert.data
    ? alertRemediation(alert.data)
    : undefined;
  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> All alerts
      </Button>
      <ResourceState
        loading={capabilitiesLoading || alert.isPending}
        error={capabilitiesError ?? alert.error}
        onRetry={() => alert.refetch()}
        variant="cards"
      >
        {alert.data ? (
          <>
            <PageHeading
              title={humanize(alert.data.kind)}
              description={
                <span className="flex flex-wrap items-center gap-2">
                  <AlertSeverityBadge severity={alert.data.severity} />
                  <StatusBadge state={alert.data.state} />
                  <CodeValue>{alert.data.id}</CodeValue>
                </span>
              }
              actions={
                remediation ? (
                  <Button asChild variant="outline">
                    <a href={remediation.href}>
                      {remediation.label}
                      <ExternalLinkIcon aria-hidden="true" />
                    </a>
                  </Button>
                ) : undefined
              }
            />

            <section
              className={cn(
                "rounded-lg border p-4",
                alert.data.state === "open" &&
                  alert.data.severity === "critical"
                  ? "border-red-500/40 bg-red-500/10"
                  : alert.data.state === "open"
                    ? "border-amber-500/40 bg-amber-500/10"
                    : "border-emerald-500/30 bg-emerald-500/10",
              )}
              aria-labelledby="alert-condition-summary"
            >
              <div className="flex items-start gap-3">
                {alert.data.state === "resolved" ? (
                  <ShieldCheckIcon
                    className="mt-0.5 size-5 shrink-0 text-emerald-300"
                    aria-hidden="true"
                  />
                ) : (
                  <CircleAlertIcon
                    className={cn(
                      "mt-0.5 size-5 shrink-0",
                      alert.data.severity === "critical"
                        ? "text-red-300"
                        : "text-amber-300",
                    )}
                    aria-hidden="true"
                  />
                )}
                <div className="min-w-0">
                  <h2 id="alert-condition-summary" className="font-medium">
                    {alert.data.state === "resolved"
                      ? "Condition resolved"
                      : "Condition remains active"}
                  </h2>
                  <p className="mt-1 whitespace-pre-wrap break-words text-sm">
                    {safeDisplayText(alert.data.summary, 2_048)}
                  </p>
                </div>
              </div>
            </section>

            <div className="grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(19rem,1fr)]">
              <section className="rounded-lg border border-border/70 bg-card p-4">
                <h2 className="text-sm font-semibold">Condition evidence</h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  Only bounded, allowlisted scalar evidence is displayed.
                  Unknown fields and nested values remain hidden.
                </p>
                <EvidenceSummary
                  evidence={alert.data.evidence}
                  limit={MAX_DETAIL_EVIDENCE}
                  expanded
                />
              </section>

              <section className="rounded-lg border border-border/70 bg-card p-4">
                <h2 className="text-sm font-semibold">Alert identity</h2>
                <dl className="mt-4 space-y-4">
                  <DetailItem label="Scope">
                    <span className="block">
                      {humanize(alert.data.scope_type)}
                    </span>
                    <CodeValue>{alert.data.scope_id}</CodeValue>
                  </DetailItem>
                  <DetailItem label="Policy">
                    {alert.data.policy_id ? (
                      <>
                        <CodeValue>{alert.data.policy_id}</CodeValue>
                        <span className="ml-2">
                          revision {alert.data.policy_revision ?? "—"}
                        </span>
                      </>
                    ) : (
                      "No policy reference"
                    )}
                  </DetailItem>
                  <DetailItem label="Record version">
                    {alert.data.version}
                  </DetailItem>
                </dl>
              </section>
            </div>

            <AlertLifecycle alert={alert.data} />
          </>
        ) : null}
      </ResourceState>
    </div>
  );
}

function AlertLifecycle({ alert }: { alert: ScannerAlert }) {
  const reopenCycles = Math.max(0, alert.generation - 1);
  return (
    <section className="rounded-lg border border-border/70 bg-card p-4">
      <div className="flex items-start gap-3">
        <HistoryIcon
          className="mt-0.5 size-5 shrink-0 text-sky-300"
          aria-hidden="true"
        />
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold">Observed lifecycle</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            This read API exposes aggregate lifecycle facts, not individual
            resolve/reopen transition events.
          </p>
        </div>
      </div>
      <ol className="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <LifecycleItem label="First seen">
          <Timestamp value={alert.first_triggered_at} />
        </LifecycleItem>
        <LifecycleItem label="Last seen">
          <Timestamp value={alert.last_triggered_at} />
          <span className="mt-1 block text-xs text-muted-foreground">
            {alert.trigger_count.toLocaleString()} total occurrence
            {alert.trigger_count === 1 ? "" : "s"}
          </span>
        </LifecycleItem>
        <LifecycleItem label="Reopened">
          {reopenCycles.toLocaleString()} recorded cycle
          {reopenCycles === 1 ? "" : "s"}
          <span className="mt-1 block text-xs text-muted-foreground">
            Current lifecycle generation {alert.generation}. Exact transition
            timestamps are not exposed.
          </span>
        </LifecycleItem>
        <LifecycleItem label="Resolution">
          {alert.state === "resolved" ? (
            <Timestamp value={alert.resolved_at} />
          ) : (
            "Condition remains open"
          )}
        </LifecycleItem>
      </ol>
    </section>
  );
}

function EvidenceSummary({
  evidence,
  limit,
  expanded = false,
}: {
  evidence: Record<string, unknown>;
  limit: number;
  expanded?: boolean;
}) {
  const entries = safeEvidenceEntries(evidence, limit);
  if (entries.length === 0) {
    return (
      <p className="mt-3 text-sm text-muted-foreground">
        No display-safe evidence summary is available.
      </p>
    );
  }
  return (
    <dl
      className={cn(
        "mt-3 grid gap-2",
        expanded ? "sm:grid-cols-2" : "sm:grid-cols-2",
      )}
      aria-label="Bounded alert evidence"
    >
      {entries.map(([key, value]) => (
        <div
          key={key}
          className="min-w-0 rounded-md border border-border/50 bg-background/40 px-3 py-2"
        >
          <dt className="text-xs text-muted-foreground">
            {EVIDENCE_LABELS[key]}
          </dt>
          <dd className="mt-0.5 break-words text-sm">
            {formatEvidenceValue(key, value)}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function AlertSeverityBadge({
  severity,
}: {
  severity: ScannerAlertSeverity;
}) {
  const critical = severity === "critical";
  const Icon = critical ? CircleAlertIcon : AlertTriangleIcon;
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center gap-1 rounded-full border px-2 text-xs font-medium",
        critical
          ? "border-red-500/30 bg-red-500/10 text-red-800 dark:text-red-300"
          : "border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-300",
      )}
      aria-label={`Severity: ${humanize(severity)}`}
    >
      <Icon className="size-3" aria-hidden="true" />
      {humanize(severity)}
    </span>
  );
}

function AlertsUnavailable() {
  return (
    <div
      className="rounded-lg border border-border/70 bg-card p-8 text-center"
      role="status"
    >
      <h1 className="text-xl font-semibold">Scanner alerts unavailable</h1>
      <p className="mx-auto mt-2 max-w-xl text-sm text-muted-foreground">
        This deployment mode does not expose scanner supply-chain read data.
        Enable release-management read capability before opening alert history.
      </p>
    </div>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: React.ReactNode;
}) {
  return (
    <label>
      <span className="mb-1 block text-xs font-medium">{label}</span>
      <select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
      >
        {children}
      </select>
    </label>
  );
}

function DetailItem({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </dt>
      <dd className="mt-1 break-words text-sm">{children}</dd>
    </div>
  );
}

function LifecycleItem({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <li className="rounded-md border border-border/50 bg-background/40 p-3">
      <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <div className="mt-1 text-sm">{children}</div>
    </li>
  );
}

function safeEvidenceEntries(
  evidence: Record<string, unknown>,
  limit: number,
): Array<[string, string | number | boolean]> {
  const entries: Array<[string, string | number | boolean]> = [];
  for (const key of Object.keys(EVIDENCE_LABELS)) {
    const value = evidence[key];
    if (
      typeof value !== "string" &&
      typeof value !== "number" &&
      typeof value !== "boolean"
    ) {
      continue;
    }
    entries.push([
      key,
      typeof value === "string"
        ? value.slice(0, MAX_EVIDENCE_TEXT)
        : value,
    ]);
    if (entries.length >= limit) break;
  }
  return entries;
}

function formatEvidenceValue(
  key: string,
  value: string | number | boolean,
): string {
  if (
    DURATION_EVIDENCE.has(key) &&
    typeof value === "number" &&
    Number.isFinite(value)
  ) {
    return formatSeconds(value);
  }
  if (key === "last_completed_at" && typeof value === "string") {
    const date = new Date(value);
    return Number.isNaN(date.valueOf()) ? value : date.toLocaleString();
  }
  if (typeof value === "boolean") return value ? "Yes" : "No";
  return String(value);
}

function formatSeconds(value: number): string {
  const seconds = Math.max(0, Math.round(value));
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m`;
  return `${seconds}s`;
}

function evidenceString(
  evidence: Record<string, unknown>,
  key: string,
): string | undefined {
  const value = evidence[key];
  return typeof value === "string" && value.length <= MAX_EVIDENCE_TEXT
    ? value
    : undefined;
}

function alertRemediation(alert: ScannerAlert): {
  href: string;
  label: string;
} {
  switch (alert.kind) {
    case "missed_discovery":
      return { href: "/scanners?tab=updates", label: "Review updates" };
    case "stale_stable_release": {
      const release = evidenceString(alert.evidence, "release_id");
      return {
        href: release
          ? `/scanners?tab=releases&release=${encodeURIComponent(release)}`
          : "/scanners?tab=releases",
        label: "Review release",
      };
    }
    case "queue_backlog": {
      const queue = evidenceString(alert.evidence, "queue");
      if (queue === "notification") {
        return {
          href: "/scanners?tab=notifications&notification_view=deliveries",
          label: "Review deliveries",
        };
      }
      if (queue === "discovery") {
        return { href: "/scanners?tab=updates", label: "Review updates" };
      }
      if (queue === "rollout") {
        return { href: "/scanners?tab=rollouts", label: "Review rollouts" };
      }
      return { href: "/scanners?tab=candidates", label: "Review candidates" };
    }
    case "repeated_gate_failure":
      return { href: "/scanners?tab=candidates", label: "Review candidates" };
    case "mirror_drift":
      return {
        href: `/scanners?tab=registries&registry=${encodeURIComponent(alert.scope_id)}`,
        label: "Review registry",
      };
    case "rollout_failure": {
      const rollout = evidenceString(alert.evidence, "rollout_id");
      return {
        href: rollout
          ? `/scanners?tab=rollouts&rollout=${encodeURIComponent(rollout)}`
          : "/scanners?tab=rollouts",
        label: "Review rollout",
      };
    }
    case "signature_health":
      return {
        href: `/scanners?tab=releases&release=${encodeURIComponent(alert.scope_id)}`,
        label: "Review release",
      };
    case "lease_churn":
    default:
      return { href: "/scanners?tab=operations", label: "Review operations" };
  }
}
