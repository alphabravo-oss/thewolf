import { memo, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangleIcon,
  CheckCircle2Icon,
  FilterIcon,
  GitPullRequestArrowIcon,
  RefreshCwIcon,
  SearchIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  CodeValue,
  PageHeading,
  ResourceState,
  RiskBadge,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import {
  parseJson,
  scannerSupplyChainApi,
  type Compatibility,
  type SourceEvidence,
  type UpdateItem,
} from "@/lib/scanner-supply-chain";
import { useScannerReleaseCapabilities } from "./capabilities";
import { CursorNavigation } from "./cursor-navigation";
import {
  safeBackendFailureMessage,
  safeDisplayText,
  safeErrorMessage,
  safeEvidenceHref,
} from "@/lib/safe-display";

export interface UpdateFilters {
  q: string;
  risk: string;
  status: string;
  source: string;
  tier: string;
}

export const UpdatesPanel = memo(function UpdatesPanel({
  filters,
  cursor,
  onCursorChange = () => undefined,
  onFiltersChange,
  onCandidateCreated,
}: {
  filters: UpdateFilters;
  cursor?: string;
  onCursorChange?: (cursor?: string) => void;
  onFiltersChange: (filters: UpdateFilters) => void;
  onCandidateCreated: (id: string) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const updates = useQuery({
    queryKey: [
      "scanner-supply-chain",
      "updates",
      filters.q,
      filters.risk,
      filters.status,
      filters.source,
      filters.tier,
      cursor,
    ],
    queryFn: () =>
      scannerSupplyChainApi.updates({
        q: filters.q,
        risk: filters.risk,
        state: filters.status,
        source: filters.source,
        integration_tier: filters.tier,
        cursor,
        limit: 100,
      }),
    placeholderData: (previous) => previous,
  });

  // A selection is intentionally scoped to one exact result set. Query keeps
  // previous page data visible while fetching, so clear eagerly in callbacks
  // and again here for external URL/history-driven filter changes.
  useEffect(() => {
    setSelected(new Set());
  }, [
    cursor,
    filters.q,
    filters.risk,
    filters.source,
    filters.status,
    filters.tier,
  ]);
  const discoveryRuns = useQuery({
    queryKey: ["scanner-supply-chain", "discovery-runs", "recent"],
    queryFn: () => scannerSupplyChainApi.discoveryRuns({ limit: 5 }),
    staleTime: 30_000,
  });

  const checkNow = useMutation({
    mutationFn: () =>
      scannerSupplyChainApi.createDiscovery(
        { type: "all" },
        "On-demand complete scanner update discovery",
      ),
    onSuccess: (receipt) => {
      toast.success(`Discovery ${receipt.id} queued`);
      queryClient.invalidateQueries({ queryKey: ["scanner-supply-chain"] });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Discovery failed")),
  });

  const createCandidate = useMutation({
    mutationFn: () => {
      const discoveryRunIds = new Set(
        (updates.data?.items ?? [])
          .filter((item) => selected.has(item.id))
          .map((item) => item.discovery_run_id),
      );
      if (discoveryRunIds.size !== 1) {
        throw new Error(
          "Selected updates must belong to one exact discovery run",
        );
      }
      return scannerSupplyChainApi.createCandidate(
        [...selected],
        `Candidate created from ${selected.size} selected update${selected.size === 1 ? "" : "s"}`,
        [...discoveryRunIds][0],
      );
    },
    onSuccess: (receipt) => {
      toast.success(`Candidate ${receipt.id} queued`);
      setSelected(new Set());
      queryClient.invalidateQueries({ queryKey: ["scanner-supply-chain"] });
      onCandidateCreated(receipt.id);
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Candidate creation failed")),
  });

  const items = useMemo(
    () =>
      (updates.data?.items ?? []).filter((item) => {
        const evidence = parseJson<SourceEvidence>(item.source_evidence, {});
        if (
          filters.source &&
          (item.source ?? evidence.source ?? "").toLowerCase() !==
            filters.source.toLowerCase()
        ) {
          return false;
        }
        if (
          filters.tier &&
          (item.integration_tier ?? "").toLowerCase() !==
            filters.tier.toLowerCase()
        ) {
          return false;
        }
        return true;
      }),
    [updates.data?.items, filters.source, filters.tier],
  );
  const selectableItems = useMemo(
    () =>
      items.filter(
        (item) =>
          item.selection_state !== "held" &&
          item.selection_state !== "unsupported" &&
          item.selection_state !== "unreachable" &&
          parseJson<Compatibility>(item.compatibility, {}).compatible !== false,
      ),
    [items],
  );
  const allVisibleSelected =
    selectableItems.length > 0 &&
    selectableItems.every((item) => selected.has(item.id));

  function changeFilters(next: UpdateFilters) {
    setSelected(new Set());
    onFiltersChange(next);
  }

  function changeCursor(next?: string) {
    setSelected(new Set());
    onCursorChange(next);
  }

  function toggleAll() {
    setSelected((current) => {
      const next = new Set(current);
      if (allVisibleSelected) {
        selectableItems.forEach((item) => next.delete(item.id));
      } else {
        selectableItems.forEach((item) => next.add(item.id));
      }
      return next;
    });
  }

  return (
    <div className="space-y-5">
      <PageHeading
        title="Scanner updates"
        description="Review source evidence and compatibility before selecting changes for a complete scanner-set candidate. Incomplete source coverage is never treated as current."
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
              Check all now
            </Button>
            <Button
              type="button"
              disabled={
                capabilitiesLoading ||
                !permissions.operate ||
                !capabilities.candidates ||
                selected.size === 0 ||
                updates.isFetching ||
                createCandidate.isPending
              }
              title={
                !permissions.operate
                  ? "Scanner operator access is required"
                  : !capabilities.candidates
                    ? "Requires candidate mode"
                    : undefined
              }
              onClick={() => createCandidate.mutate()}
            >
              <GitPullRequestArrowIcon aria-hidden="true" />
              Create candidate ({selected.size})
            </Button>
          </>
        }
      />

      <DiscoveryCoverage
        loading={discoveryRuns.isPending}
        runs={discoveryRuns.data?.items}
        error={discoveryRuns.error}
      />

      <div className="rounded-lg border border-border/70 bg-card p-3">
        <div className="grid gap-2 md:grid-cols-[minmax(15rem,1fr)_repeat(4,minmax(8rem,auto))]">
          <label className="relative">
            <span className="sr-only">Search scanner updates</span>
            <SearchIcon
              className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              value={filters.q}
              onChange={(event) =>
                changeFilters({ ...filters, q: event.target.value })
              }
              placeholder="Search scanners and sources"
              className="pl-9"
            />
          </label>
          <FilterSelect
            label="Risk"
            value={filters.risk}
            onChange={(risk) => changeFilters({ ...filters, risk })}
            options={["", "critical", "high", "medium", "low", "none"]}
          />
          <FilterSelect
            label="Status"
            value={filters.status}
            onChange={(status) => changeFilters({ ...filters, status })}
            options={[
              "",
              "available",
              "held",
              "manual",
              "unsupported",
              "unreachable",
            ]}
          />
          <FilterSelect
            label="Source"
            value={filters.source}
            onChange={(source) => changeFilters({ ...filters, source })}
            options={[
              "",
              "github",
              "pypi",
              "npm",
              "docker",
              "rubygems",
              "go",
              "manual",
            ]}
          />
          <FilterSelect
            label="Tier"
            value={filters.tier}
            onChange={(tier) => changeFilters({ ...filters, tier })}
            options={["", "default", "bucket", "upstream"]}
          />
        </div>
      </div>

      <ResourceState
        loading={updates.isPending}
        error={updates.error}
        empty={items.length === 0}
        emptyTitle="No matching update records"
        emptyDescription="Run a discovery check or broaden the current filters. A no-results view does not by itself prove that every source is current."
        onRetry={() => updates.refetch()}
      >
        <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
          <div
            className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            role="region"
            tabIndex={0}
            aria-label="Scanner update inventory"
          >
            <table className="w-full min-w-[70rem] text-sm">
              <thead className="bg-muted/20 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="w-10 px-3 py-2">
                    <input
                      type="checkbox"
                      aria-label="Select all compatible visible updates"
                      checked={allVisibleSelected}
                      onChange={toggleAll}
                      disabled={
                        !permissions.operate ||
                        !capabilities.candidates ||
                        updates.isFetching
                      }
                      className="size-4 rounded border-border accent-primary"
                    />
                  </th>
                  <th className="px-3 py-2 font-medium">Component</th>
                  <th className="px-3 py-2 font-medium">Pinned</th>
                  <th className="px-3 py-2 font-medium">Available</th>
                  <th className="px-3 py-2 font-medium">Risk</th>
                  <th className="px-3 py-2 font-medium">Source evidence</th>
                  <th className="px-3 py-2 font-medium">Required gates</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {items.map((item) => (
                  <UpdateRow
                    key={item.id}
                    item={item}
                    selected={selected.has(item.id)}
                    mutable={
                      permissions.operate &&
                      capabilities.candidates &&
                      !updates.isFetching
                    }
                    onToggle={() =>
                      setSelected((current) => {
                        const next = new Set(current);
                        if (next.has(item.id)) next.delete(item.id);
                        else next.add(item.id);
                        return next;
                      })
                    }
                  />
                ))}
              </tbody>
            </table>
          </div>
          <CursorNavigation
            currentCursor={cursor}
            nextCursor={updates.data?.next_cursor}
            loading={updates.isFetching}
            label="Scanner updates"
            onCursorChange={changeCursor}
          />
        </div>
      </ResourceState>
    </div>
  );
});

const UpdateRow = memo(function UpdateRow({
  item,
  selected,
  onToggle,
  mutable,
}: {
  item: UpdateItem;
  selected: boolean;
  onToggle: () => void;
  mutable: boolean;
}) {
  const evidence = parseJson<SourceEvidence>(item.source_evidence, {});
  const compatibility = parseJson<Compatibility>(item.compatibility, {});
  const evidenceHref = safeEvidenceHref(evidence.url);
  const unavailable =
    item.selection_state === "held" ||
    item.selection_state === "unsupported" ||
    item.selection_state === "unreachable" ||
    compatibility.compatible === false;
  const gates = compatibility.required_gates ?? [];

  return (
    <tr className="[content-visibility:auto] [contain-intrinsic-size:0_72px] hover:bg-muted/10">
      <td className="px-3 py-3 align-top">
        <input
          type="checkbox"
          aria-label={`Select ${item.component_name}`}
          checked={selected}
          onChange={onToggle}
          disabled={!mutable || unavailable}
          className="size-4 rounded border-border accent-primary"
        />
      </td>
      <td className="px-3 py-3 align-top">
        <p className="font-medium">{item.component_name}</p>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {humanize(item.component_type)}
          {item.integration_tier ? ` · ${item.integration_tier}` : ""}
        </p>
      </td>
      <td className="px-3 py-3 align-top">
        <CodeValue title={item.current_value}>{item.current_value}</CodeValue>
      </td>
      <td className="px-3 py-3 align-top">
        <CodeValue title={item.available_value}>
          {item.available_value}
        </CodeValue>
        {evidence.digest_before && evidence.digest_after ? (
          <p className="mt-1 text-xs text-amber-300">Digest changed</p>
        ) : null}
      </td>
      <td className="px-3 py-3 align-top">
        <RiskBadge risk={item.risk_class} />
        {compatibility.reasons?.map((reason) => (
          <p
            key={reason}
            className="mt-1 max-w-48 text-xs text-muted-foreground"
          >
            {safeDisplayText(reason, 512)}
          </p>
        ))}
      </td>
      <td className="px-3 py-3 align-top">
        <p className="text-xs">
          {safeDisplayText(
            evidence.source ?? item.source ?? "Unknown source",
            128,
          )}
        </p>
        {evidenceHref ? (
          <a
            href={evidenceHref}
            target="_blank"
            rel="noreferrer"
            className="mt-0.5 block max-w-52 truncate text-xs text-primary hover:underline"
          >
            {evidence.url}
          </a>
        ) : null}
        {evidence.error ? (
          <p className="mt-1 max-w-52 text-xs text-red-300">
            Source evidence could not be verified. Review the discovery run
            before selecting this update.
          </p>
        ) : (
          <p className="mt-1 text-xs text-muted-foreground">
            Checked{" "}
            <Timestamp
              value={
                evidence.checked_at ?? item.last_checked_at ?? item.updated_at
              }
            />
          </p>
        )}
      </td>
      <td className="px-3 py-3 align-top">
        {gates.length ? (
          <div className="flex max-w-56 flex-wrap gap-1">
            {gates.map((gate) => (
              <span
                key={gate}
                className="rounded border border-border/60 bg-muted/30 px-1.5 py-0.5 text-[10px]"
              >
                {humanize(gate)}
              </span>
            ))}
          </div>
        ) : (
          <span className="text-xs text-muted-foreground">Policy default</span>
        )}
      </td>
      <td className="px-3 py-3 align-top">
        <StatusBadge state={item.selection_state} />
      </td>
    </tr>
  );
});

function DiscoveryCoverage({
  loading,
  runs,
  error,
}: {
  loading: boolean;
  runs?: Array<{
    id: string;
    state: string;
    completed_at?: string;
    available_count: number;
    error_class?: string;
    error_detail?: string;
  }>;
  error: unknown;
}) {
  if (loading) {
    return (
      <div className="h-12 animate-pulse rounded-lg border border-border bg-muted/20" />
    );
  }
  if (error || !runs?.length) {
    return (
      <div
        className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-100"
        role="status"
      >
        <AlertTriangleIcon className="mt-0.5 size-4" aria-hidden="true" />
        Update source coverage is unknown because no completed discovery run is
        available.
      </div>
    );
  }
  const latest = runs[0];
  const complete = latest.state === "completed" && !latest.error_detail;
  return (
    <div
      className={`flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3 text-sm ${
        complete
          ? "border-emerald-500/30 bg-emerald-500/10"
          : "border-amber-500/30 bg-amber-500/10"
      }`}
      role="status"
    >
      <span className="flex items-center gap-2">
        {complete ? (
          <CheckCircle2Icon
            className="size-4 text-emerald-300"
            aria-hidden="true"
          />
        ) : (
          <AlertTriangleIcon
            className="size-4 text-amber-300"
            aria-hidden="true"
          />
        )}
        Latest discovery is <strong>{humanize(latest.state)}</strong>
        {latest.error_detail
          ? `: ${safeBackendFailureMessage(latest.error_class, "Discovery did not complete. Review bounded run evidence before retrying.")}`
          : ""}
      </span>
      <span className="text-xs text-muted-foreground">
        {latest.available_count} available ·{" "}
        <Timestamp value={latest.completed_at} />
      </span>
    </div>
  );
}

function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
}) {
  return (
    <label className="relative">
      <span className="sr-only">{label}</span>
      <FilterIcon
        className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground"
        aria-hidden="true"
      />
      <select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="h-10 w-full appearance-none rounded-md border border-input bg-background pl-9 pr-8 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={label}
      >
        {options.map((option) => (
          <option key={option || "all"} value={option}>
            {option ? humanize(option) : `All ${label.toLowerCase()}s`}
          </option>
        ))}
      </select>
    </label>
  );
}
