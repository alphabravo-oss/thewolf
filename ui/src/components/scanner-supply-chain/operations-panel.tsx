import { memo, useId } from "react";
import { useQueries } from "@tanstack/react-query";
import {
  ActivityIcon,
  AlertTriangleIcon,
  ArrowRightIcon,
  DatabaseIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  TimerResetIcon,
  WorkflowIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { CardSkeleton } from "@/components/skeleton";
import {
  MetricCard,
  PageHeading,
  PanelHeading,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import {
  scannerSupplyChainApi,
  type Overview,
  type ReleaseFactoryComponentHealth,
  type ReleaseFactoryComponentName,
  type ReleaseFactoryHealth,
} from "@/lib/scanner-supply-chain";
import { safeErrorMessage } from "@/lib/safe-display";

const numberFormatter = new Intl.NumberFormat();
const MAX_STUCK_ROWS = 20;
const FACTORY_COMPONENTS = new Set<ReleaseFactoryComponentName>([
  "alert",
  "build",
  "discovery",
  "fixed",
  "integration",
  "notification",
  "proposal",
  "quality",
  "registry",
  "rollout",
  "scheduler",
]);
const BACKLOG_STATES = [
  "pending",
  "delivering",
  "retry",
  "dead_letter",
] as const;

export type OperationsDestination =
  | "updates"
  | "candidates"
  | "releases"
  | "rollouts"
  | "policy"
  | "registries"
  | "notifications";

export const OperationsPanel = memo(function OperationsPanel() {
  const [factoryQuery, overviewQuery] = useQueries({
    queries: [
      {
        queryKey: ["scanner-release-factory", "health"],
        queryFn: scannerSupplyChainApi.releaseFactoryHealth,
        staleTime: 15_000,
        refetchInterval: 30_000,
      },
      {
        queryKey: ["scanner-supply-chain", "overview"],
        queryFn: scannerSupplyChainApi.overview,
        staleTime: 20_000,
        refetchInterval: 60_000,
      },
    ],
  });
  const refreshing = factoryQuery.isFetching || overviewQuery.isFetching;

  function refreshAll() {
    void Promise.all([factoryQuery.refetch(), overviewQuery.refetch()]);
  }

  return (
    <div className="space-y-5">
      <PageHeading
        title="Release Operations"
        description="Readiness, freshness, stuck work, release health, and rollout safety from bounded Wolf API summaries."
        actions={
          <>
            <span className="sr-only" aria-live="polite">
              {refreshing
                ? "Refreshing release operations…"
                : "Release operations are current."}
            </span>
            <Button
              type="button"
              variant="outline"
              onClick={refreshAll}
              disabled={refreshing}
              aria-label="Refresh release operations dashboard"
            >
              <RefreshCwIcon
                className={
                  refreshing ? "animate-spin motion-reduce:animate-none" : ""
                }
                aria-hidden="true"
              />
              {refreshing ? "Refreshing…" : "Refresh"}
            </Button>
          </>
        }
      />

      {factoryQuery.error || overviewQuery.error ? (
        <PartialDashboardFailure
          factoryFailed={Boolean(factoryQuery.error)}
          overviewFailed={Boolean(overviewQuery.error)}
          onRetryFactory={() => factoryQuery.refetch()}
          onRetryOverview={() => overviewQuery.refetch()}
        />
      ) : null}

      <DashboardRegion
        title="Release Factory"
        loading={factoryQuery.isPending}
        error={factoryQuery.error}
        empty={Boolean(
          factoryQuery.data && factoryQuery.data.components.length === 0,
        )}
        emptyDescription="No release-factory components were reported by this Wolf instance."
        onRetry={() => factoryQuery.refetch()}
      >
        {factoryQuery.data ? (
          <FactoryHealthDashboard health={factoryQuery.data} />
        ) : null}
      </DashboardRegion>

      <DashboardRegion
        title="Supply-Chain Health"
        loading={overviewQuery.isPending}
        error={overviewQuery.error}
        empty={Boolean(
          overviewQuery.data && !hasOperationalOverview(overviewQuery.data),
        )}
        emptyDescription="Freshness, release, registry, and rollout summaries will appear after release management reports its first state."
        onRetry={() => overviewQuery.refetch()}
      >
        {overviewQuery.data ? (
          <SupplyChainHealthDashboard overview={overviewQuery.data} />
        ) : null}
      </DashboardRegion>
    </div>
  );
});

function DashboardRegion({
  title,
  loading,
  error,
  empty,
  emptyDescription,
  onRetry,
  children,
}: {
  title: string;
  loading: boolean;
  error: Error | null;
  empty: boolean;
  emptyDescription: string;
  onRetry: () => void;
  children: React.ReactNode;
}) {
  if (loading) {
    return (
      <section
        className="space-y-3"
        role="status"
        aria-live="polite"
        aria-label={`Loading ${title.toLowerCase()}`}
      >
        <p className="text-sm text-muted-foreground">
          Loading {title.toLowerCase()}…
        </p>
        <div
          className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4"
          aria-hidden="true"
        >
          <CardSkeleton />
          <CardSkeleton />
          <CardSkeleton />
          <CardSkeleton />
        </div>
      </section>
    );
  }
  if (error) {
    return (
      <section
        className="rounded-lg border border-red-500/30 bg-red-500/10 p-5"
        role="alert"
      >
        <h2 className="font-medium">Could Not Load {title}</h2>
        <p className="mt-1 break-words text-sm text-muted-foreground">
          {safeErrorMessage(
            error,
            "Wolf did not return this operational summary.",
          )}{" "}
          Retry the bounded API request.
        </p>
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="mt-3"
          onClick={onRetry}
        >
          Retry {title}
        </Button>
      </section>
    );
  }
  if (empty) {
    return (
      <section
        className="rounded-lg border border-dashed border-border p-8 text-center"
        role="status"
        aria-live="polite"
      >
        <h2 className="text-sm font-medium">No {title} Data</h2>
        <p className="mx-auto mt-1 max-w-2xl text-sm text-muted-foreground">
          {emptyDescription}
        </p>
      </section>
    );
  }
  return <>{children}</>;
}

function PartialDashboardFailure({
  factoryFailed,
  overviewFailed,
  onRetryFactory,
  onRetryOverview,
}: {
  factoryFailed: boolean;
  overviewFailed: boolean;
  onRetryFactory: () => void;
  onRetryOverview: () => void;
}) {
  return (
    <div
      className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-4"
      role="alert"
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 gap-3">
          <AlertTriangleIcon
            className="mt-0.5 size-5 shrink-0 text-amber-700 dark:text-amber-300"
            aria-hidden="true"
          />
          <div>
            <p className="font-medium text-amber-800 dark:text-amber-100">
              Some Operations Data Is Unavailable
            </p>
            <p className="mt-1 text-sm text-amber-800 dark:text-amber-100/80">
              Available sections remain current. Retry the failed summary before
              making an operational decision.
            </p>
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2">
          {factoryFailed ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={onRetryFactory}
            >
              Retry Factory Health
            </Button>
          ) : null}
          {overviewFailed ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={onRetryOverview}
            >
              Retry Supply-Chain Health
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

const FactoryHealthDashboard = memo(function FactoryHealthDashboard({
  health,
}: {
  health: ReleaseFactoryHealth;
}) {
  const tableHeadingId = useId();
  const stuckHeadingId = useId();
  const components = health.components.filter((component) =>
    FACTORY_COMPONENTS.has(component.component),
  );
  const enabledComponents = components.filter((component) => component.enabled);
  const readyComponents = enabledComponents.filter(
    (component) => component.ready,
  );
  const stuckRows = boundedStuckWork(components);
  const stuckCount = stuckRows.reduce((sum, row) => sum + row.count, 0);
  const degradedComponents = enabledComponents.filter(
    (component) => !component.ready,
  );

  return (
    <section className="space-y-4">
      {!health.ready || degradedComponents.length > 0 ? (
        <div
          className="rounded-lg border border-red-500/35 bg-red-500/10 p-4"
          role="alert"
        >
          <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
            <div className="flex min-w-0 gap-3">
              <AlertTriangleIcon
                className="mt-0.5 size-5 shrink-0 text-red-700 dark:text-red-300"
                aria-hidden="true"
              />
              <div>
                <p className="font-medium text-red-800 dark:text-red-100">
                  Release Factory Needs Attention
                </p>
                <p className="mt-1 break-words text-sm text-red-800 dark:text-red-100/80">
                  {health.database === "unavailable"
                    ? "The release database is unavailable. Restore database connectivity before retrying durable work."
                    : `${numberFormatter.format(degradedComponents.length)} enabled component${degradedComponents.length === 1 ? "" : "s"} ${degradedComponents.length === 1 ? "is" : "are"} not ready. Review durable work and leases.`}
                </p>
              </div>
            </div>
            <Button asChild size="sm" variant="outline">
              <a href="/settings?tab=scanners">Open Scanner Troubleshooting</a>
            </Button>
          </div>
        </div>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Factory Readiness"
          value={humanize(health.status)}
          state={health.ready ? "good" : "danger"}
          detail={
            health.ready
              ? "Ready for configured durable work"
              : "Operator review required"
          }
        />
        <MetricCard
          label="Component Readiness"
          value={`${numberFormatter.format(readyComponents.length)}/${numberFormatter.format(enabledComponents.length)}`}
          state={
            degradedComponents.length > 0
              ? "danger"
              : enabledComponents.length > 0
                ? "good"
                : "neutral"
          }
          detail={`${numberFormatter.format(components.length - enabledComponents.length)} disabled by deployment mode`}
        />
        <MetricCard
          label="Stuck Work"
          value={numberFormatter.format(stuckCount)}
          state={stuckCount > 0 ? "danger" : "good"}
          detail={
            stuckCount > 0
              ? "Expired or lost lease evidence"
              : "No stuck work reported"
          }
        />
        <MetricCard
          label="Database"
          value={humanize(health.database)}
          state={
            health.database === "ok"
              ? "good"
              : health.database === "unknown"
                ? "warning"
                : "danger"
          }
          detail={`Factory uptime ${formatDuration(health.uptime_ms)}`}
        />
      </div>

      <FactoryReliabilityDashboard components={components} />

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Component Readiness"
          description="Control-plane workers and isolated fixed, quality, and integration lane state"
        />
        <div
          className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          role="region"
          tabIndex={0}
          aria-labelledby={tableHeadingId}
        >
          <table
            className="w-full min-w-[54rem] text-sm"
            aria-labelledby={tableHeadingId}
          >
            <caption id={tableHeadingId} className="sr-only">
              Release-factory component readiness
            </caption>
            <thead className="border-b border-border/60 bg-muted/20 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th scope="col" className="px-4 py-3 font-medium">
                  Component
                </th>
                <th scope="col" className="px-4 py-3 font-medium">
                  State
                </th>
                <th scope="col" className="px-4 py-3 font-medium">
                  Last Activity
                </th>
                <th scope="col" className="px-4 py-3 font-medium">
                  Last Success
                </th>
                <th scope="col" className="px-4 py-3 text-right font-medium">
                  Stuck
                </th>
                <th scope="col" className="px-4 py-3 text-right font-medium">
                  Remediation
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/50">
              {components.map((component) => {
                const stuck = componentStuckCount(component);
                const remediation = remediationFor(component.component);
                return (
                  <tr key={component.component}>
                    <th
                      scope="row"
                      className="px-4 py-3 text-left font-medium"
                      aria-label={`${humanize(component.component)}: ${
                        component.enabled ? "Enabled" : "Disabled in this mode"
                      }`}
                    >
                      <span translate="no">
                        {humanize(component.component)}
                      </span>
                      <span className="mt-0.5 block text-xs font-normal text-muted-foreground">
                        {component.enabled
                          ? "Enabled"
                          : "Disabled in this mode"}
                      </span>
                    </th>
                    <td className="px-4 py-3">
                      <StatusBadge
                        state={
                          component.enabled
                            ? component.ready
                              ? component.status
                              : "degraded"
                            : "disabled"
                        }
                      />
                    </td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">
                      <Timestamp value={component.last_activity} />
                    </td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">
                      <Timestamp value={component.last_success} />
                    </td>
                    <td className="px-4 py-3 text-right font-mono text-xs tabular-nums">
                      {numberFormatter.format(stuck)}
                    </td>
                    <td className="px-4 py-3 text-right">
                      <Button asChild size="sm" variant="ghost">
                        <a href={destinationHref(remediation.tab)}>
                          {remediation.label}
                          <ArrowRightIcon aria-hidden="true" />
                        </a>
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <section
        className="overflow-hidden rounded-lg border border-border/70 bg-card"
        aria-labelledby={stuckHeadingId}
      >
        <div id={stuckHeadingId}>
          <PanelHeading
            title="Stuck Work"
            description="Bounded expired-lease and lease-loss evidence by component"
          />
        </div>
        {stuckRows.length > 0 ? (
          <div
            className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            role="region"
            tabIndex={0}
            aria-labelledby={stuckHeadingId}
          >
            <table className="w-full min-w-[36rem] text-sm">
              <thead className="border-b border-border/60 bg-muted/20 text-left text-xs uppercase tracking-wide text-muted-foreground">
                <tr>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Component
                  </th>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Classification
                  </th>
                  <th scope="col" className="px-4 py-3 text-right font-medium">
                    Count
                  </th>
                  <th scope="col" className="px-4 py-3 text-right font-medium">
                    Action
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {stuckRows.map((row) => {
                  const remediation = remediationFor(row.component);
                  return (
                    <tr key={`${row.component}-${row.kind}`}>
                      <th
                        scope="row"
                        className="px-4 py-3 text-left font-medium"
                      >
                        {humanize(row.component)}
                      </th>
                      <td className="px-4 py-3 text-muted-foreground">
                        {humanize(row.kind)}
                      </td>
                      <td className="px-4 py-3 text-right font-mono tabular-nums text-red-300">
                        {numberFormatter.format(row.count)}
                      </td>
                      <td className="px-4 py-3 text-right">
                        <Button asChild size="sm" variant="outline">
                          <a href={destinationHref(remediation.tab)}>
                            {remediation.label}
                          </a>
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="p-6 text-center" role="status" aria-live="polite">
            <ShieldCheckIcon
              className="mx-auto size-6 text-emerald-400"
              aria-hidden="true"
            />
            <p className="mt-2 text-sm font-medium">No Stuck Work Reported</p>
            <p className="mt-1 text-sm text-muted-foreground">
              Enabled release-factory components report no expired or lost
              leases.
            </p>
          </div>
        )}
      </section>
    </section>
  );
});

function FactoryReliabilityDashboard({
  components,
}: {
  components: ReleaseFactoryComponentHealth[];
}) {
  const build = components.find((component) => component.component === "build");
  const buildTelemetryReported = Boolean(
    build?.result_counts || build?.run_counts,
  );
  const queueTelemetryReported = components.some(
    (component) => component.queue_depth !== undefined,
  );
  const completed = safeMetricCount(build?.result_counts?.completed);
  const partial = safeMetricCount(build?.result_counts?.partial);
  const failed = safeMetricCount(build?.result_counts?.failed);
  const successfulRuns = safeMetricCount(build?.run_counts?.success);
  const errorRuns = safeMetricCount(build?.run_counts?.error);
  const cancelledRuns = safeMetricCount(build?.run_counts?.cancelled);
  const queueRows = components
    .map((component) => {
      const backlog = BACKLOG_STATES.reduce(
        (sum, state) => sum + safeMetricCount(component.queue_depth?.[state]),
        0,
      );
      return {
        component: component.component,
        backlog,
        deadLetters: safeMetricCount(component.queue_depth?.dead_letter),
      };
    })
    .filter((row) => row.backlog > 0);
  const backlog = queueRows.reduce((sum, row) => sum + row.backlog, 0);
  const deadLetters = queueRows.reduce((sum, row) => sum + row.deadLetters, 0);
  const averageDuration =
    build?.average_run_duration_ms !== undefined
      ? formatDuration(safeMetricCount(build.average_run_duration_ms))
      : "Not reported";

  return (
    <section
      className="overflow-hidden rounded-lg border border-border/70 bg-card"
      aria-label="Build reliability and durable queues"
    >
      <PanelHeading
        title="Build Reliability & Durable Queues"
        description="Bounded aggregate counters since process start and current durable queue gauges; job IDs and payloads are never displayed"
      />
      <div className="grid gap-3 border-b border-border/60 p-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Completed Builds"
          value={
            buildTelemetryReported
              ? numberFormatter.format(completed)
              : "Not Reported"
          }
          state={
            !buildTelemetryReported
              ? "neutral"
              : completed > 0
                ? "good"
                : "neutral"
          }
          detail={
            buildTelemetryReported
              ? `${numberFormatter.format(successfulRuns)} successful runs · average ${averageDuration}`
              : "No bounded build counters were returned"
          }
        />
        <MetricCard
          label="Partial Builds"
          value={
            buildTelemetryReported
              ? numberFormatter.format(partial)
              : "Not Reported"
          }
          state={
            !buildTelemetryReported
              ? "neutral"
              : partial > 0
                ? "warning"
                : "good"
          }
          detail={
            buildTelemetryReported ? (
              <a
                href={destinationHref("candidates")}
                className="rounded-sm hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                Review candidate evidence
              </a>
            ) : (
              "No partial-outcome counter was returned"
            )
          }
        />
        <MetricCard
          label="Failed Builds"
          value={
            buildTelemetryReported
              ? numberFormatter.format(failed)
              : "Not Reported"
          }
          state={
            !buildTelemetryReported
              ? "neutral"
              : failed > 0 || errorRuns > 0
                ? "danger"
                : "good"
          }
          detail={
            buildTelemetryReported ? (
              <a
                href={destinationHref("candidates")}
                className="rounded-sm hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {numberFormatter.format(errorRuns)} error runs ·{" "}
                {numberFormatter.format(cancelledRuns)} cancelled
              </a>
            ) : (
              "No failure counters were returned"
            )
          }
        />
        <MetricCard
          label="Queue Backlog"
          value={
            queueTelemetryReported
              ? numberFormatter.format(backlog)
              : "Not Reported"
          }
          state={
            !queueTelemetryReported
              ? "neutral"
              : deadLetters > 0
                ? "danger"
                : backlog > 0
                  ? "warning"
                  : "good"
          }
          detail={
            queueTelemetryReported
              ? `${numberFormatter.format(deadLetters)} dead-lettered`
              : "No bounded queue gauges were returned"
          }
        />
      </div>

      <div className="p-4">
        <h3 className="text-sm font-semibold">Current Queue Remediation</h3>
        {queueRows.length > 0 ? (
          <ul className="mt-3 grid gap-2 lg:grid-cols-2">
            {queueRows.map((row) => {
              const remediation = remediationFor(row.component);
              return (
                <li
                  key={row.component}
                  className="flex flex-col gap-2 rounded-md border border-border/60 bg-muted/10 p-3 sm:flex-row sm:items-center sm:justify-between"
                >
                  <div>
                    <p className="text-sm font-medium">
                      {humanize(row.component)}
                    </p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {numberFormatter.format(row.backlog)} queued, retrying, or
                      in delivery
                      {row.deadLetters > 0
                        ? ` · ${numberFormatter.format(row.deadLetters)} dead-lettered`
                        : ""}
                    </p>
                  </div>
                  <Button asChild size="sm" variant="outline">
                    <a href={destinationHref(remediation.tab)}>
                      {remediation.label}
                    </a>
                  </Button>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="mt-2 text-sm text-muted-foreground" role="status">
            {queueTelemetryReported
              ? "No durable queue backlog is currently reported."
              : "Queue telemetry has not been reported by this instance."}
          </p>
        )}
      </div>
    </section>
  );
}

const SupplyChainHealthDashboard = memo(function SupplyChainHealthDashboard({
  overview,
}: {
  overview: Overview;
}) {
  const stable = overview.active_release ?? overview.stable_release;
  const freshness = overview.freshness;
  const registry = overview.registry_health ?? overview.registries;
  const rollout = overview.active_rollout;
  const canary = rollout?.health;
  const canaryFailures =
    (canary?.infrastructure_failures ?? 0) +
    (canary?.parser_failures ?? 0) +
    (canary?.pull_failures ?? 0) +
    (canary?.signature_failures ?? 0) +
    (canary?.manifest_failures ?? 0) +
    (canary?.crash_loops ?? 0);
  const attention = operationalAttention(overview, canaryFailures);

  return (
    <section className="space-y-4" aria-label="Supply-chain operational health">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Tool Freshness"
          value={
            freshness?.total !== undefined
              ? `${numberFormatter.format(freshness.current ?? 0)}/${numberFormatter.format(freshness.total)}`
              : humanize(freshness?.status)
          }
          state={
            (freshness?.failed ?? 0) > 0
              ? "danger"
              : (freshness?.incomplete ?? 0) > 0 ||
                  (freshness?.updates_available ?? 0) > 0
                ? "warning"
                : freshness
                  ? "good"
                  : "neutral"
          }
          detail={
            freshness ? (
              <a
                href={destinationHref("updates")}
                className="rounded-sm text-left hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {numberFormatter.format(freshness.updates_available ?? 0)}{" "}
                updates · {numberFormatter.format(freshness.incomplete ?? 0)}{" "}
                incomplete
              </a>
            ) : (
              "No discovery summary reported"
            )
          }
        />
        <MetricCard
          label="Stable Release Health"
          value={stable?.name ?? stable?.id ?? "Not Assigned"}
          state={stable?.revoked_at ? "danger" : stable ? "good" : "warning"}
          detail={
            <a
              href={destinationHref("releases")}
              className="max-w-full rounded-sm text-left hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {stable
                ? overview.stable_release_age_seconds !== undefined
                  ? `Age ${formatSeconds(overview.stable_release_age_seconds)}`
                  : `Published ${new Date(stable.published_at).toLocaleDateString()}`
                : "Review immutable releases"}
            </a>
          }
        />
        <MetricCard
          label="Registry Readiness"
          value={
            registry?.total !== undefined
              ? `${numberFormatter.format(registry.healthy ?? 0)}/${numberFormatter.format(registry.total)}`
              : "Not Reported"
          }
          state={
            (registry?.failed ?? 0) > 0
              ? "danger"
              : (registry?.degraded ?? 0) > 0
                ? "warning"
                : registry
                  ? "good"
                  : "neutral"
          }
          detail={
            <a
              href={destinationHref("registries")}
              className="rounded-sm text-left hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {numberFormatter.format(registry?.degraded ?? 0)} degraded ·{" "}
              {numberFormatter.format(registry?.failed ?? 0)} failed
            </a>
          }
        />
        <MetricCard
          label="Rollout Safety"
          value={rollout ? humanize(rollout.state) : "No Active Rollout"}
          state={
            !rollout
              ? "neutral"
              : ["failed", "rolled_back"].includes(rollout.state)
                ? "danger"
                : rollout.state === "paused" || canaryFailures > 0
                  ? "warning"
                  : "good"
          }
          detail={
            <a
              href={destinationHref("rollouts")}
              className="rounded-sm text-left hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {rollout
                ? `${numberFormatter.format(canary?.samples ?? 0)}/${numberFormatter.format(canary?.minimum_samples ?? 0)} canary samples · ${numberFormatter.format(canaryFailures)} failures`
                : "Review rollout history"}
            </a>
          }
        />
      </div>

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Operational Attention"
          description="Direct remediation paths for bounded health-summary failures"
        />
        {attention.length > 0 ? (
          <ul className="divide-y divide-border/50">
            {attention.map((item) => (
              <li
                key={item.id}
                className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
              >
                <div className="flex min-w-0 gap-3">
                  <AlertTriangleIcon
                    className={`mt-0.5 size-4 shrink-0 ${
                      item.critical ? "text-red-300" : "text-amber-300"
                    }`}
                    aria-hidden="true"
                  />
                  <div className="min-w-0">
                    <p className="text-sm font-medium">{item.title}</p>
                    <p className="mt-0.5 break-words text-xs text-muted-foreground">
                      {item.detail}
                    </p>
                  </div>
                </div>
                <Button
                  asChild
                  size="sm"
                  variant="outline"
                  className="shrink-0 self-start sm:self-auto"
                >
                  <a href={destinationHref(item.tab)}>
                    {item.action}
                    <ArrowRightIcon aria-hidden="true" />
                  </a>
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <div className="grid gap-3 p-5 sm:grid-cols-3" role="status">
            <OperationalHealthyState
              icon={<ActivityIcon aria-hidden="true" />}
              title="Freshness Signals"
              detail="No failed or incomplete discovery reported"
            />
            <OperationalHealthyState
              icon={<DatabaseIcon aria-hidden="true" />}
              title="Release Signals"
              detail="Stable release and registries report no critical condition"
            />
            <OperationalHealthyState
              icon={<WorkflowIcon aria-hidden="true" />}
              title="Rollout Signals"
              detail="No active rollout safety condition needs review"
            />
          </div>
        )}
      </section>

      <div className="flex flex-col gap-3 rounded-lg border border-border/60 bg-muted/10 px-4 py-3 text-sm sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-start gap-2 text-muted-foreground">
          <TimerResetIcon
            className="mt-0.5 size-4 shrink-0"
            aria-hidden="true"
          />
          <span>
            Summary generated{" "}
            <Timestamp
              value={overview.generated_at}
              fallback="at an unknown time"
            />
            . Factory counters are bounded aggregates; raw operation IDs,
            repository identifiers, and payloads are not included.
          </span>
        </div>
        <Button asChild variant="outline" size="sm">
          <a href="/settings?tab=scanners">Open Scanner Troubleshooting</a>
        </Button>
      </div>
    </section>
  );
});

function OperationalHealthyState({
  icon,
  title,
  detail,
}: {
  icon: React.ReactNode;
  title: string;
  detail: string;
}) {
  return (
    <div className="flex gap-3">
      <span className="mt-0.5 text-emerald-400 [&_svg]:size-4">{icon}</span>
      <div>
        <p className="text-sm font-medium">{title}</p>
        <p className="mt-0.5 text-xs text-muted-foreground">{detail}</p>
      </div>
    </div>
  );
}

function boundedStuckWork(components: ReleaseFactoryComponentHealth[]) {
  const rows: Array<{
    component: ReleaseFactoryComponentName;
    kind: string;
    count: number;
  }> = [];
  for (const component of components) {
    for (const [kind, count] of Object.entries(component.stuck_work ?? {})) {
      if (Number.isFinite(count) && count > 0) {
        rows.push({ component: component.component, kind, count });
      }
      if (rows.length >= MAX_STUCK_ROWS) return rows;
    }
  }
  return rows;
}

function componentStuckCount(component: ReleaseFactoryComponentHealth): number {
  return Object.values(component.stuck_work ?? {}).reduce(
    (sum, count) => sum + (Number.isFinite(count) && count > 0 ? count : 0),
    0,
  );
}

function safeMetricCount(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0
    ? Math.min(Math.floor(value), Number.MAX_SAFE_INTEGER)
    : 0;
}

function remediationFor(component: ReleaseFactoryComponentName): {
  tab: OperationsDestination;
  label: string;
} {
  switch (component) {
    case "scheduler":
      return { tab: "policy", label: "Review Policy" };
    case "discovery":
      return { tab: "updates", label: "Review Updates" };
    case "rollout":
      return { tab: "rollouts", label: "Review Rollouts" };
    case "registry":
      return { tab: "registries", label: "Review Registries" };
    case "alert":
    case "notification":
      return { tab: "notifications", label: "Review Notifications" };
    case "integration":
      return { tab: "rollouts", label: "Review Rollouts" };
    case "fixed":
    case "quality":
    case "proposal":
    case "build":
      return { tab: "candidates", label: "Review Candidates" };
  }
}

function destinationHref(tab: OperationsDestination): string {
  if (tab === "notifications") {
    return "/scanners?tab=notifications&notification_view=alerts";
  }
  return `/scanners?tab=${encodeURIComponent(tab)}`;
}

function operationalAttention(overview: Overview, canaryFailures: number) {
  const items: Array<{
    id: string;
    title: string;
    detail: string;
    action: string;
    tab: OperationsDestination;
    critical: boolean;
  }> = [];
  const alertCounts = Array.isArray(overview.alerts)
    ? undefined
    : overview.alerts;
  const openAlerts =
    (alertCounts?.open_critical ?? 0) + (alertCounts?.open_warning ?? 0);
  if (openAlerts > 0) {
    items.push({
      id: "alerts",
      title: "Operational Alerts Need Review",
      detail: `${numberFormatter.format(alertCounts?.open_critical ?? 0)} critical and ${numberFormatter.format(alertCounts?.open_warning ?? 0)} warning conditions are open.`,
      action: "Review Alerts",
      tab: "notifications",
      critical: (alertCounts?.open_critical ?? 0) > 0,
    });
  }
  const freshness = overview.freshness;
  if ((freshness?.failed ?? 0) > 0 || (freshness?.incomplete ?? 0) > 0) {
    items.push({
      id: "freshness",
      title: "Discovery Coverage Needs Review",
      detail: `${numberFormatter.format(freshness?.failed ?? 0)} failed and ${numberFormatter.format(freshness?.incomplete ?? 0)} incomplete freshness checks are reported.`,
      action: "Review Updates",
      tab: "updates",
      critical: (freshness?.failed ?? 0) > 0,
    });
  }
  const stable = overview.active_release ?? overview.stable_release;
  if (!stable || stable.revoked_at) {
    items.push({
      id: "release",
      title: stable?.revoked_at
        ? "Stable Release Is Revoked"
        : "No Stable Release Is Assigned",
      detail: stable?.revoked_at
        ? "Stop new assignments and review a verified rollback-eligible release."
        : "Review immutable release evidence before enabling rollout control.",
      action: "Review Releases",
      tab: "releases",
      critical: Boolean(stable?.revoked_at),
    });
  }
  const registry = overview.registry_health ?? overview.registries;
  if ((registry?.failed ?? 0) > 0 || (registry?.degraded ?? 0) > 0) {
    items.push({
      id: "registry",
      title: "Registry Verification Needs Review",
      detail: `${numberFormatter.format(registry?.failed ?? 0)} failed and ${numberFormatter.format(registry?.degraded ?? 0)} degraded registry targets are reported.`,
      action: "Review Registries",
      tab: "registries",
      critical: (registry?.failed ?? 0) > 0,
    });
  }
  const rollout = overview.active_rollout;
  if (
    rollout &&
    (["failed", "paused", "rolled_back"].includes(rollout.state) ||
      canaryFailures > 0)
  ) {
    items.push({
      id: "rollout",
      title: "Rollout Safety Needs Review",
      detail: `${humanize(rollout.state)} rollout with ${numberFormatter.format(canaryFailures)} reported canary failures.`,
      action: "Review Rollout",
      tab: "rollouts",
      critical: rollout.state === "failed",
    });
  }
  return items;
}

function hasOperationalOverview(overview: Overview): boolean {
  return Boolean(
    overview.freshness ||
    overview.active_release ||
    overview.stable_release ||
    overview.registry_health ||
    overview.registries ||
    overview.alerts ||
    overview.active_rollout,
  );
}

function formatDuration(milliseconds: number): string {
  return formatSeconds(Math.max(0, Math.round(milliseconds / 1_000)));
}

function formatSeconds(seconds: number): string {
  const safeSeconds = Math.max(0, Math.round(seconds));
  const days = Math.floor(safeSeconds / 86_400);
  const hours = Math.floor((safeSeconds % 86_400) / 3_600);
  const minutes = Math.floor((safeSeconds % 3_600) / 60);
  if (days > 0) {
    return `${numberFormatter.format(days)}d ${numberFormatter.format(hours)}h`;
  }
  if (hours > 0) {
    return `${numberFormatter.format(hours)}h ${numberFormatter.format(minutes)}m`;
  }
  return `${numberFormatter.format(minutes)}m`;
}
