import { memo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangleIcon,
  ArchiveRestoreIcon,
  ExternalLinkIcon,
  ListRestartIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  Trash2Icon,
  WrenchIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ActionDialog } from "./action-dialog";
import { useScannerReleaseCapabilities } from "./capabilities";
import { EventTimeline } from "./history";
import {
  CodeValue,
  PageHeading,
  PanelHeading,
  ResourceState,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import { useScannerEvents } from "./use-events";
import {
  scannerSupplyChainApi,
  type RegistryImageObservation,
  type RegistryJob,
  type RegistryJobKind,
  type RegistryJobState,
  type RegistryQuarantineObject,
  type RegistryQuarantineState,
  type RegistryReSignPolicy,
  type RegistrySummary,
} from "@/lib/scanner-supply-chain";
import { safeErrorMessage } from "@/lib/safe-display";
import { cn } from "@/lib/utils";
import { CursorNavigation } from "./cursor-navigation";

export type RegistryWorkspaceView = "targets" | "jobs" | "quarantine";

export interface RegistryWorkspaceFilters {
  registryId?: string;
  jobId?: string;
  jobKind?: RegistryJobKind | "";
  jobState?: RegistryJobState | "";
  quarantineState?: RegistryQuarantineState | "";
}

export function RegistrySectionNavigation({
  view,
  onViewChange,
}: {
  view: RegistryWorkspaceView;
  onViewChange: (view: RegistryWorkspaceView) => void;
}) {
  const items: Array<{ key: RegistryWorkspaceView; label: string }> = [
    { key: "targets", label: "Targets" },
    { key: "jobs", label: "Reconciliation jobs" },
    { key: "quarantine", label: "Quarantine inventory" },
  ];
  return (
    <nav
      aria-label="Registry operations"
      className="overflow-x-auto border-b border-border/60"
    >
      <div className="flex min-w-max gap-1">
        {items.map((item) => (
          <button
            key={item.key}
            type="button"
            aria-current={view === item.key ? "page" : undefined}
            className={cn(
              "h-10 border-b-2 px-3 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
              view === item.key
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
            onClick={() => onViewChange(item.key)}
          >
            {item.label}
          </button>
        ))}
      </div>
    </nav>
  );
}

export const RegistryJobsPanel = memo(function RegistryJobsPanel({
  filters,
  cursor,
  onCursorChange = () => undefined,
  onViewChange,
  onFiltersChange,
}: {
  filters: RegistryWorkspaceFilters;
  cursor?: string;
  onCursorChange?: (cursor?: string) => void;
  onViewChange: (view: RegistryWorkspaceView) => void;
  onFiltersChange: (filters: Partial<RegistryWorkspaceFilters>) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<
    "reconcile" | "repair" | "cleanup" | "retry" | undefined
  >();
  const [releaseId, setReleaseId] = useState("");
  const [sourceRegistryId, setSourceRegistryId] = useState("");
  const [reSignPolicy, setReSignPolicy] =
    useState<RegistryReSignPolicy>("preserve");

  const registries = useQuery({
    queryKey: ["scanner-supply-chain", "registries"],
    queryFn: scannerSupplyChainApi.registries,
    staleTime: 30_000,
  });
  const releases = useQuery({
    queryKey: ["scanner-supply-chain", "releases", "registry-jobs"],
    queryFn: () => scannerSupplyChainApi.releases({ limit: 100 }),
    staleTime: 60_000,
  });
  const jobs = useQuery({
    queryKey: [
      "scanner-supply-chain",
      "registry-jobs",
      filters.registryId,
      filters.jobKind,
      filters.jobState,
      cursor,
    ],
    queryFn: () =>
      scannerSupplyChainApi.registryJobs({
        registry_target_id: filters.registryId,
        kind: filters.jobKind,
        state: filters.jobState,
        cursor,
        limit: 100,
      }),
    refetchInterval: 15_000,
  });
  const detail = useQuery({
    queryKey: ["scanner-supply-chain", "registry-job", filters.jobId],
    queryFn: () => scannerSupplyChainApi.registryJob(filters.jobId ?? ""),
    enabled: Boolean(filters.jobId),
    refetchInterval: (query) => {
      const state = query.state.data?.job.state;
      return state && isTerminalRegistryJob(state) ? false : 5_000;
    },
  });

  const targets = registries.data?.items ?? [];
  const selectedTarget =
    targets.find((registry) => registry.id === filters.registryId) ??
    targets[0];
  const jobItems = jobs.data?.items ?? [];
  const selectedJob = detail.data?.job;

  const command = useMutation({
    mutationFn: async ({
      action,
      reason,
    }: {
      action: "reconcile" | "repair" | "cleanup" | "retry";
      reason: string;
    }) => {
      if (!permissions.manageRegistries || !capabilities.candidates) {
        throw new Error("Registry commands require candidate mode");
      }
      if (action === "retry") {
        if (!selectedJob) throw new Error("Registry job is unavailable");
        return scannerSupplyChainApi.retryRegistryJob(
          selectedJob.id,
          reason,
          detail.data?.etag ?? selectedJob.version,
        );
      }
      if (!selectedTarget) throw new Error("Choose a registry target");
      if (action === "cleanup") {
        return scannerSupplyChainApi.createRegistryCleanupJob(
          selectedTarget.id,
          reason,
        );
      }
      if (!releaseId) throw new Error("Choose a published release");
      if (action === "repair" && !sourceRegistryId) {
        throw new Error("Choose a verified source registry");
      }
      return scannerSupplyChainApi.createRegistryJob(selectedTarget.id, {
        kind: action,
        release_id: releaseId,
        source_registry_id: action === "repair" ? sourceRegistryId : undefined,
        re_sign_policy: action === "repair" ? reSignPolicy : "preserve",
        reason,
        max_attempts: 5,
      });
    },
    onSuccess: (receipt, variables) => {
      toast.success(`${humanize(variables.action)} job queued`);
      setDialog(undefined);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "registry-jobs"],
      });
      onFiltersChange({ jobId: receipt.id });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Registry command failed")),
  });

  const actionUnavailable =
    capabilitiesLoading ||
    !permissions.manageRegistries ||
    !capabilities.candidates ||
    !selectedTarget;
  const repairInvalid =
    actionUnavailable ||
    !releaseId ||
    !sourceRegistryId ||
    sourceRegistryId === selectedTarget?.id;

  return (
    <div className="space-y-5">
      <PageHeading
        title="Registry reconciliation"
        description="Queue and inspect durable digest reconciliation, drift repair, and guarded cleanup. Exact image evidence and resumable state transitions remain attached to every job."
      />
      <RegistrySectionNavigation view="jobs" onViewChange={onViewChange} />

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Queue a durable operation"
          description="Every command requires an audit reason and is idempotent. Repair and cleanup require a typed target confirmation."
        />
        <div className="grid gap-4 p-4 md:grid-cols-2 xl:grid-cols-4">
          <Field label="Destination registry" htmlFor="registry-job-target">
            <select
              id="registry-job-target"
              value={selectedTarget?.id ?? ""}
              onChange={(event) =>
                onFiltersChange({
                  registryId: event.target.value || undefined,
                  jobId: undefined,
                })
              }
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
              disabled={registries.isPending}
            >
              {targets.length === 0 ? (
                <option value="">No configured targets</option>
              ) : null}
              {targets.map((registry) => (
                <option key={registry.id} value={registry.id}>
                  {registry.name} · {humanize(registry.type)}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Published release" htmlFor="registry-job-release">
            <select
              id="registry-job-release"
              value={releaseId}
              onChange={(event) => setReleaseId(event.target.value)}
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              <option value="">Choose release</option>
              {(releases.data?.items ?? []).map((release) => (
                <option key={release.id} value={release.id}>
                  {release.name || release.id} · {humanize(release.state)}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Verified repair source" htmlFor="registry-job-source">
            <select
              id="registry-job-source"
              value={sourceRegistryId}
              onChange={(event) => setSourceRegistryId(event.target.value)}
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              <option value="">Choose source</option>
              {targets
                .filter((registry) => registry.id !== selectedTarget?.id)
                .map((registry) => (
                  <option key={registry.id} value={registry.id}>
                    {registry.name}
                  </option>
                ))}
            </select>
          </Field>
          <Field label="Repair signature policy" htmlFor="registry-job-resign">
            <select
              id="registry-job-resign"
              value={reSignPolicy}
              onChange={(event) =>
                setReSignPolicy(event.target.value as RegistryReSignPolicy)
              }
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              <option value="preserve">Preserve exact closure</option>
              <option value="required">Authorized re-sign required</option>
              <option value="forbidden">Re-sign forbidden</option>
            </select>
          </Field>
        </div>
        <div className="flex flex-wrap gap-2 border-t border-border/50 p-4">
          <Button
            type="button"
            variant="outline"
            disabled={actionUnavailable || !releaseId}
            title={
              !capabilities.candidates ? "Requires candidate mode" : undefined
            }
            onClick={() => setDialog("reconcile")}
          >
            <RefreshCwIcon aria-hidden="true" /> Reconcile exact evidence
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={repairInvalid}
            title={
              !capabilities.candidates
                ? "Requires candidate mode"
                : sourceRegistryId === selectedTarget?.id
                  ? "Source and destination must differ"
                  : undefined
            }
            onClick={() => setDialog("repair")}
          >
            <WrenchIcon aria-hidden="true" /> Repair drift
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={actionUnavailable}
            title={
              !capabilities.candidates ? "Requires candidate mode" : undefined
            }
            onClick={() => setDialog("cleanup")}
          >
            <Trash2Icon aria-hidden="true" /> Queue guarded cleanup
          </Button>
          {!capabilitiesLoading && !capabilities.candidates ? (
            <p className="self-center text-xs text-muted-foreground">
              Commands are unavailable in observe-only mode. Job and evidence
              inspection remain available.
            </p>
          ) : null}
        </div>
      </section>

      <section aria-labelledby="registry-jobs-heading">
        <div className="mb-3 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <h2 id="registry-jobs-heading" className="text-lg font-semibold">
              Durable job history
            </h2>
            <p className="text-sm text-muted-foreground">
              Showing up to 100 newest matching jobs.
            </p>
          </div>
          <div className="grid gap-2 sm:grid-cols-2">
            <Field label="Kind" htmlFor="registry-job-kind-filter">
              <select
                id="registry-job-kind-filter"
                value={filters.jobKind ?? ""}
                onChange={(event) =>
                  onFiltersChange({
                    jobKind: event.target.value as RegistryJobKind | "",
                    jobId: undefined,
                  })
                }
                className="h-9 rounded-md border border-input bg-background px-3 text-sm"
              >
                <option value="">All kinds</option>
                <option value="reconcile">Reconcile</option>
                <option value="repair">Repair</option>
                <option value="cleanup">Cleanup</option>
              </select>
            </Field>
            <Field label="State" htmlFor="registry-job-state-filter">
              <select
                id="registry-job-state-filter"
                value={filters.jobState ?? ""}
                onChange={(event) =>
                  onFiltersChange({
                    jobState: event.target.value as RegistryJobState | "",
                    jobId: undefined,
                  })
                }
                className="h-9 rounded-md border border-input bg-background px-3 text-sm"
              >
                <option value="">All states</option>
                {REGISTRY_JOB_STATES.map((state) => (
                  <option key={state} value={state}>
                    {humanize(state)}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        </div>

        <DataFreshness
          fetching={jobs.isFetching}
          updatedAt={jobs.dataUpdatedAt}
          backgroundError={Boolean(jobs.error && jobs.data)}
        />
        <ResourceState
          loading={jobs.isPending}
          error={jobs.data ? undefined : jobs.error}
          empty={jobItems.length === 0}
          emptyTitle="No registry jobs match"
          emptyDescription="Adjust the registry, kind, or state filters, or queue a durable operation above."
          onRetry={() => jobs.refetch()}
        >
          <div className="grid gap-4 xl:grid-cols-[minmax(19rem,0.8fr)_minmax(0,1.4fr)]">
            <div className="space-y-2">
              {jobItems.map((job) => (
                <JobListItem
                  key={job.id}
                  job={job}
                  registry={targets.find(
                    (target) => target.id === job.registry_target_id,
                  )}
                  selected={filters.jobId === job.id}
                  onSelect={() => onFiltersChange({ jobId: job.id })}
                />
              ))}
            </div>
            {filters.jobId ? (
              <ResourceState
                loading={detail.isPending}
                error={detail.error}
                empty={!detail.data}
                emptyTitle="Job detail unavailable"
                emptyDescription="Select another durable registry job."
                onRetry={() => detail.refetch()}
              >
                {detail.data ? (
                  <RegistryJobDetailView
                    detail={detail.data}
                    registry={targets.find(
                      (target) =>
                        target.id === detail.data?.job.registry_target_id,
                    )}
                    sourceRegistry={targets.find(
                      (target) =>
                        target.id ===
                        detail.data?.job.source_registry_target_id,
                    )}
                    retryPending={command.isPending}
                    retryAllowed={
                      permissions.manageRegistries && capabilities.candidates
                    }
                    onRetry={() => setDialog("retry")}
                  />
                ) : null}
              </ResourceState>
            ) : (
              <SelectJobPrompt />
            )}
          </div>
          <div className="mt-3 overflow-hidden rounded-lg border border-border/70 bg-card">
            <CursorNavigation
              currentCursor={cursor}
              nextCursor={jobs.data?.next_cursor}
              loading={jobs.isFetching}
              label="Registry job history"
              onCursorChange={onCursorChange}
            />
          </div>
        </ResourceState>
      </section>

      <ActionDialog
        open={dialog === "reconcile"}
        onOpenChange={(open) => setDialog(open ? "reconcile" : undefined)}
        title="Queue exact registry reconciliation?"
        description={`Read back the manifest, signature, provenance, and SBOM closure for ${selectedTarget?.name ?? "this registry"} without mutating immutable tags.`}
        confirmLabel="Queue reconciliation"
        pending={command.isPending}
        onConfirm={(reason) => command.mutate({ action: "reconcile", reason })}
      />
      <ActionDialog
        open={dialog === "repair"}
        onOpenChange={(open) => setDialog(open ? "repair" : undefined)}
        title="Repair registry drift?"
        description={`Copy the verified immutable release closure from the selected source into ${selectedTarget?.name ?? "the destination"}, then perform exact destination readback.`}
        confirmLabel="Queue repair"
        pending={command.isPending}
        destructive
        confirmationText={selectedTarget?.name}
        onConfirm={(reason) => command.mutate({ action: "repair", reason })}
      />
      <ActionDialog
        open={dialog === "cleanup"}
        onOpenChange={(open) => setDialog(open ? "cleanup" : undefined)}
        title="Queue guarded quarantine cleanup?"
        description="The worker re-authorizes every object against protection, retention, release evidence, build records, and published candidates immediately before exact-digest deletion."
        confirmLabel="Queue cleanup"
        pending={command.isPending}
        destructive
        confirmationText={selectedTarget?.name}
        onConfirm={(reason) => command.mutate({ action: "cleanup", reason })}
      />
      <ActionDialog
        open={dialog === "retry"}
        onOpenChange={(open) => setDialog(open ? "retry" : undefined)}
        title="Retry dead-lettered registry job?"
        description="Retry uses the loaded job version and a new idempotency key. Repair the reported dependency before retrying."
        confirmLabel="Retry job"
        pending={command.isPending}
        onConfirm={(reason) => command.mutate({ action: "retry", reason })}
      />
    </div>
  );
});

export const RegistryQuarantinePanel = memo(function RegistryQuarantinePanel({
  filters,
  onViewChange,
  onFiltersChange,
}: {
  filters: RegistryWorkspaceFilters;
  onViewChange: (view: RegistryWorkspaceView) => void;
  onFiltersChange: (filters: Partial<RegistryWorkspaceFilters>) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const registries = useQuery({
    queryKey: ["scanner-supply-chain", "registries"],
    queryFn: scannerSupplyChainApi.registries,
    staleTime: 30_000,
  });
  const inventory = useQuery({
    queryKey: [
      "scanner-supply-chain",
      "registry-quarantine",
      filters.registryId,
      filters.quarantineState,
    ],
    queryFn: () =>
      scannerSupplyChainApi.registryQuarantine({
        registry_target_id: filters.registryId,
        state: filters.quarantineState,
        limit: 100,
      }),
    refetchInterval: 30_000,
  });
  const targets = registries.data?.items ?? [];
  const selectedTarget =
    targets.find((target) => target.id === filters.registryId) ?? targets[0];
  const objects = inventory.data?.items ?? [];
  const cleanup = useMutation({
    mutationFn: (reason: string) => {
      if (!permissions.manageRegistries || !capabilities.candidates) {
        throw new Error("Quarantine cleanup requires candidate mode");
      }
      if (!selectedTarget) throw new Error("Choose a registry target");
      return scannerSupplyChainApi.createRegistryCleanupJob(
        selectedTarget.id,
        reason,
      );
    },
    onSuccess: (receipt) => {
      toast.success("Guarded cleanup job queued");
      setCleanupOpen(false);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "registry-jobs"],
      });
      onViewChange("jobs");
      onFiltersChange({
        jobId: receipt.id,
        jobKind: "cleanup",
        jobState: "",
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Cleanup could not be queued")),
  });

  return (
    <div className="space-y-5">
      <PageHeading
        title="Registry quarantine"
        description="Read-only inventory of partial or retained immutable objects. Cleanup is executed only as a durable, per-object re-authorized job."
        actions={
          <Button
            type="button"
            variant="destructive"
            disabled={
              capabilitiesLoading ||
              !permissions.manageRegistries ||
              !capabilities.candidates ||
              !selectedTarget
            }
            title={
              !capabilities.candidates ? "Requires candidate mode" : undefined
            }
            onClick={() => setCleanupOpen(true)}
          >
            <Trash2Icon aria-hidden="true" /> Queue guarded cleanup
          </Button>
        }
      />
      <RegistrySectionNavigation
        view="quarantine"
        onViewChange={onViewChange}
      />
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Registry target" htmlFor="quarantine-registry-filter">
          <select
            id="quarantine-registry-filter"
            value={selectedTarget?.id ?? ""}
            onChange={(event) =>
              onFiltersChange({
                registryId: event.target.value || undefined,
              })
            }
            className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
          >
            {targets.length === 0 ? (
              <option value="">No configured targets</option>
            ) : null}
            {targets.map((registry) => (
              <option key={registry.id} value={registry.id}>
                {registry.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Inventory state" htmlFor="quarantine-state-filter">
          <select
            id="quarantine-state-filter"
            value={filters.quarantineState ?? ""}
            onChange={(event) =>
              onFiltersChange({
                quarantineState: event.target.value as
                  | RegistryQuarantineState
                  | "",
              })
            }
            className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
          >
            <option value="">All states</option>
            {REGISTRY_QUARANTINE_STATES.map((state) => (
              <option key={state} value={state}>
                {humanize(state)}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <div
        role="note"
        className="rounded-lg border border-sky-500/30 bg-sky-500/10 p-3 text-sm"
      >
        Eligibility shown here is provisional. The cleanup worker always
        re-checks release images, artifacts, manifests, tools, approval
        evidence, candidate locks, build outputs, and published candidates in
        the deletion transaction.
      </div>
      <DataFreshness
        fetching={inventory.isFetching}
        updatedAt={inventory.dataUpdatedAt}
        backgroundError={Boolean(inventory.error && inventory.data)}
      />
      <ResourceState
        loading={inventory.isPending}
        error={inventory.data ? undefined : inventory.error}
        empty={objects.length === 0}
        emptyTitle="No quarantine objects match"
        emptyDescription="This target has no objects in the selected state."
        onRetry={() => inventory.refetch()}
      >
        <div className="space-y-3">
          {objects.map((object) => (
            <QuarantineObjectCard
              key={object.id}
              object={object}
              registry={targets.find(
                (target) => target.id === object.registry_target_id,
              )}
              onInspectJobs={() => {
                onViewChange("jobs");
                onFiltersChange({
                  registryId: object.registry_target_id,
                  jobKind: "cleanup",
                  jobState:
                    object.state === "delete_failed" ? "dead_letter" : "",
                  jobId: undefined,
                });
              }}
            />
          ))}
        </div>
      </ResourceState>
      <ActionDialog
        open={cleanupOpen}
        onOpenChange={setCleanupOpen}
        title="Queue guarded quarantine cleanup?"
        description="This creates a durable cleanup job. Each object must independently pass protection, retention, state, and database-reference checks immediately before exact-digest deletion."
        confirmLabel="Queue cleanup"
        pending={cleanup.isPending}
        destructive
        confirmationText={selectedTarget?.name}
        onConfirm={(reason) => cleanup.mutate(reason)}
      />
    </div>
  );
});

function JobListItem({
  job,
  registry,
  selected,
  onSelect,
}: {
  job: RegistryJob;
  registry?: RegistrySummary;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      className={cn(
        "w-full rounded-lg border p-4 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        selected
          ? "border-primary/50 bg-primary/5"
          : "border-border/70 bg-card hover:bg-muted/15",
      )}
      aria-label={`Open ${humanize(job.kind)} job ${job.id}`}
      onClick={onSelect}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="font-medium">{humanize(job.kind)}</p>
          <p className="mt-1 truncate text-xs text-muted-foreground">
            {registry?.name ?? job.registry_target_id}
          </p>
        </div>
        <StatusBadge state={job.state} />
      </div>
      <div className="mt-3 grid gap-1 text-xs text-muted-foreground">
        <span>
          Attempt {job.attempt} of {job.max_attempts}
        </span>
        <Timestamp value={job.updated_at} />
        <CodeValue>{job.id}</CodeValue>
      </div>
    </button>
  );
}

function RegistryJobDetailView({
  detail,
  registry,
  sourceRegistry,
  retryPending,
  retryAllowed,
  onRetry,
}: {
  detail: Awaited<ReturnType<typeof scannerSupplyChainApi.registryJob>>;
  registry?: RegistrySummary;
  sourceRegistry?: RegistrySummary;
  retryPending: boolean;
  retryAllowed: boolean;
  onRetry: () => void;
}) {
  const job = detail.job;
  const terminal = isTerminalRegistryJob(job.state);
  const stream = useScannerEvents("registry_job", job.id, terminal, {
    replayTerminal: true,
  });
  const correlation = stream.events.find(
    (event) => event.trace_id || event.operation_id,
  );
  const remediation = registryJobRemediation(job.error_class);
  const leaseIsStale =
    !terminal &&
    Boolean(job.lease_expires_at) &&
    new Date(job.lease_expires_at ?? 0).getTime() <= Date.now();

  return (
    <article className="min-w-0 space-y-4">
      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title={`${humanize(job.kind)} job`}
          description={job.id}
          actions={
            job.state === "dead_letter" ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!retryAllowed || retryPending}
                title={!retryAllowed ? "Requires candidate mode" : undefined}
                onClick={onRetry}
              >
                <RotateCcwIcon aria-hidden="true" /> Retry job
              </Button>
            ) : null
          }
        />
        <dl className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
          <DetailValue label="State">
            <StatusBadge state={job.state} />
          </DetailValue>
          <DetailValue label="Destination">
            {registry?.name ?? job.registry_target_id}
          </DetailValue>
          <DetailValue label="Release">
            {job.release_id ? <CodeValue>{job.release_id}</CodeValue> : "All"}
          </DetailValue>
          <DetailValue label="Source">
            {sourceRegistry?.name ??
              job.source_registry_target_id ??
              "Not required"}
          </DetailValue>
          <DetailValue label="Signature policy">
            {humanize(job.re_sign_policy)}
          </DetailValue>
          <DetailValue label="Attempt">
            {job.attempt} of {job.max_attempts}
          </DetailValue>
          <DetailValue label="Requested by">{job.actor}</DetailValue>
          <DetailValue label="Started">
            <Timestamp value={job.started_at} fallback="Not started" />
          </DetailValue>
          <DetailValue label="Completed">
            <Timestamp value={job.completed_at} fallback="Not completed" />
          </DetailValue>
        </dl>
        {job.reason ? (
          <div className="border-t border-border/50 p-4">
            <p className="text-xs font-medium text-muted-foreground">
              Audit reason
            </p>
            <p className="mt-1 text-sm">{job.reason}</p>
          </div>
        ) : null}
      </section>

      {leaseIsStale ? (
        <div
          role="status"
          className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-4 text-sm"
        >
          <p className="font-medium">Worker lease appears stale</p>
          <p className="mt-1 text-muted-foreground">
            The loaded lease expired <Timestamp value={job.lease_expires_at} />.
            Durable recovery can reclaim or retry this job; do not queue a
            duplicate operation.
          </p>
        </div>
      ) : null}

      {job.error_class ? (
        <section
          className="rounded-lg border border-red-500/30 bg-red-500/10 p-4"
          aria-labelledby="registry-job-failure"
        >
          <div className="flex items-start gap-3">
            <ShieldAlertIcon
              className="mt-0.5 size-5 shrink-0 text-red-300"
              aria-hidden="true"
            />
            <div>
              <h3 id="registry-job-failure" className="font-medium">
                {humanize(job.error_class)}
              </h3>
              <p className="mt-1 text-sm text-muted-foreground">
                {remediation}
              </p>
              <p className="mt-2 text-xs text-muted-foreground">
                Raw worker error details and summary payloads are intentionally
                not displayed. Use correlated audit records for bounded
                investigation.
              </p>
            </div>
          </div>
        </section>
      ) : null}

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Exact per-image evidence"
          description={`${detail.images.length} image ${detail.images.length === 1 ? "record" : "records"}; manifest and OCI referrer digests are compared independently.`}
        />
        {detail.images.length === 0 ? (
          <p className="p-6 text-center text-sm text-muted-foreground">
            No image evidence has been persisted yet. Active jobs update this
            section after registry readback.
          </p>
        ) : (
          <div className="space-y-4 p-4">
            {detail.images.map((image) => (
              <ImageEvidenceCard key={image.id} image={image} />
            ))}
          </div>
        )}
      </section>

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Durable event stream"
          description="The stream reconnects with Last-Event-ID and retains the newest 500 allowlisted state transitions in this session."
          actions={
            <div className="flex flex-wrap gap-2">
              <StatusBadge
                state={
                  stream.state === "live"
                    ? "running"
                    : stream.state === "stopped"
                      ? "completed"
                      : stream.state
                }
              />
              {correlation?.operation_id ? (
                <Button asChild variant="outline" size="sm">
                  <a
                    href={`/scanners?tab=audit&operation_id=${encodeURIComponent(correlation.operation_id)}`}
                  >
                    <ExternalLinkIcon aria-hidden="true" /> Audit operation
                  </a>
                </Button>
              ) : correlation?.trace_id ? (
                <Button asChild variant="outline" size="sm">
                  <a
                    href={`/scanners?tab=audit&trace_id=${encodeURIComponent(correlation.trace_id)}`}
                  >
                    <ExternalLinkIcon aria-hidden="true" /> Audit trace
                  </a>
                </Button>
              ) : null}
            </div>
          }
        />
        {stream.error ? (
          <p
            role="status"
            className="border-b border-amber-500/20 bg-amber-500/10 px-4 py-2 text-sm"
          >
            Event stream reconnecting. Persisted job detail remains available.
          </p>
        ) : null}
        <div className="p-4">
          {stream.events.length ? (
            <EventTimeline events={stream.events} />
          ) : (
            <p className="py-5 text-center text-sm text-muted-foreground">
              {stream.state === "connecting"
                ? "Loading persisted events…"
                : "No persisted state transitions are available."}
            </p>
          )}
        </div>
      </section>
    </article>
  );
}

function ImageEvidenceCard({ image }: { image: RegistryImageObservation }) {
  return (
    <article className="overflow-hidden rounded-lg border border-border/60">
      <div className="flex flex-col gap-2 border-b border-border/50 bg-muted/10 p-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h3 className="font-medium">{image.image_key}</h3>
          <p className="mt-1 break-all text-xs text-muted-foreground">
            {image.destination_reference}
          </p>
        </div>
        <StatusBadge state={image.state} />
      </div>
      <div
        className="overflow-x-auto"
        tabIndex={0}
        role="region"
        aria-label={`Exact evidence for ${image.image_key}`}
      >
        <table className="min-w-[48rem] w-full text-left text-sm">
          <thead className="bg-muted/20 text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-3 py-2 font-medium">
                Evidence
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Expected
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Source
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Destination
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Exact match
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/50">
            <EvidenceRow
              label="Manifest"
              expected={image.expected_digest}
              source={image.source_digest}
              destination={image.destination_digest}
            />
            <EvidenceRow
              label="Signature"
              expected={image.expected_signature_digest}
              destination={image.destination_signature_digest}
            />
            <EvidenceRow
              label="Provenance"
              expected={image.expected_provenance_digest}
              destination={image.destination_provenance_digest}
            />
            <EvidenceRow
              label="SBOM"
              expected={image.expected_sbom_digest}
              destination={image.destination_sbom_digest}
            />
          </tbody>
        </table>
      </div>
      <p className="border-t border-border/50 px-3 py-2 text-xs text-muted-foreground">
        Checked <Timestamp value={image.checked_at} />
      </p>
    </article>
  );
}

function EvidenceRow({
  label,
  expected,
  source,
  destination,
}: {
  label: string;
  expected?: string;
  source?: string;
  destination?: string;
}) {
  const exact =
    Boolean(expected) &&
    expected === destination &&
    (source === undefined || source === expected);
  return (
    <tr>
      <th scope="row" className="px-3 py-2 font-medium">
        {label}
      </th>
      <DigestCell value={expected} />
      <DigestCell value={source} />
      <DigestCell value={destination} />
      <td className="px-3 py-2">
        {exact ? (
          <span className="inline-flex items-center gap-1 text-emerald-300">
            <ShieldCheckIcon className="size-4" aria-hidden="true" /> Verified
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-amber-300">
            <AlertTriangleIcon className="size-4" aria-hidden="true" />
            {expected && destination ? "Mismatch" : "Pending"}
          </span>
        )}
      </td>
    </tr>
  );
}

function DigestCell({ value }: { value?: string }) {
  return (
    <td className="max-w-52 px-3 py-2">
      {value ? (
        <CodeValue title={value}>{value}</CodeValue>
      ) : (
        <span className="text-muted-foreground">Not observed</span>
      )}
    </td>
  );
}

function QuarantineObjectCard({
  object,
  registry,
  onInspectJobs,
}: {
  object: RegistryQuarantineObject;
  registry?: RegistrySummary;
  onInspectJobs: () => void;
}) {
  const eligibility = quarantineEligibility(object);
  return (
    <article className="overflow-hidden rounded-lg border border-border/70 bg-card">
      <div className="flex flex-col gap-3 border-b border-border/50 p-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-medium">{object.repository}</h2>
            <StatusBadge state={object.state} />
            {object.protected ? (
              <span className="inline-flex items-center gap-1 rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-1 text-xs">
                <ShieldCheckIcon className="size-3" aria-hidden="true" />
                Protected
              </span>
            ) : null}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {registry?.name ?? object.registry_target_id} ·{" "}
            {humanize(object.object_kind)}
          </p>
          <div className="mt-2 max-w-full">
            <CodeValue title={object.digest}>{object.digest}</CodeValue>
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onInspectJobs}
        >
          <ListRestartIcon aria-hidden="true" /> Related cleanup jobs
        </Button>
      </div>
      <dl className="grid gap-4 p-4 md:grid-cols-2 xl:grid-cols-4">
        <DetailValue label="Retention class">
          {humanize(object.retention_class)}
        </DetailValue>
        <DetailValue label="Retain until">
          <Timestamp
            value={object.retain_until}
            fallback="No expiry recorded"
          />
        </DetailValue>
        <DetailValue label="Discovered">
          <Timestamp value={object.discovered_at} />
        </DetailValue>
        <DetailValue label="Last referenced">
          <Timestamp
            value={object.last_referenced_at}
            fallback="Never recorded"
          />
        </DetailValue>
      </dl>
      <div
        className={cn(
          "border-t p-4 text-sm",
          eligibility.potentiallyEligible
            ? "border-emerald-500/20 bg-emerald-500/5"
            : "border-amber-500/20 bg-amber-500/5",
        )}
      >
        <p className="font-medium">
          {eligibility.potentiallyEligible
            ? "Potentially cleanup eligible"
            : "Cleanup blocked by visible state"}
        </p>
        <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
          {eligibility.reasons.map((reason) => (
            <li key={reason}>{reason}</li>
          ))}
        </ul>
      </div>
    </article>
  );
}

function quarantineEligibility(object: RegistryQuarantineObject): {
  potentiallyEligible: boolean;
  reasons: string[];
} {
  const reasons: string[] = [];
  if (object.protected) reasons.push("Object protection is enabled.");
  if (!object.retain_until) {
    reasons.push("No retention expiry is recorded.");
  } else if (new Date(object.retain_until).getTime() > Date.now()) {
    reasons.push("The retention window has not expired.");
  }
  if (!["quarantined", "orphaned", "delete_failed"].includes(object.state)) {
    reasons.push(`${humanize(object.state)} objects are not deletable.`);
  }
  if (
    object.state === "deleting" &&
    object.deletion_lease_expires_at &&
    new Date(object.deletion_lease_expires_at).getTime() <= Date.now()
  ) {
    reasons.push(
      "The deletion lease is stale; worker recovery must reconcile it before another cleanup.",
    );
  }
  if (reasons.length === 0) {
    return {
      potentiallyEligible: true,
      reasons: [
        "Visible protection, retention, and state checks pass.",
        "Database-reference authorization is still required at execution time.",
      ],
    };
  }
  return { potentiallyEligible: false, reasons };
}

function registryJobRemediation(errorClass?: string): string {
  const remedies: Record<string, string> = {
    authentication:
      "Repair the destination registry credential reference and test connectivity before retrying.",
    authorization:
      "Restore the required registry or signer permission, then retry with the current job version.",
    registry_unavailable:
      "Confirm registry reachability and bounded egress policy, then retry after service recovery.",
    digest_mismatch:
      "Treat immutable digest conflict as an integrity incident. Verify the source closure before queueing repair.",
    signature_mismatch:
      "Verify signer trust, signature referrer availability, and re-sign policy before retrying.",
    provenance_mismatch:
      "Verify the recorded provenance referrer at the immutable source; do not bypass exact evidence checks.",
    sbom_mismatch:
      "Verify the recorded SBOM referrer at the immutable source; queue repair only from verified evidence.",
    retention_not_expired:
      "Wait for the recorded retention window. Cleanup never shortens retention.",
    protected_reference:
      "The object is referenced or protected and must remain retained. Do not bypass cleanup authorization.",
    operation_timeout:
      "Check registry latency and worker health; content-addressed work can be retried after the dependency recovers.",
  };
  return (
    (errorClass && remedies[errorClass]) ??
    "Inspect the correlated audit transition, repair the reported registry or worker dependency, and retry only after the underlying condition is resolved."
  );
}

function DataFreshness({
  fetching,
  updatedAt,
  backgroundError,
}: {
  fetching: boolean;
  updatedAt: number;
  backgroundError: boolean;
}) {
  if (!updatedAt && !fetching) return null;
  return (
    <p
      className={cn(
        "mb-3 text-xs text-muted-foreground",
        backgroundError && "text-amber-300",
      )}
      role={backgroundError ? "status" : undefined}
    >
      {backgroundError
        ? "Refresh failed; showing the last successful bounded response from "
        : fetching
          ? "Refreshing durable state; last successful response "
          : "Last refreshed "}
      {updatedAt ? (
        <Timestamp value={new Date(updatedAt).toISOString()} />
      ) : (
        "now"
      )}
      .
    </p>
  );
}

function SelectJobPrompt() {
  return (
    <div className="grid min-h-72 place-items-center rounded-lg border border-dashed border-border p-8 text-center">
      <div>
        <ArchiveRestoreIcon
          className="mx-auto size-8 text-muted-foreground"
          aria-hidden="true"
        />
        <p className="mt-3 text-sm font-medium">Select a durable job</p>
        <p className="mt-1 text-sm text-muted-foreground">
          Inspect exact image evidence, failure remediation, audit correlation,
          and resumable events.
        </p>
      </div>
    </div>
  );
}

function Field({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="block text-xs font-medium">
        {label}
      </label>
      {children}
    </div>
  );
}

function DetailValue({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0 text-sm">{children}</dd>
    </div>
  );
}

const REGISTRY_JOB_STATES: RegistryJobState[] = [
  "queued",
  "claimed",
  "retry",
  "completed",
  "dead_letter",
  "cancelled",
];

const REGISTRY_QUARANTINE_STATES: RegistryQuarantineState[] = [
  "quarantined",
  "orphaned",
  "retained",
  "delete_failed",
  "deleting",
  "deleted",
  "promoted",
];

function isTerminalRegistryJob(state: RegistryJobState): boolean {
  return ["completed", "dead_letter", "cancelled"].includes(state);
}
