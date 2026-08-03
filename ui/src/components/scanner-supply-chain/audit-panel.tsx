import { memo, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  DownloadIcon,
  SearchIcon,
  ShieldCheckIcon,
  WaypointsIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { EventTimeline } from "./history";
import { CursorNavigation } from "./cursor-navigation";
import { PageHeading, PartialFailureBanner, ResourceState } from "./primitives";
import {
  isValidScannerOperationId,
  isValidScannerTraceId,
  scannerSupplyChainApi,
} from "@/lib/scanner-supply-chain";

type CorrelationKind = "trace" | "operation";
export interface AuditPanelFilters {
  aggregateType: string;
  eventType: string;
  actor: string;
}

const DEFAULT_AUDIT_FILTERS: AuditPanelFilters = {
  aggregateType: "",
  eventType: "",
  actor: "",
};

export const AuditPanel = memo(function AuditPanel({
  traceId,
  operationId,
  cursor,
  onCursorChange = () => undefined,
  filters: controlledFilters,
  onFiltersChange,
  onCorrelationChange,
}: {
  traceId?: string;
  operationId?: string;
  cursor?: string;
  onCursorChange?: (cursor?: string) => void;
  filters?: AuditPanelFilters;
  onFiltersChange?: (filters: AuditPanelFilters) => void;
  onCorrelationChange?: (value: {
    traceId?: string;
    operationId?: string;
  }) => void;
}) {
  const [localFilters, setLocalFilters] = useState(DEFAULT_AUDIT_FILTERS);
  const filters = controlledFilters ?? localFilters;
  const changeFilters = onFiltersChange ?? setLocalFilters;
  const { aggregateType, eventType, actor } = filters;
  const [correlationKind, setCorrelationKind] = useState<CorrelationKind>(
    operationId ? "operation" : "trace",
  );
  const [correlationDraft, setCorrelationDraft] = useState(
    operationId ?? traceId ?? "",
  );
  const activeTraceId =
    traceId && isValidScannerTraceId(traceId) ? traceId.trim() : undefined;
  const activeOperationId =
    operationId && isValidScannerOperationId(operationId)
      ? operationId.trim()
      : undefined;
  const trimmedCorrelation = correlationDraft.trim();
  const correlationValid =
    trimmedCorrelation.length === 0 ||
    (correlationKind === "trace"
      ? isValidScannerTraceId(trimmedCorrelation)
      : isValidScannerOperationId(trimmedCorrelation));
  const correlationError =
    trimmedCorrelation.length > 0 && !correlationValid
      ? correlationKind === "trace"
        ? "Trace IDs must be 32 lowercase hexadecimal characters and cannot be all zeroes."
        : "Operation IDs must be 8–128 characters using letters, numbers, dot, underscore, colon, slash, or hyphen."
      : undefined;

  useEffect(() => {
    if (activeOperationId) {
      setCorrelationKind("operation");
      setCorrelationDraft(activeOperationId);
    } else if (activeTraceId) {
      setCorrelationKind("trace");
      setCorrelationDraft(activeTraceId);
    } else {
      setCorrelationDraft("");
    }
  }, [activeOperationId, activeTraceId]);

  const audit = useQuery({
    queryKey: [
      "scanner-supply-chain",
      "audit",
      aggregateType,
      eventType,
      actor,
      activeTraceId,
      activeOperationId,
      cursor,
    ],
    queryFn: () =>
      scannerSupplyChainApi.audit({
        aggregate_type: aggregateType,
        event_type: eventType,
        actor,
        trace_id: activeTraceId,
        operation_id: activeOperationId,
        cursor,
      }),
    placeholderData: (previous) => previous,
  });
  const items = audit.data?.items ?? [];
  const exportHref = useMemo(
    () =>
      auditExportHref({
        aggregate_type: aggregateType,
        event_type: eventType,
        actor,
        trace_id: activeTraceId,
        operation_id: activeOperationId,
      }),
    [activeOperationId, activeTraceId, actor, aggregateType, eventType],
  );

  function applyCorrelation() {
    if (!correlationValid) return;
    const value = trimmedCorrelation || undefined;
    onCorrelationChange?.({
      traceId: correlationKind === "trace" ? value : undefined,
      operationId: correlationKind === "operation" ? value : undefined,
    });
  }

  return (
    <div className="space-y-5">
      <PageHeading
        title="Scanner release audit"
        description="Immutable policy, registry, candidate, approval, publication, rollout, rollback, revocation, and offline-transfer events."
        actions={
          <Button asChild variant="outline">
            <a href={exportHref} download>
              <DownloadIcon aria-hidden="true" /> Export JSONL
            </a>
          </Button>
        }
      />

      <div className="grid gap-2 rounded-lg border border-border/70 bg-card p-3 md:grid-cols-3">
        <label>
          <span className="mb-1 block text-xs font-medium">Aggregate</span>
          <select
            value={aggregateType}
            onChange={(event) => {
              changeFilters({
                ...filters,
                aggregateType: event.target.value,
              });
              onCursorChange(undefined);
            }}
            className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
          >
            <option value="">All aggregates</option>
            <option value="policy">Policy</option>
            <option value="registry">Registry</option>
            <option value="discovery">Discovery</option>
            <option value="candidate">Candidate</option>
            <option value="release">Release</option>
            <option value="rollout">Rollout</option>
          </select>
        </label>
        <label>
          <span className="mb-1 block text-xs font-medium">Event type</span>
          <div className="relative">
            <SearchIcon
              className="pointer-events-none absolute left-3 top-3 size-4 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              value={eventType}
              onChange={(event) => {
                changeFilters({ ...filters, eventType: event.target.value });
                onCursorChange(undefined);
              }}
              placeholder="scanner.release.published"
              className="pl-9"
            />
          </div>
        </label>
        <label>
          <span className="mb-1 block text-xs font-medium">Actor</span>
          <Input
            value={actor}
            onChange={(event) => {
              changeFilters({ ...filters, actor: event.target.value });
              onCursorChange(undefined);
            }}
            placeholder="User or service identity"
          />
        </label>
      </div>

      <form
        className="rounded-lg border border-border/70 bg-card p-3"
        onSubmit={(event) => {
          event.preventDefault();
          applyCorrelation();
        }}
        aria-label="Audit correlation filter"
      >
        <div className="flex items-start gap-3">
          <WaypointsIcon
            className="mt-2.5 size-4 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <div className="grid min-w-0 flex-1 gap-2 md:grid-cols-[12rem_minmax(0,1fr)_auto]">
            <label>
              <span className="mb-1 block text-xs font-medium">
                Correlation type
              </span>
              <select
                value={correlationKind}
                onChange={(event) =>
                  setCorrelationKind(event.target.value as CorrelationKind)
                }
                className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
              >
                <option value="trace">Trace ID</option>
                <option value="operation">Operation ID</option>
              </select>
            </label>
            <label>
              <span className="mb-1 block text-xs font-medium">
                Exact correlation identifier
              </span>
              <Input
                value={correlationDraft}
                onChange={(event) => setCorrelationDraft(event.target.value)}
                placeholder={
                  correlationKind === "trace"
                    ? "32 lowercase hexadecimal characters"
                    : "op_… or another bounded operation ID"
                }
                autoComplete="off"
                spellCheck={false}
                className="font-mono text-xs"
                aria-invalid={Boolean(correlationError)}
                aria-describedby={
                  correlationError ? "audit-correlation-error" : undefined
                }
              />
            </label>
            <Button
              type="submit"
              className="self-end"
              disabled={!correlationValid}
            >
              {trimmedCorrelation ? "Apply exact filter" : "Clear filter"}
            </Button>
          </div>
        </div>
        {correlationError ? (
          <p
            id="audit-correlation-error"
            className="mt-2 text-sm text-destructive-text"
            role="alert"
          >
            {correlationError}
          </p>
        ) : (
          <p className="mt-2 text-xs text-muted-foreground">
            Exact matching only. Partial identifiers are never sent to the
            server or included in exports.
          </p>
        )}
      </form>

      <PartialFailureBanner
        failures={
          audit.isPlaceholderData
            ? [
                {
                  resource: "Audit filters",
                  message:
                    "Showing the prior result while the latest server-side filter is loading.",
                },
              ]
            : undefined
        }
      />

      <ResourceState
        loading={audit.isPending}
        error={audit.error}
        empty={items.length === 0}
        emptyTitle="No matching audit events"
        emptyDescription="Supply-chain mutations and state transitions appear here once they occur."
        onRetry={() => audit.refetch()}
      >
        <section className="rounded-lg border border-border/70 bg-card p-4">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-2 border-b border-border/50 pb-3">
            <div className="flex items-center gap-2">
              <ShieldCheckIcon
                className="size-4 text-emerald-300"
                aria-hidden="true"
              />
              <h2 className="text-sm font-medium">Append-only event history</h2>
            </div>
            <span className="text-xs text-muted-foreground">
              {audit.data?.total ?? items.length} event
              {(audit.data?.total ?? items.length) === 1 ? "" : "s"}
            </span>
          </div>
          <EventTimeline events={items} />
          <CursorNavigation
            currentCursor={cursor}
            nextCursor={audit.data?.next_cursor}
            loading={audit.isFetching}
            label="Audit history"
            onCursorChange={onCursorChange}
          />
        </section>
      </ResourceState>
    </div>
  );
});

function auditExportHref(filters: {
  aggregate_type?: string;
  event_type?: string;
  actor?: string;
  trace_id?: string;
  operation_id?: string;
}): string {
  const search = new URLSearchParams({ format: "jsonl" });
  for (const [key, value] of Object.entries(filters)) {
    const normalized = value?.trim();
    if (normalized) search.set(key, normalized);
  }
  return `/api/v1/scanner-supply-chain/audit/export?${search.toString()}`;
}
