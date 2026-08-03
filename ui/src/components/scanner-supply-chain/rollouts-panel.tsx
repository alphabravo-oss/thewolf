import { memo, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  CirclePauseIcon,
  CirclePlayIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ActionDialog } from "./action-dialog";
import { EventTimeline } from "./history";
import { useScannerEvents } from "./use-events";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  CodeValue,
  MetricCard,
  PageHeading,
  PanelHeading,
  ResourceState,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import {
  parseJson,
  scannerSupplyChainApi,
  type CohortSummary,
  type RealScanRolloutHealth,
  type RolloutDetail,
  type SyntheticRolloutHealth,
} from "@/lib/scanner-supply-chain";
import { CursorNavigation } from "./cursor-navigation";
import {
  safeBackendFailureMessage,
  safeDisplayText,
  safeErrorMessage,
} from "@/lib/safe-display";

type RolloutAction = "pause" | "resume" | "rollback";

export const RolloutsPanel = memo(function RolloutsPanel({
  rolloutId,
  cursor,
  state: controlledState,
  onStateChange,
  onCursorChange = () => undefined,
  onSelectRollout,
}: {
  rolloutId?: string;
  cursor?: string;
  state?: string;
  onStateChange?: (state: string) => void;
  onCursorChange?: (cursor?: string) => void;
  onSelectRollout: (id?: string) => void;
}) {
  const [localState, setLocalState] = useState("");
  const state = controlledState ?? localState;
  const changeState = onStateChange ?? setLocalState;
  if (rolloutId) {
    return (
      <RolloutDetailView
        rolloutId={rolloutId}
        onBack={() => onSelectRollout(undefined)}
      />
    );
  }
  return (
    <RolloutList
      cursor={cursor}
      state={state}
      onStateChange={changeState}
      onCursorChange={onCursorChange}
      onSelectRollout={onSelectRollout}
    />
  );
});

function RolloutList({
  cursor,
  state,
  onStateChange,
  onCursorChange,
  onSelectRollout,
}: {
  cursor?: string;
  state: string;
  onStateChange: (state: string) => void;
  onCursorChange: (cursor?: string) => void;
  onSelectRollout: (id: string) => void;
}) {
  const rollouts = useQuery({
    queryKey: ["scanner-supply-chain", "rollouts", state, cursor],
    queryFn: () =>
      scannerSupplyChainApi.rollouts({ state, cursor, limit: 100 }),
    placeholderData: (previous) => previous,
    refetchInterval: 15_000,
  });
  const items = rollouts.data?.items ?? [];
  return (
    <div className="space-y-5">
      <PageHeading
        title="Scanner rollouts"
        description="Canary and stable cohort progress, synthetic fixture verification, sampled real-scan health, automatic rollback thresholds, and release drift."
        actions={
          <select
            value={state}
            onChange={(event) => {
              onStateChange(event.target.value);
              onCursorChange(undefined);
            }}
            aria-label="Filter rollouts by state"
            className="h-10 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <option value="">All states</option>
            <option value="canary">Canary</option>
            <option value="verifying">Verifying</option>
            <option value="rolling_out">Rolling out</option>
            <option value="paused">Paused</option>
            <option value="failed">Failed</option>
            <option value="completed">Completed</option>
            <option value="rolled_back">Rolled back</option>
          </select>
        }
      />
      <ResourceState
        loading={rollouts.isPending}
        error={rollouts.error}
        empty={items.length === 0}
        emptyTitle="No rollout history"
        emptyDescription="Promoting a verified release creates a canary-first rollout record."
        onRetry={() => rollouts.refetch()}
      >
        <div className="grid gap-3">
          {items.map((rollout) => (
            <button
              key={rollout.id}
              type="button"
              onClick={() => onSelectRollout(rollout.id)}
              className="grid w-full gap-3 rounded-lg border border-border/70 bg-card p-4 text-left transition-colors hover:bg-muted/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:grid-cols-[1fr_auto_auto]"
            >
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{rollout.target}</span>
                  <StatusBadge state={rollout.state} />
                  {rollout.automatic_rollback ? (
                    <span className="text-xs text-emerald-300">
                      Automatic rollback armed
                    </span>
                  ) : null}
                </div>
                <div className="mt-2 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
                  <CodeValue>
                    {rollout.from_release_id ?? "Unassigned"}
                  </CodeValue>
                  <span>→</span>
                  <CodeValue>{rollout.to_release_id}</CodeValue>
                </div>
                {rollout.error_detail ? (
                  <p className="mt-2 text-xs text-red-300">
                    {safeBackendFailureMessage(
                      rollout.error_class,
                      "Rollout processing did not complete. Review bounded rollout evidence before taking action.",
                    )}
                  </p>
                ) : null}
              </div>
              <div className="text-xs text-muted-foreground">
                <p>{humanize(rollout.strategy ?? "canary")}</p>
                <p className="mt-1">
                  <Timestamp value={rollout.updated_at} />
                </p>
              </div>
              <CohortMiniSummary cohorts={rollout.cohorts ?? []} />
            </button>
          ))}
          <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
            <CursorNavigation
              currentCursor={cursor}
              nextCursor={rollouts.data?.next_cursor}
              loading={rollouts.isFetching}
              label="Rollout history"
              onCursorChange={onCursorChange}
            />
          </div>
        </div>
      </ResourceState>
    </div>
  );
}

function RolloutDetailView({
  rolloutId,
  onBack,
}: {
  rolloutId: string;
  onBack: () => void;
}) {
  const { capabilities, permissions } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const [action, setAction] = useState<RolloutAction>();
  const rollout = useQuery({
    queryKey: ["scanner-supply-chain", "rollout", rolloutId],
    queryFn: () => scannerSupplyChainApi.rollout(rolloutId),
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      return state && !["completed", "rolled_back", "failed"].includes(state)
        ? 5_000
        : false;
    },
  });
  const rolloutTerminal = Boolean(
    rollout.data &&
    ["completed", "rolled_back", "failed"].includes(rollout.data.state),
  );
  const eventStream = useScannerEvents("rollout", rolloutId, rolloutTerminal);
  const mutateAction = useMutation({
    mutationFn: ({
      selectedAction,
      reason,
    }: {
      selectedAction: RolloutAction;
      reason: string;
    }) => {
      if (!permissions.operate || !capabilities.canary) {
        throw new Error("Rollout controls require canary mode");
      }
      const current = rollout.data;
      if (!current) throw new Error("Rollout is unavailable");
      return scannerSupplyChainApi.rolloutAction(
        rolloutId,
        selectedAction,
        reason,
        current.version,
      );
    },
    onSuccess: (_, variables) => {
      toast.success(`${humanize(variables.selectedAction)} command accepted`);
      setAction(undefined);
      queryClient.invalidateQueries({ queryKey: ["scanner-supply-chain"] });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Rollout action failed")),
  });

  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> All rollouts
      </Button>
      <ResourceState
        loading={rollout.isPending}
        error={rollout.error}
        onRetry={() => rollout.refetch()}
        variant="cards"
      >
        {rollout.data ? (
          <RolloutContent
            rollout={rollout.data}
            onAction={setAction}
            eventStream={eventStream}
          />
        ) : null}
      </ResourceState>
      {rollout.data && action ? (
        <RolloutActionDialog
          action={action}
          rollout={rollout.data}
          pending={mutateAction.isPending}
          onClose={() => setAction(undefined)}
          onConfirm={(reason) =>
            mutateAction.mutate({ selectedAction: action, reason })
          }
        />
      ) : null}
    </div>
  );
}

const RolloutContent = memo(function RolloutContent({
  rollout,
  onAction,
  eventStream,
}: {
  rollout: RolloutDetail;
  onAction: (action: RolloutAction) => void;
  eventStream: ReturnType<typeof useScannerEvents>;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const synthetic = rollout.synthetic_health;
  const realScans = rollout.real_scan_health;
  const canPause = [
    "pending",
    "preparing",
    "canary",
    "verifying",
    "rolling_out",
  ].includes(rollout.state);
  const canResume = rollout.state === "paused";
  const canRollback = !["rolled_back", "rolling_back"].includes(rollout.state);
  const progress = rollout.cohorts.reduce(
    (total, cohort) => ({
      ready: total.ready + cohort.ready_workers,
      workers: total.workers + cohort.total_workers,
      failed: total.failed + cohort.failed_workers,
    }),
    { ready: 0, workers: 0, failed: 0 },
  );
  const timeline = useMemo(() => {
    const bySequence = new Map<
      number,
      NonNullable<RolloutDetail["events"]>[number]
    >();
    [...(rollout.events ?? []), ...eventStream.events].forEach((event) => {
      bySequence.set(event.sequence, event);
    });
    return [...bySequence.values()].sort(
      (left, right) => left.sequence - right.sequence,
    );
  }, [rollout.events, eventStream.events]);

  return (
    <div className="space-y-5">
      <PageHeading
        title={`Rollout to ${rollout.target}`}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <StatusBadge state={rollout.state} />
            <CodeValue>{rollout.from_release_id ?? "Unassigned"}</CodeValue>
            <span>→</span>
            <CodeValue>{rollout.to_release_id}</CodeValue>
          </span>
        }
        actions={
          <>
            {canPause ? (
              <Button
                type="button"
                variant="outline"
                onClick={() => onAction("pause")}
                disabled={
                  capabilitiesLoading ||
                  !permissions.operate ||
                  !capabilities.canary
                }
              >
                <CirclePauseIcon aria-hidden="true" /> Pause
              </Button>
            ) : null}
            {canResume ? (
              <Button
                type="button"
                onClick={() => onAction("resume")}
                disabled={
                  capabilitiesLoading ||
                  !permissions.operate ||
                  !capabilities.canary
                }
              >
                <CirclePlayIcon aria-hidden="true" /> Resume
              </Button>
            ) : null}
            {canRollback ? (
              <Button
                type="button"
                variant="destructive"
                onClick={() => onAction("rollback")}
                disabled={
                  capabilitiesLoading ||
                  !permissions.operate ||
                  !capabilities.canary
                }
              >
                <RotateCcwIcon aria-hidden="true" /> Roll back
              </Button>
            ) : null}
          </>
        }
      />

      {rollout.error_detail ? (
        <div className="rounded-lg border border-red-500/40 bg-red-500/10 p-4">
          <div className="flex gap-3">
            <ShieldAlertIcon
              className="mt-0.5 size-5 shrink-0 text-red-300"
              aria-hidden="true"
            />
            <div>
              <p className="font-medium text-red-200">
                Rollout condition requires review
              </p>
              <p className="mt-1 text-sm text-red-100/80">
                {safeBackendFailureMessage(
                  rollout.error_class,
                  "Rollout processing did not complete. Review bounded rollout evidence before taking action.",
                )}
              </p>
              {rollout.recommendation ? (
                <p className="mt-2 text-sm">
                  {safeDisplayText(rollout.recommendation, 1_024)}
                </p>
              ) : null}
            </div>
          </div>
        </div>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Workers ready"
          value={`${progress.ready}/${progress.workers}`}
          state={progress.failed ? "warning" : "good"}
          detail={`${progress.failed} failed`}
        />
        <MetricCard
          label="Synthetic fixtures"
          value={
            synthetic
              ? `${synthetic.fixture_passed}/${synthetic.fixture_total}`
              : "Not reported"
          }
          state={
            synthetic?.state === "failed"
              ? "danger"
              : synthetic?.state === "passed" && synthetic.current
                ? "good"
                : "warning"
          }
          detail={
            synthetic
              ? `${humanize(synthetic.state)} · ${synthetic.current ? "current corpus" : "stale corpus"}`
              : "No distinct evidence"
          }
        />
        <MetricCard
          label="Sampled real scans"
          value={
            realScans
              ? realScans.candidate_samples + realScans.stable_samples
              : "Not reported"
          }
          state={
            realScans?.state === "degraded"
              ? "danger"
              : realScans?.state === "healthy"
                ? "good"
                : "warning"
          }
          detail={
            realScans
              ? `${realScans.candidate_samples} candidate · ${realScans.stable_samples} stable`
              : "No distinct evidence"
          }
        />
        <MetricCard
          label="Maintenance window"
          value={rollout.maintenance_window?.open ? "Open" : "Closed"}
          state={rollout.maintenance_window?.open ? "good" : "warning"}
          detail={
            rollout.maintenance_window?.next_open_at ? (
              <>
                Next{" "}
                <Timestamp value={rollout.maintenance_window.next_open_at} />
              </>
            ) : (
              (rollout.maintenance_window?.name ?? "Not reported")
            )
          }
        />
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <SyntheticHealthCard health={synthetic} />
        <RealScanHealthCard health={realScans} />
      </div>

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Cohort progression"
          description="A cohort advances only after its release and scan-health checks pass."
        />
        <div className="grid gap-0 divide-y divide-border/50 lg:grid-cols-3 lg:divide-x lg:divide-y-0">
          {rollout.cohorts.map((cohort) => (
            <CohortCard key={cohort.id ?? cohort.name} cohort={cohort} />
          ))}
          {!rollout.cohorts.length ? (
            <p className="p-8 text-center text-sm text-muted-foreground lg:col-span-3">
              No cohort state has been reported.
            </p>
          ) : null}
        </div>
      </section>

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Rollout timeline"
          description={
            eventStream.error
              ? "Stream reconnecting. Persisted rollout evidence remains available."
              : `Durable stream ${eventStream.state}`
          }
        />
        <div className="max-h-[36rem] overflow-auto p-4">
          {timeline.length ? (
            <EventTimeline events={timeline} />
          ) : (
            <p className="py-8 text-center text-sm text-muted-foreground">
              No persisted rollout events are available.
            </p>
          )}
        </div>
      </section>
    </div>
  );
});

function SyntheticHealthCard({ health }: { health?: SyntheticRolloutHealth }) {
  return (
    <section
      className="overflow-hidden rounded-lg border border-border/70 bg-card"
      aria-label="Synthetic fixture verification"
    >
      <PanelHeading
        title="Synthetic fixture verification"
        description="Fixed-corpus scanner checks, reported independently from production scan samples."
        actions={
          health ? (
            <>
              <StatusBadge state={health.state} />
              <StatusBadge state={health.current ? "current" : "stale"} />
            </>
          ) : undefined
        }
      />
      {!health ? (
        <p className="p-5 text-sm text-muted-foreground">
          Distinct synthetic evidence has not been reported for this rollout.
        </p>
      ) : (
        <>
          <dl className="grid gap-3 p-4 text-sm sm:grid-cols-2">
            <HealthValue label="Corpus digest">
              <CodeValue title={health.corpus_digest}>
                {health.corpus_digest || "Not reported"}
              </CodeValue>
            </HealthValue>
            <HealthValue label="Observed">
              <Timestamp value={health.observed_at} />
            </HealthValue>
            <HealthValue label="Fixtures passed">
              {health.fixture_passed}/{health.fixture_total}
            </HealthValue>
            <HealthValue label="Fixtures failed">
              {health.fixture_failed}
            </HealthValue>
          </dl>
          {!health.current ? (
            <div
              className="border-t border-amber-500/30 bg-amber-500/10 px-4 py-3 text-xs text-amber-100"
              role="status"
            >
              The synthetic result is stale for the current approved corpus.
              Refresh fixture evidence before advancing this rollout.
            </div>
          ) : health.failure_class ? (
            <div className="border-t border-red-500/30 bg-red-500/10 px-4 py-3 text-xs">
              Bounded failure class:{" "}
              <strong>{humanize(health.failure_class)}</strong>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

function RealScanHealthCard({ health }: { health?: RealScanRolloutHealth }) {
  const durationDelta =
    health &&
    Number.isFinite(health.candidate_p95_duration_ms) &&
    Number.isFinite(health.stable_p95_duration_ms)
      ? health.candidate_p95_duration_ms - health.stable_p95_duration_ms
      : undefined;
  return (
    <section
      className="overflow-hidden rounded-lg border border-border/70 bg-card"
      aria-label="Sampled real-scan health"
    >
      <PanelHeading
        title="Sampled real-scan health"
        description="Candidate and stable production samples, separate from the fixed synthetic corpus."
        actions={health ? <StatusBadge state={health.state} /> : undefined}
      />
      {!health ? (
        <p className="p-5 text-sm text-muted-foreground">
          Distinct sampled real-scan evidence has not been reported for this
          rollout.
        </p>
      ) : (
        <dl className="grid gap-3 p-4 text-sm sm:grid-cols-2">
          <HealthValue label="Candidate samples">
            {health.candidate_samples}
          </HealthValue>
          <HealthValue label="Stable samples">
            {health.stable_samples}
          </HealthValue>
          <HealthValue label="Candidate infrastructure failures">
            {health.candidate_infrastructure_failures}
          </HealthValue>
          <HealthValue label="Stable infrastructure failures">
            {health.stable_infrastructure_failures}
          </HealthValue>
          <HealthValue label="Parser failures">
            {health.parser_failures}
          </HealthValue>
          <HealthValue label="Expected finding losses">
            {health.expected_finding_losses}
          </HealthValue>
          <HealthValue label="Candidate p95 duration">
            {formatMilliseconds(health.candidate_p95_duration_ms)}
          </HealthValue>
          <HealthValue label="Stable p95 duration">
            {formatMilliseconds(health.stable_p95_duration_ms)}
          </HealthValue>
          <HealthValue label="p95 duration delta">
            {durationDelta === undefined
              ? "Not reported"
              : `${durationDelta > 0 ? "+" : ""}${formatMilliseconds(durationDelta)}`}
          </HealthValue>
          <HealthValue label="Workers ready">
            {health.workers_ready}/{health.workers_total} ·{" "}
            {health.workers_failed} failed
          </HealthValue>
          <HealthValue label="Observed">
            <Timestamp value={health.observed_at} />
          </HealthValue>
        </dl>
      )}
    </section>
  );
}

function CohortCard({ cohort }: { cohort: CohortSummary }) {
  const health = parseJson<Record<string, unknown>>(cohort.health_summary, {});
  const percent =
    cohort.total_workers > 0
      ? Math.round((cohort.ready_workers / cohort.total_workers) * 100)
      : 0;
  return (
    <article className="p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="font-medium">{cohort.name}</h3>
        <StatusBadge state={cohort.state} />
      </div>
      <div className="mt-4 h-2 overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full bg-primary transition-[width]"
          style={{ width: `${percent}%` }}
        />
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        {cohort.ready_workers}/{cohort.total_workers} ready ·{" "}
        {cohort.failed_workers} failed
      </p>
      <CodeValue title={cohort.observed_release_id}>
        {cohort.observed_release_id ?? "No observed release"}
      </CodeValue>
      {Object.keys(health).length ? (
        <p className="mt-2 text-xs text-muted-foreground">
          {safeDisplayText(
            health.summary ?? health.outcome ?? "Health evidence reported",
            512,
          )}
        </p>
      ) : null}
    </article>
  );
}

function CohortMiniSummary({ cohorts }: { cohorts: CohortSummary[] }) {
  if (!cohorts.length) {
    return <span className="text-xs text-muted-foreground">No cohorts</span>;
  }
  const ready = cohorts.reduce((sum, cohort) => sum + cohort.ready_workers, 0);
  const total = cohorts.reduce((sum, cohort) => sum + cohort.total_workers, 0);
  return (
    <span className="text-right text-xs text-muted-foreground">
      {ready}/{total} workers
      <br />
      {cohorts.length} cohort{cohorts.length === 1 ? "" : "s"}
    </span>
  );
}

function RolloutActionDialog({
  action,
  rollout,
  pending,
  onClose,
  onConfirm,
}: {
  action: RolloutAction;
  rollout: RolloutDetail;
  pending: boolean;
  onClose: () => void;
  onConfirm: (reason: string) => void;
}) {
  const descriptions: Record<RolloutAction, string> = {
    pause: `Pause rollout ${rollout.id} at the next safe boundary. Active scans remain pinned to their assigned release.`,
    resume: `Resume rollout ${rollout.id} after rechecking policy, release verification, and the maintenance window.`,
    rollback: `Stop new assignments to ${rollout.to_release_id} and restore ${rollout.from_release_id ?? "the last known-good release"} on ${rollout.target}.`,
  };
  return (
    <ActionDialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={`${humanize(action)} rollout?`}
      description={descriptions[action]}
      confirmLabel={humanize(action)}
      pending={pending}
      destructive={action === "rollback"}
      confirmationText={
        action === "rollback" ? rollout.to_release_id : undefined
      }
      onConfirm={onConfirm}
    />
  );
}

function HealthValue({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 font-mono">{children}</dd>
    </div>
  );
}

function formatMilliseconds(value: number): string {
  return Number.isFinite(value)
    ? `${Math.round(value).toLocaleString()} ms`
    : "Not reported";
}
