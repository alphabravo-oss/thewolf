import { memo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangleIcon,
  ArrowLeftIcon,
  BanIcon,
  HammerIcon,
  ListRestartIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  TerminalSquareIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ActionDialog } from "@/components/scanner-supply-chain/action-dialog";
import {
  CodeValue,
  MetricCard,
  PageHeading,
  PanelHeading,
  StatusBadge,
  Timestamp,
  humanize,
} from "@/components/scanner-supply-chain/primitives";
import {
  customBuildRemediation,
  isTerminalCustomBuild,
  scannerCustomBuildApi,
  type CustomBuildState,
  type CustomBuildOperationReceipt,
} from "@/lib/scanner-custom-build";
import { CustomBuildCreateDialog } from "./custom-build-create-dialog";
import { useCustomBuildEvents } from "./use-custom-build-events";

export const CustomBuildsPanel = memo(function CustomBuildsPanel({
  buildId,
  state,
  traceId,
  operationId,
  onSelectBuild,
  onBuildAccepted,
  onStateChange,
}: {
  buildId?: string;
  state?: CustomBuildState | "";
  traceId?: string;
  operationId?: string;
  onSelectBuild: (buildId?: string) => void;
  onBuildAccepted?: (receipt: CustomBuildOperationReceipt) => void;
  onStateChange: (state: CustomBuildState | "") => void;
}) {
  const [createOpen, setCreateOpen] = useState(false);
  const acceptBuild = (receipt: CustomBuildOperationReceipt) => {
    if (onBuildAccepted) onBuildAccepted(receipt);
    else onSelectBuild(receipt.id);
  };

  if (buildId) {
    return (
      <>
        <CustomBuildDetail
          buildId={buildId}
          traceId={traceId}
          operationId={operationId}
          onBack={() => onSelectBuild(undefined)}
        />
        <CustomBuildCreateDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onAccepted={acceptBuild}
        />
      </>
    );
  }

  return (
    <>
      <CustomBuildList
        state={state ?? ""}
        onStateChange={onStateChange}
        onSelectBuild={onSelectBuild}
        onCreate={() => setCreateOpen(true)}
      />
      <CustomBuildCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onAccepted={acceptBuild}
      />
    </>
  );
});

function CustomBuildList({
  state,
  onStateChange,
  onSelectBuild,
  onCreate,
}: {
  state: CustomBuildState | "";
  onStateChange: (state: CustomBuildState | "") => void;
  onSelectBuild: (id: string) => void;
  onCreate: () => void;
}) {
  const builds = useQuery({
    queryKey: ["scanner-custom-builds", "list", state],
    queryFn: () => scannerCustomBuildApi.list({ state, limit: 100 }),
    refetchInterval: 15_000,
  });
  const items = builds.data?.items ?? [];
  const active = items.filter((build) => !isTerminalCustomBuild(build.state));
  const attention = items.filter(
    (build) => build.state === "failed" || build.state === "partial",
  );

  return (
    <div className="space-y-5">
      <PageHeading
        title="Custom builds"
        description="Durable, worker-owned scanner image builds. Queue one or all variants, follow resumable logs, and inspect each published or locally loaded result after navigation or reload."
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={() => builds.refetch()}
              disabled={builds.isFetching}
            >
              <RefreshCwIcon
                className={builds.isFetching ? "animate-spin" : undefined}
                aria-hidden="true"
              />
              Refresh
            </Button>
            <Button type="button" onClick={onCreate}>
              <HammerIcon aria-hidden="true" /> Queue custom build
            </Button>
          </>
        }
      />

      <div className="grid gap-3 sm:grid-cols-3">
        <MetricCard
          label="Loaded operations"
          value={items.length}
          detail="Server-bounded to 100"
        />
        <MetricCard
          label="In progress"
          value={active.length}
          detail="Queued, claimed, or running"
          state={active.length ? "warning" : "good"}
        />
        <MetricCard
          label="Needs attention"
          value={attention.length}
          detail="Partial or failed"
          state={attention.length ? "danger" : "good"}
        />
      </div>

      <section
        className="rounded-lg border border-border/70 bg-card p-3"
        aria-label="Custom build filters"
      >
        <label className="grid gap-1.5 text-sm sm:max-w-xs">
          <span className="font-medium">Build status</span>
          <select
            value={state}
            onChange={(event) =>
              onStateChange(event.target.value as CustomBuildState | "")
            }
            className="h-10 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <option value="">All statuses</option>
            <option value="queued">Queued</option>
            <option value="claimed">Claimed</option>
            <option value="running">Running</option>
            <option value="completed">Completed</option>
            <option value="partial">Partial</option>
            <option value="failed">Failed</option>
            <option value="cancelled">Cancelled</option>
          </select>
        </label>
      </section>

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Build operations"
          description="Select an operation for its durable receipt, per-variant outcome, and logs."
        />
        {builds.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground" role="status">
            Loading custom builds…
          </div>
        ) : builds.isError ? (
          <div className="space-y-3 p-5" role="alert">
            <p className="text-sm text-destructive">
              Custom builds could not be loaded. Verify scanner build read
              permission and service availability.
            </p>
            <Button type="button" variant="outline" onClick={() => builds.refetch()}>
              Try again
            </Button>
          </div>
        ) : items.length === 0 ? (
          <div className="space-y-3 p-8 text-center">
            <HammerIcon
              className="mx-auto size-8 text-muted-foreground"
              aria-hidden="true"
            />
            <div>
              <p className="text-sm font-medium">No custom builds found</p>
              <p className="mt-1 text-xs text-muted-foreground">
                {state
                  ? `No operations match ${humanize(state)}.`
                  : "Queue a durable build to create the first operation."}
              </p>
            </div>
            {state ? (
              <Button type="button" variant="outline" onClick={() => onStateChange("")}>
                Clear filter
              </Button>
            ) : (
              <Button type="button" onClick={onCreate}>
                Queue custom build
              </Button>
            )}
          </div>
        ) : (
          <ul className="divide-y divide-border/60">
            {items.map((build) => (
              <li
                key={build.id}
                style={{ contentVisibility: "auto", containIntrinsicSize: "76px" }}
              >
                <button
                  type="button"
                  onClick={() => onSelectBuild(build.id)}
                  className="grid w-full gap-2 p-4 text-left transition-colors hover:bg-muted/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center"
                >
                  <span className="min-w-0">
                    <span className="block truncate font-mono text-xs">
                      {build.id}
                    </span>
                    <span className="mt-1 block text-xs text-muted-foreground">
                      {build.variants.map(variantLabel).join(", ") || "No variants"} ·{" "}
                      {build.push ? "Registry push" : "Local load"}
                    </span>
                  </span>
                  <StatusBadge state={build.state} />
                  <span className="text-xs text-muted-foreground sm:text-right">
                    <Timestamp value={build.updated_at} />
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function CustomBuildDetail({
  buildId,
  traceId,
  operationId,
  onBack,
}: {
  buildId: string;
  traceId?: string;
  operationId?: string;
  onBack: () => void;
}) {
  const queryClient = useQueryClient();
  const [action, setAction] = useState<"cancel" | "retry">();
  const detail = useQuery({
    queryKey: ["scanner-custom-builds", "detail", buildId],
    queryFn: () => scannerCustomBuildApi.detail(buildId),
    refetchInterval: (query) =>
      isTerminalCustomBuild(query.state.data?.build.state) ? false : 5_000,
  });
  const build = detail.data?.build;
  const terminal = isTerminalCustomBuild(build?.state);
  const stream = useCustomBuildEvents(buildId, terminal, Boolean(build));

  const command = useMutation({
    mutationFn: async ({ kind, reason }: { kind: "cancel" | "retry"; reason: string }) => {
      if (!build) throw new Error("Custom build is unavailable");
      const etag = detail.data?.etag ?? build.version;
      return kind === "cancel"
        ? scannerCustomBuildApi.cancel(build.id, reason, etag)
        : scannerCustomBuildApi.retry(build.id, reason, etag);
    },
    onSuccess: (_, variables) => {
      toast.success(
        variables.kind === "cancel"
          ? "Cancellation requested"
          : "Custom build queued for retry",
      );
      setAction(undefined);
      queryClient.invalidateQueries({ queryKey: ["scanner-custom-builds"] });
    },
    onError: (error) => {
      toast.error(
        error instanceof Error ? error.message : "Custom build command failed",
      );
      detail.refetch();
    },
  });

  if (detail.isLoading) {
    return (
      <div className="space-y-4" role="status">
        <Button type="button" variant="ghost" onClick={onBack}>
          <ArrowLeftIcon aria-hidden="true" /> Back to custom builds
        </Button>
        <div className="rounded-lg border border-border/70 bg-card p-8 text-sm text-muted-foreground">
          Loading custom build…
        </div>
      </div>
    );
  }

  if (detail.isError || !build) {
    return (
      <div className="space-y-4">
        <Button type="button" variant="ghost" onClick={onBack}>
          <ArrowLeftIcon aria-hidden="true" /> Back to custom builds
        </Button>
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-5" role="alert">
          <p className="text-sm text-destructive">
            Custom build details are unavailable. The operation may not exist,
            or your account may lack scanner build read permission.
          </p>
          <Button
            type="button"
            variant="outline"
            className="mt-3"
            onClick={() => detail.refetch()}
          >
            Try again
          </Button>
        </div>
      </div>
    );
  }

  const inventory = detail.data!;
  const canCancel = ["queued", "claimed", "running"].includes(build.state);
  const canRetry = ["partial", "failed", "cancelled"].includes(build.state);
  const failedVariants = inventory.variants.filter(
    (variant) => variant.state === "failed" || variant.state === "cancelled",
  );
  const staleLease =
    !terminal &&
    build.lease_expires_at &&
    new Date(build.lease_expires_at).getTime() < Date.now();

  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> Back to custom builds
      </Button>

      <PageHeading
        title="Custom build operation"
        description="Durable status and allowlisted build evidence. Secret references, raw request payloads, and raw worker error details are intentionally not exposed."
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={() => detail.refetch()}
              disabled={detail.isFetching}
            >
              <RefreshCwIcon
                className={detail.isFetching ? "animate-spin" : undefined}
                aria-hidden="true"
              />{" "}
              Refresh
            </Button>
            {canRetry ? (
              <Button type="button" variant="outline" onClick={() => setAction("retry")}>
                <RotateCcwIcon aria-hidden="true" /> Retry
              </Button>
            ) : null}
            {canCancel ? (
              <Button type="button" variant="destructive" onClick={() => setAction("cancel")}>
                <BanIcon aria-hidden="true" /> Cancel build
              </Button>
            ) : null}
          </>
        }
      />

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Durable operation receipt"
          actions={<StatusBadge state={build.state} />}
        />
        <dl className="grid gap-4 p-4 text-sm sm:grid-cols-2 xl:grid-cols-4">
          <DetailTerm label="Build ID">
            <CodeValue>{build.id}</CodeValue>
          </DetailTerm>
          {operationId ? (
            <DetailTerm label="Audit correlation">
              <a
                href={customBuildAuditHref(operationId, traceId)}
                className="text-primary underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                Open operation audit
              </a>
            </DetailTerm>
          ) : null}
          <DetailTerm label="Status resource">
            <code className="break-all text-xs">
              /api/v1/scanners/custom-builds/{build.id}
            </code>
          </DetailTerm>
          <DetailTerm label="Attempt">
            {build.attempt} of {build.max_attempts}
          </DetailTerm>
          <DetailTerm label="Reserved image version">
            {build.reserved_version ?? "Pending"}
          </DetailTerm>
          <DetailTerm label="Destination">
            {build.push ? `Registry · ${build.namespace ?? "server default"}` : "Local Docker host"}
          </DetailTerm>
          <DetailTerm label="Platforms">
            {build.platforms.join(", ") || "Server default"}
          </DetailTerm>
          <DetailTerm label="Created">
            <Timestamp value={build.created_at} />
          </DetailTerm>
          <DetailTerm label="Updated">
            <Timestamp value={build.updated_at} />
          </DetailTerm>
          <DetailTerm label="Actor">{build.actor ?? "Unavailable"}</DetailTerm>
          <DetailTerm label="Reason">
            <span className="break-words">{build.reason || "Unavailable"}</span>
          </DetailTerm>
        </dl>
      </section>

      {staleLease ? (
        <div
          className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-sm"
          role="status"
        >
          <AlertTriangleIcon
            className="mt-0.5 size-4 shrink-0 text-amber-300"
            aria-hidden="true"
          />
          <p>
            The worker lease appears stale. Status polling and stream
            reconnection will continue; check custom-build worker health before
            retrying.
          </p>
        </div>
      ) : null}

      {(build.state === "partial" || build.state === "failed") ? (
        <div
          className="rounded-lg border border-destructive/40 bg-destructive/10 p-4"
          role="alert"
        >
          <div className="flex items-start gap-2">
            <AlertTriangleIcon
              className="mt-0.5 size-4 shrink-0 text-destructive"
              aria-hidden="true"
            />
            <div className="min-w-0">
              <p className="text-sm font-medium">
                {build.state === "partial"
                  ? "Some variants did not complete"
                  : "The custom build failed"}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                Failure class: {humanize(build.error_class)}
              </p>
              <p className="mt-1 text-xs">
                {customBuildRemediation(build.error_class)}
              </p>
            </div>
          </div>
        </div>
      ) : null}

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Variant outcomes"
          description={`${inventory.variants.length} allowlisted variant records`}
        />
        {inventory.variants.length === 0 ? (
          <p className="p-5 text-sm text-muted-foreground">
            Per-variant records have not been created yet.
          </p>
        ) : (
          <ul className="divide-y divide-border/60">
            {inventory.variants.map((variant) => (
              <li key={variant.id} className="space-y-3 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h3 className="font-medium">{variantLabel(variant.variant)}</h3>
                  <StatusBadge state={variant.state} />
                </div>
                <dl className="grid gap-3 text-sm sm:grid-cols-2 xl:grid-cols-4">
                  <DetailTerm label="Digest">
                    <CodeValue>{variant.digest ?? "Not produced"}</CodeValue>
                  </DetailTerm>
                  <DetailTerm label="Loaded locally">
                    {variant.loaded_locally ? "Yes" : "No"}
                  </DetailTerm>
                  <DetailTerm label="Pushed">
                    {variant.pushed ? "Yes" : "No"}
                  </DetailTerm>
                  <DetailTerm label="Completed">
                    <Timestamp value={variant.completed_at} fallback="Not yet" />
                  </DetailTerm>
                </dl>
                {variant.refs.length ? (
                  <div>
                    <p className="text-xs font-medium text-muted-foreground">
                      Image references
                    </p>
                    <ul className="mt-1 space-y-1">
                      {variant.refs.map((ref) => (
                        <li key={ref} className="break-all font-mono text-xs">
                          {ref}
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}
                {variant.error_class ? (
                  <div className="rounded-md border border-amber-500/30 bg-amber-500/10 p-2 text-xs">
                    <p>
                      Failure class: <strong>{humanize(variant.error_class)}</strong>
                    </p>
                    <p className="mt-1">
                      {customBuildRemediation(variant.error_class)}
                    </p>
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title="Bounded build log"
          description="Up to 800 recent lines are rendered. Reconnects resume with Last-Event-ID; operation status polling remains authoritative."
          actions={
            <StatusBadge
              state={
                stream.state === "polling"
                  ? "degraded"
                  : stream.state === "stopped"
                    ? "completed"
                    : stream.state
              }
            />
          }
        />
        {stream.state === "polling" ? (
          <div
            className="border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-xs"
            role="status"
          >
            Live logs are temporarily unavailable. Durable operation status and
            variant outcomes continue to poll every 5 seconds while stream
            reconnection is attempted.
          </div>
        ) : stream.error ? (
          <div className="border-b border-border/60 px-4 py-2 text-xs text-muted-foreground" role="status">
            {stream.error}; reconnecting from the last received event.
          </div>
        ) : null}
        <div
          className="max-h-[32rem] min-h-40 overflow-auto bg-black/70 p-4 font-mono text-xs text-slate-200"
          role="log"
          aria-label="Custom build log"
          aria-live="polite"
        >
          {stream.logs.length ? (
            <ol>
              {stream.logs.map((entry) => (
                <li key={entry.sequence} className="whitespace-pre-wrap break-words">
                  <span className="select-none text-slate-500">
                    {String(entry.sequence).padStart(4, "0")}{" "}
                  </span>
                  {entry.variant ? (
                    <span className="text-sky-300">[{entry.variant}] </span>
                  ) : null}
                  {entry.line}
                </li>
              ))}
            </ol>
          ) : (
            <div className="flex min-h-32 items-center justify-center text-slate-400">
              <TerminalSquareIcon className="mr-2 size-4" aria-hidden="true" />
              {terminal ? "No retained log lines." : "Waiting for build output…"}
            </div>
          )}
        </div>
      </section>

      {failedVariants.length > 0 ? (
        <div className="flex items-start gap-2 rounded-lg border border-border/70 bg-card p-3 text-xs text-muted-foreground">
          <ListRestartIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          Retry creates another durable attempt for this build after you confirm
          its current version and supply an audit reason.
        </div>
      ) : null}

      <ActionDialog
        open={action === "cancel"}
        onOpenChange={(open) => setAction(open ? "cancel" : undefined)}
        title="Cancel custom build"
        description="Cancellation is cooperative. An active build step may finish before the worker records the cancellation."
        confirmLabel="Request cancellation"
        destructive
        pending={command.isPending}
        confirmationText={build.id}
        onConfirm={(reason) => command.mutate({ kind: "cancel", reason })}
      />
      <ActionDialog
        open={action === "retry"}
        onOpenChange={(open) => setAction(open ? "retry" : undefined)}
        title="Retry custom build"
        description="Retry this terminal operation with the same safe build configuration. Type the build ID and explain why another attempt is appropriate."
        confirmLabel="Retry build"
        pending={command.isPending}
        confirmationText={build.id}
        onConfirm={(reason) => command.mutate({ kind: "retry", reason })}
      />
    </div>
  );
}

function DetailTerm({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0">{children}</dd>
    </div>
  );
}

function variantLabel(variant: string): string {
  return variant === "codeql" ? "CodeQL" : humanize(variant);
}

function customBuildAuditHref(operationId: string, traceId?: string): string {
  const params = new URLSearchParams({
    tab: "audit",
    operation_id: operationId,
  });
  if (traceId) params.set("trace_id", traceId);
  return `/scanners?${params.toString()}`;
}
