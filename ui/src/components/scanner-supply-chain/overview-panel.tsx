import { memo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangleIcon,
  ArrowRightIcon,
  BoxesIcon,
  GitPullRequestArrowIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  WrenchIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  MetricCard,
  PageHeading,
  PanelHeading,
  PartialFailureBanner,
  ResourceState,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import { ActionDialog } from "./action-dialog";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  scannerSupplyChainApi,
  type Overview,
} from "@/lib/scanner-supply-chain";
import { safeDisplayText, safeErrorMessage } from "@/lib/safe-display";

export const OverviewPanel = memo(function OverviewPanel({
  onNavigate,
}: {
  onNavigate: (
    tab: "updates" | "candidates" | "releases" | "rollouts" | "registries",
    resourceId?: string,
  ) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const overview = useQuery({
    queryKey: ["scanner-supply-chain", "overview"],
    queryFn: scannerSupplyChainApi.overview,
    staleTime: 20_000,
    refetchInterval: (query) => {
      const data = query.state.data;
      return data?.active_rollout ? 10_000 : 60_000;
    },
  });
  const [rollbackOpen, setRollbackOpen] = useState(false);

  const checkNow = useMutation({
    mutationFn: () =>
      scannerSupplyChainApi.createDiscovery(
        { type: "all" },
        "On-demand complete discovery from scanner supply-chain overview",
      ),
    onSuccess: (receipt) => {
      toast.success(`Discovery ${receipt.id} queued`);
      queryClient.invalidateQueries({ queryKey: ["scanner-supply-chain"] });
      onNavigate("updates");
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Unable to start discovery")),
  });

  const createCandidate = useMutation({
    mutationFn: () =>
      scannerSupplyChainApi.createCandidate(
        [],
        "On-demand complete candidate from scanner supply-chain overview",
        overview.data?.latest_discovery?.id,
      ),
    onSuccess: (receipt) => {
      toast.success(`Candidate ${receipt.id} queued`);
      queryClient.invalidateQueries({ queryKey: ["scanner-supply-chain"] });
      onNavigate("candidates", receipt.id);
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Unable to create candidate")),
  });

  const rollback = useMutation({
    mutationFn: (reason: string) => {
      const rollout = overview.data?.active_rollout;
      if (!rollout) throw new Error("No active rollout is available");
      return scannerSupplyChainApi.rolloutAction(
        rollout.id,
        "rollback",
        reason,
        rollout.version,
      );
    },
    onSuccess: () => {
      toast.success("Rollback queued");
      setRollbackOpen(false);
      queryClient.invalidateQueries({ queryKey: ["scanner-supply-chain"] });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Unable to roll back")),
  });

  return (
    <div className="space-y-5">
      <PageHeading
        title="Scanner supply chain"
        description="Freshness, release integrity, registry health, and staged worker rollout from one operational view."
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={() => checkNow.mutate()}
              disabled={
                capabilitiesLoading ||
                !permissions.operate ||
                !capabilities.candidates ||
                checkNow.isPending
              }
              title={
                !permissions.operate
                  ? "Scanner operator access is required"
                  : !capabilities.candidates
                    ? "Requires candidate mode"
                    : undefined
              }
            >
              <RefreshCwIcon
                className={checkNow.isPending ? "animate-spin" : ""}
                aria-hidden="true"
              />
              Check now
            </Button>
            <Button
              type="button"
              onClick={() => createCandidate.mutate()}
              disabled={
                capabilitiesLoading ||
                !permissions.operate ||
                !capabilities.candidates ||
                createCandidate.isPending
              }
              title={
                !permissions.operate
                  ? "Scanner operator access is required"
                  : !capabilities.candidates
                    ? "Requires candidate mode"
                    : undefined
              }
            >
              <GitPullRequestArrowIcon aria-hidden="true" />
              Create candidate
            </Button>
          </>
        }
      />

      <ResourceState
        loading={overview.isPending}
        error={overview.error}
        onRetry={() => overview.refetch()}
        variant="cards"
      >
        {overview.data ? (
          <OverviewContent
            overview={overview.data}
            onNavigate={onNavigate}
            onRollback={() => setRollbackOpen(true)}
            canRollback={permissions.operate && capabilities.canary}
          />
        ) : null}
      </ResourceState>

      <ActionDialog
        open={rollbackOpen}
        onOpenChange={setRollbackOpen}
        title="Roll back the active scanner rollout?"
        description={`This stops new assignments to ${overview.data?.active_rollout?.to_release_id ?? "the active release"} and restores the configured last known-good release. Active scans keep their immutable release snapshot.`}
        confirmLabel="Queue rollback"
        destructive
        confirmationText={overview.data?.active_rollout?.to_release_id}
        pending={rollback.isPending}
        onConfirm={(reason) => rollback.mutate(reason)}
      />
    </div>
  );
});

const OverviewContent = memo(function OverviewContent({
  overview,
  onNavigate,
  onRollback,
  canRollback,
}: {
  overview: Overview;
  onNavigate: (
    tab: "updates" | "candidates" | "releases" | "rollouts" | "registries",
    resourceId?: string,
  ) => void;
  onRollback: () => void;
  canRollback: boolean;
}) {
  const stable = overview.active_release ?? overview.stable_release;
  const cohorts =
    overview.worker_health?.cohorts ??
    overview.cohorts ??
    deriveWorkerCohorts(overview.worker_health?.workers ?? []);
  const registryHealth = overview.registry_health ?? overview.registries;
  const configuredRegistries =
    registryHealth && "configured" in registryHealth
      ? registryHealth.configured
      : registryHealth?.total;
  const criticalAlerts = Array.isArray(overview.alerts)
    ? overview.alerts.filter((alert) => alert.severity === "critical")
    : [];
  const openCriticalAlertCount = Array.isArray(overview.alerts)
    ? criticalAlerts.length
    : (overview.alerts?.open_critical ?? 0);
  const drifted = cohorts.reduce(
    (sum, cohort) =>
      sum +
      (cohort.desired_release_id &&
      cohort.observed_release_id !== cohort.desired_release_id
        ? Math.max(1, cohort.total_workers - cohort.ready_workers)
        : 0),
    0,
  );

  return (
    <div className="space-y-5">
      {openCriticalAlertCount > 0 && criticalAlerts.length === 0 ? (
        <div
          className="rounded-lg border border-red-500/40 bg-red-500/10 p-4"
          role="alert"
        >
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="flex min-w-0 gap-3">
              <AlertTriangleIcon
                className="mt-0.5 size-5 shrink-0 text-red-700 dark:text-red-300"
                aria-hidden="true"
              />
              <div>
                <p className="font-medium text-red-800 dark:text-red-200">
                  {openCriticalAlertCount} open critical scanner{" "}
                  {openCriticalAlertCount === 1 ? "alert" : "alerts"}
                </p>
                <p className="mt-1 text-sm text-red-800 dark:text-red-100/80">
                  Review the bounded condition evidence and follow its
                  resource-specific remediation path.
                </p>
              </div>
            </div>
            <Button asChild size="sm" variant="outline">
              <a href="/scanners?tab=notifications&notification_view=alerts">
                Review critical alerts
                <ArrowRightIcon aria-hidden="true" />
              </a>
            </Button>
          </div>
        </div>
      ) : null}
      {criticalAlerts.map((alert) => (
        <div
          key={alert.id ?? alert.title}
          className="rounded-lg border border-red-500/40 bg-red-500/10 p-4"
          role="alert"
        >
          <div className="flex gap-3">
            <AlertTriangleIcon
              className="mt-0.5 size-5 shrink-0 text-red-700 dark:text-red-300"
              aria-hidden="true"
            />
            <div className="min-w-0 flex-1">
              <p className="font-medium text-red-800 dark:text-red-200">
                {safeDisplayText(alert.title, 256)}
              </p>
              {alert.detail ? (
                <p className="mt-1 text-sm text-red-800 dark:text-red-100/80">
                  {safeDisplayText(alert.detail, 1_024)}
                </p>
              ) : null}
              <Button asChild className="mt-3" size="sm" variant="outline">
                <a
                  href={
                    alert.id
                      ? `/scanners?tab=notifications&notification_view=alerts&alert=${encodeURIComponent(alert.id)}`
                      : "/scanners?tab=notifications&notification_view=alerts"
                  }
                >
                  Inspect critical alert
                  <ArrowRightIcon aria-hidden="true" />
                </a>
              </Button>
            </div>
          </div>
        </div>
      ))}
      <PartialFailureBanner failures={overview.partial_failures} />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Stable release"
          value={stable?.name ?? stable?.id ?? "Not assigned"}
          state={stable?.revoked_at ? "danger" : stable ? "good" : "warning"}
          detail={
            stable ? (
              <button
                type="button"
                className="hover:text-foreground hover:underline"
                onClick={() => onNavigate("releases", stable.id)}
              >
                Published <Timestamp value={stable.published_at} />
              </button>
            ) : (
              "No verified stable release"
            )
          }
        />
        <MetricCard
          label="Tool freshness"
          value={
            overview.freshness?.total !== undefined
              ? `${overview.freshness.current ?? 0}/${overview.freshness.total}`
              : humanize(overview.freshness?.status ?? "unknown")
          }
          state={
            (overview.freshness?.failed ?? 0) > 0
              ? "danger"
              : (overview.freshness?.updates_available ?? 0) > 0 ||
                  (overview.freshness?.incomplete ?? 0) > 0
                ? "warning"
                : "good"
          }
          detail={
            overview.freshness?.total !== undefined
              ? `${overview.freshness?.updates_available ?? 0} updates · ${overview.freshness?.incomplete ?? 0} incomplete`
              : overview.freshness?.last_checked_at
                ? `Last checked ${new Date(overview.freshness.last_checked_at).toLocaleString()}`
                : "No completed discovery"
          }
        />
        <MetricCard
          label="Worker release drift"
          value={overview.worker_health?.drifted ?? drifted}
          state={
            (overview.worker_health?.drifted ?? drifted) > 0
              ? "warning"
              : "good"
          }
          detail={`${overview.worker_health?.ready ?? cohorts.reduce((sum, cohort) => sum + cohort.ready_workers, 0)} of ${overview.worker_health?.total ?? cohorts.reduce((sum, cohort) => sum + cohort.total_workers, 0)} workers ready`}
        />
        <MetricCard
          label="Registry health"
          value={
            registryHealth?.total !== undefined
              ? `${registryHealth.healthy ?? 0}/${registryHealth.total}`
              : (configuredRegistries ?? 0)
          }
          state={
            (registryHealth?.failed ?? 0) > 0
              ? "danger"
              : (registryHealth?.degraded ?? 0) > 0
                ? "warning"
                : "good"
          }
          detail={
            <button
              type="button"
              className="hover:text-foreground hover:underline"
              onClick={() => onNavigate("registries")}
            >
              {registryHealth?.total !== undefined
                ? `${registryHealth?.degraded ?? 0} degraded · ${registryHealth?.failed ?? 0} failed`
                : "configured targets"}
            </button>
          }
        />
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
          <PanelHeading
            title="Worker cohorts"
            description="Desired and observed immutable release assignments"
            actions={
              overview.active_rollout ? (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    onNavigate("rollouts", overview.active_rollout?.id)
                  }
                >
                  View rollout <ArrowRightIcon aria-hidden="true" />
                </Button>
              ) : null
            }
          />
          {cohorts.length ? (
            <div className="divide-y divide-border/50">
              {cohorts.map((cohort) => {
                const percentage =
                  cohort.total_workers > 0
                    ? Math.round(
                        (cohort.ready_workers / cohort.total_workers) * 100,
                      )
                    : 0;
                return (
                  <div key={cohort.id ?? cohort.name} className="px-4 py-3">
                    <div className="flex items-center justify-between gap-3">
                      <div>
                        <p className="text-sm font-medium">{cohort.name}</p>
                        <p className="mt-0.5 font-mono text-xs text-muted-foreground">
                          {cohort.observed_release_id ?? "No observed release"}
                        </p>
                      </div>
                      <StatusBadge state={cohort.state} />
                    </div>
                    <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-primary transition-[width]"
                        style={{ width: `${percentage}%` }}
                      />
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {cohort.ready_workers}/{cohort.total_workers} ready
                      {cohort.failed_workers
                        ? ` · ${cohort.failed_workers} failed`
                        : ""}
                    </p>
                  </div>
                );
              })}
            </div>
          ) : (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No worker cohorts have reported a release assignment.
            </p>
          )}
        </section>

        <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
          <PanelHeading
            title="Automation schedule"
            description="Times are shown locally; hover for UTC"
          />
          <div className="divide-y divide-border/50">
            <ScheduleRow
              icon={<RefreshCwIcon aria-hidden="true" />}
              name="Daily discovery"
              schedule={overview.discovery_schedule}
              fallbackLast={overview.latest_discovery?.completed_at}
            />
            <ScheduleRow
              icon={<GitPullRequestArrowIcon aria-hidden="true" />}
              name="Weekly candidate"
              schedule={overview.candidate_schedule}
            />
          </div>
        </section>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <ActionCard
          icon={<GitPullRequestArrowIcon aria-hidden="true" />}
          title="Approval queue"
          detail={
            overview.pending_candidate
              ? `${overview.pending_candidate.id} is ${overview.pending_candidate.state.replaceAll("_", " ")}`
              : "No candidate currently needs attention"
          }
          actionLabel={
            overview.pending_candidate ? "Review candidate" : "View candidates"
          }
          onAction={() =>
            onNavigate("candidates", overview.pending_candidate?.id)
          }
          warning={Boolean(overview.pending_candidate)}
        />
        <ActionCard
          icon={<BoxesIcon aria-hidden="true" />}
          title="Active rollout"
          detail={
            overview.active_rollout
              ? `${overview.active_rollout.to_release_id} → ${overview.active_rollout.target}`
              : "No release is currently rolling out"
          }
          actionLabel={
            overview.active_rollout ? "View rollout" : "View rollouts"
          }
          onAction={() => onNavigate("rollouts", overview.active_rollout?.id)}
        />
        <ActionCard
          icon={<RotateCcwIcon aria-hidden="true" />}
          title="Recovery"
          detail={
            stable?.rollback_eligible
              ? "A verified last known-good release is available"
              : "No rollback-eligible release is reported"
          }
          actionLabel="Roll back"
          onAction={onRollback}
          disabled={
            !canRollback ||
            !overview.active_rollout ||
            !stable?.rollback_eligible
          }
          danger
        />
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/10 px-4 py-3 text-sm">
        <div className="flex items-center gap-2 text-muted-foreground">
          <WrenchIcon className="size-4" aria-hidden="true" />
          Need per-tool inventory, image inspection, pulls, or a custom local
          build?
        </div>
        <Button asChild variant="outline" size="sm">
          <Link to="/settings" search={{ tab: "scanners" }}>
            Open scanner troubleshooting
          </Link>
        </Button>
      </div>
    </div>
  );
});

function deriveWorkerCohorts(
  workers: NonNullable<Overview["worker_health"]>["workers"],
): NonNullable<Overview["cohorts"]> {
  const byCohort = new Map<string, NonNullable<Overview["cohorts"]>[number]>();
  for (const worker of workers ?? []) {
    const cohortName = worker.cohort || "unassigned";
    const current = byCohort.get(cohortName) ?? {
      name: cohortName,
      state: "healthy",
      total_workers: 0,
      ready_workers: 0,
      failed_workers: 0,
      desired_release_id: worker.desired_release_id,
      observed_release_id: worker.observed_release_id,
    };
    current.total_workers++;
    if (
      worker.verification_state === "verified" &&
      worker.desired_release_id === worker.observed_release_id
    ) {
      current.ready_workers++;
    }
    if (worker.verification_error) {
      current.failed_workers++;
      current.state = "failed";
    } else if (worker.desired_release_id !== worker.observed_release_id) {
      current.state = "degraded";
    }
    byCohort.set(cohortName, current);
  }
  return [...byCohort.values()];
}

function ScheduleRow({
  icon,
  name,
  schedule,
  fallbackLast,
}: {
  icon: React.ReactNode;
  name: string;
  schedule?: Overview["discovery_schedule"];
  fallbackLast?: string;
}) {
  return (
    <div className="grid grid-cols-[1.5rem_1fr] gap-3 px-4 py-3">
      <span className="mt-0.5 text-muted-foreground [&_svg]:size-4">
        {icon}
      </span>
      <div>
        <div className="flex items-center justify-between gap-3">
          <p className="text-sm font-medium">{name}</p>
          <StatusBadge
            state={schedule?.stale ? "stale" : (schedule?.state ?? "healthy")}
          />
        </div>
        <div className="mt-1 grid gap-1 text-xs text-muted-foreground sm:grid-cols-2">
          <span>
            Last:{" "}
            <Timestamp value={schedule?.last_success_at ?? fallbackLast} />
          </span>
          <span>
            Next:{" "}
            <Timestamp value={schedule?.next_run_at} fallback="Not scheduled" />
          </span>
        </div>
      </div>
    </div>
  );
}

function ActionCard({
  icon,
  title,
  detail,
  actionLabel,
  onAction,
  warning,
  danger,
  disabled,
}: {
  icon: React.ReactNode;
  title: string;
  detail: string;
  actionLabel: string;
  onAction: () => void;
  warning?: boolean;
  danger?: boolean;
  disabled?: boolean;
}) {
  return (
    <section className="rounded-lg border border-border/70 bg-card p-4">
      <div className="flex items-start gap-3">
        <span
          className={`grid size-9 shrink-0 place-items-center rounded-md [&_svg]:size-4 ${
            danger
              ? "bg-red-500/10 text-red-300"
              : warning
                ? "bg-amber-500/10 text-amber-300"
                : "bg-primary/10 text-primary"
          }`}
        >
          {icon}
        </span>
        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-medium">{title}</h3>
          <p className="mt-1 text-xs text-muted-foreground">{detail}</p>
          <Button
            type="button"
            variant={danger ? "destructive" : "ghost"}
            size="sm"
            className="mt-3"
            disabled={disabled}
            onClick={onAction}
          >
            {actionLabel} <ArrowRightIcon aria-hidden="true" />
          </Button>
        </div>
      </div>
    </section>
  );
}
