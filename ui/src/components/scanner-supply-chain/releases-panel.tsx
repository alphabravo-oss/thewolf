import { memo, useMemo, useState } from "react";
import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  DownloadIcon,
  GitCompareArrowsIcon,
  HistoryIcon,
  RocketIcon,
  ShieldBanIcon,
  ShieldCheckIcon,
  TagIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ActionDialog } from "./action-dialog";
import { ArtifactDiffViewer } from "./artifact-diff-viewer";
import { useScannerReleaseCapabilities } from "./capabilities";
import { LegacyImportDialog } from "./legacy-import-dialog";
import {
  CodeValue,
  PageHeading,
  PanelHeading,
  ResourceState,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import {
  scannerSupplyChainApi,
  type ReleaseDetail,
  type ReleaseSummary,
} from "@/lib/scanner-supply-chain";
import { CursorNavigation } from "./cursor-navigation";
import { safeDisplayText, safeErrorMessage } from "@/lib/safe-display";

type ReleaseAction = "promote" | "deprecate" | "revoke";

export const ReleasesPanel = memo(function ReleasesPanel({
  releaseId,
  cursor,
  state: controlledState,
  onStateChange,
  compareId,
  onSelectRelease,
  onCursorChange = () => undefined,
  onCompare,
  onRolloutCreated,
}: {
  releaseId?: string;
  cursor?: string;
  state?: string;
  onStateChange?: (state: string) => void;
  compareId?: string;
  onSelectRelease: (id?: string) => void;
  onCursorChange?: (cursor?: string) => void;
  onCompare: (id?: string) => void;
  onRolloutCreated: (id: string) => void;
}) {
  const [localState, setLocalState] = useState("");
  const state = controlledState ?? localState;
  const changeState = onStateChange ?? setLocalState;
  if (releaseId && compareId) {
    return (
      <ReleaseCompare
        fromId={releaseId}
        toId={compareId}
        onBack={() => onCompare(undefined)}
      />
    );
  }
  if (releaseId) {
    return (
      <ReleaseDetailView
        releaseId={releaseId}
        onBack={() => onSelectRelease(undefined)}
        onCompare={onCompare}
        onRolloutCreated={onRolloutCreated}
      />
    );
  }
  return (
    <ReleaseList
      cursor={cursor}
      state={state}
      onStateChange={changeState}
      onCursorChange={onCursorChange}
      onSelectRelease={onSelectRelease}
    />
  );
});

function ReleaseList({
  cursor,
  state,
  onStateChange,
  onCursorChange,
  onSelectRelease,
}: {
  cursor?: string;
  state: string;
  onStateChange: (state: string) => void;
  onCursorChange: (cursor?: string) => void;
  onSelectRelease: (id: string) => void;
}) {
  const [legacyImportOpen, setLegacyImportOpen] = useState(false);
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const releases = useQuery({
    queryKey: ["scanner-supply-chain", "releases", state, cursor],
    queryFn: () =>
      scannerSupplyChainApi.releases({ state, cursor, limit: 100 }),
    placeholderData: (previous) => previous,
  });
  const items = releases.data?.items ?? [];
  return (
    <div className="space-y-5">
      <PageHeading
        title="Scanner releases"
        description="Immutable, signed scanner-set history. Runtime channels resolve to an exact manifest and image digest snapshot before assignment."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => setLegacyImportOpen(true)}
              disabled={
                capabilitiesLoading ||
                !permissions.systemAdmin ||
                !capabilities.candidates
              }
              title={
                !permissions.systemAdmin
                  ? "System administrator access is required"
                  : !capabilities.candidates
                    ? "Legacy import requires candidate mode or higher"
                    : undefined
              }
            >
              <HistoryIcon aria-hidden="true" /> Import legacy snapshot
            </Button>
            <select
              value={state}
              onChange={(event) => {
                onStateChange(event.target.value);
                onCursorChange(undefined);
              }}
              aria-label="Filter releases by state"
              className="h-10 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <option value="">All states</option>
              <option value="published">Published</option>
              <option value="stable">Stable</option>
              <option value="deprecated">Deprecated</option>
              <option value="revoked">Revoked</option>
            </select>
          </div>
        }
      />
      <ResourceState
        loading={releases.isPending}
        error={releases.error}
        empty={items.length === 0}
        emptyTitle="No scanner releases"
        emptyDescription="A release appears only after an approved candidate is published with immutable artifact evidence."
        onRetry={() => releases.refetch()}
      >
        <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
          <div
            className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            role="region"
            tabIndex={0}
            aria-label="Scanner release inventory"
          >
            <table className="w-full min-w-[66rem] text-sm">
              <thead className="bg-muted/20 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">Release</th>
                  <th className="px-4 py-2 font-medium">Channels</th>
                  <th className="px-4 py-2 font-medium">State</th>
                  <th className="px-4 py-2 font-medium">Signer</th>
                  <th className="px-4 py-2 font-medium">Platforms</th>
                  <th className="px-4 py-2 font-medium">Coverage</th>
                  <th className="px-4 py-2 font-medium">Published</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {items.map((release) => (
                  <tr
                    key={release.id}
                    className="cursor-pointer [content-visibility:auto] [contain-intrinsic-size:0_68px] hover:bg-muted/15"
                    tabIndex={0}
                    onClick={() => onSelectRelease(release.id)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onSelectRelease(release.id);
                      }
                    }}
                    aria-label={`Open release ${release.name ?? release.id}`}
                  >
                    <td className="px-4 py-3">
                      <p className="font-medium">
                        {release.name ?? release.id}
                      </p>
                      <CodeValue title={release.manifest_digest}>
                        {release.manifest_digest}
                      </CodeValue>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex flex-wrap gap-1">
                        {(release.channels ?? []).length ? (
                          release.channels?.map((channel) => (
                            <span
                              key={channel}
                              className="inline-flex items-center gap-1 rounded border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-xs text-primary"
                            >
                              <TagIcon className="size-3" aria-hidden="true" />
                              {channel}
                            </span>
                          ))
                        ) : (
                          <span className="text-xs text-muted-foreground">
                            Immutable only
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge
                        state={
                          release.legacy
                            ? "legacy"
                            : release.revoked_at
                              ? "revoked"
                              : release.state
                        }
                      />
                      {release.legacy ? (
                        <p className="mt-1 text-xs text-amber-800 dark:text-amber-300">
                          Protected historical evidence
                        </p>
                      ) : null}
                      {release.rollback_eligible ? (
                        <p className="mt-1 text-xs text-emerald-800 dark:text-emerald-300">
                          Rollback eligible
                        </p>
                      ) : null}
                    </td>
                    <td className="max-w-52 truncate px-4 py-3 text-xs">
                      {release.signer_identity ?? "Unreported"}
                    </td>
                    <td className="px-4 py-3 text-xs">
                      {release.platforms?.join(", ") ?? "See manifest"}
                    </td>
                    <td className="px-4 py-3 text-xs">
                      {release.rollout_coverage !== undefined
                        ? `${Math.round(release.rollout_coverage * 100)}%`
                        : "—"}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">
                      <Timestamp value={release.published_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <CursorNavigation
            currentCursor={cursor}
            nextCursor={releases.data?.next_cursor}
            loading={releases.isFetching}
            label="Release history"
            onCursorChange={onCursorChange}
          />
        </div>
      </ResourceState>
      {legacyImportOpen ? (
        <LegacyImportDialog open onOpenChange={setLegacyImportOpen} />
      ) : null}
    </div>
  );
}

function ReleaseDetailView({
  releaseId,
  onBack,
  onCompare,
  onRolloutCreated,
}: {
  releaseId: string;
  onBack: () => void;
  onCompare: (id: string) => void;
  onRolloutCreated: (id: string) => void;
}) {
  const { capabilities, permissions } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const [action, setAction] = useState<ReleaseAction>();
  const [compareId, setCompareId] = useState("");
  const release = useQuery({
    queryKey: ["scanner-supply-chain", "release", releaseId],
    queryFn: () => scannerSupplyChainApi.release(releaseId),
  });
  const releaseOptions = useQuery({
    queryKey: ["scanner-supply-chain", "releases", "compare-options"],
    queryFn: () => scannerSupplyChainApi.releases({ limit: 100 }),
    staleTime: 60_000,
  });
  const verify = useMutation({
    mutationFn: () => scannerSupplyChainApi.verifyRelease(releaseId),
    onSuccess: (receipt) => {
      toast.success(`Verification ${receipt.id} queued`);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "release", releaseId],
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Verification failed")),
  });
  const exportBundle = useMutation({
    mutationFn: () => scannerSupplyChainApi.exportRelease(releaseId),
    onSuccess: ({ blob, filename, headers }) => {
      const objectURL = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = objectURL;
      anchor.download = filename ?? `${releaseId}.scanner-release.tar.zst`;
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(objectURL);
      const signature = headers.get("X-Wolf-Bundle-Signature-Status");
      toast.success(
        signature
          ? `Signed release bundle downloaded (${signature})`
          : "Release bundle downloaded",
      );
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Bundle export failed")),
  });
  const mutateAction = useMutation({
    mutationFn: ({
      selectedAction,
      reason,
    }: {
      selectedAction: ReleaseAction;
      reason: string;
    }) => {
      if (
        (selectedAction === "promote" && !permissions.operate) ||
        ((selectedAction === "deprecate" || selectedAction === "revoke") &&
          !permissions.administer) ||
        (selectedAction === "promote" && !capabilities.canary) ||
        ((selectedAction === "deprecate" || selectedAction === "revoke") &&
          !capabilities.stable_control)
      ) {
        throw new Error(
          "This action is unavailable in the current release-management mode",
        );
      }
      const current = release.data;
      if (!current) throw new Error("Release is unavailable");
      const payload: Record<string, unknown> = { reason };
      if (selectedAction === "promote") {
        payload.target = "stable";
        payload.strategy = "canary";
      }
      return scannerSupplyChainApi.releaseAction(
        releaseId,
        selectedAction,
        payload,
        current.version,
      );
    },
    onSuccess: (receipt, variables) => {
      toast.success(`${humanize(variables.selectedAction)} command accepted`);
      setAction(undefined);
      queryClient.invalidateQueries({ queryKey: ["scanner-supply-chain"] });
      if (variables.selectedAction === "promote") onRolloutCreated(receipt.id);
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Release action failed")),
  });

  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> All releases
      </Button>
      <ResourceState
        loading={release.isPending}
        error={release.error}
        onRetry={() => release.refetch()}
        variant="cards"
      >
        {release.data ? (
          <ReleaseContent
            release={release.data}
            verifyPending={verify.isPending}
            onVerify={() => verify.mutate()}
            exportPending={exportBundle.isPending}
            onExport={() => exportBundle.mutate()}
            onAction={setAction}
            compareId={compareId}
            onCompareId={setCompareId}
            compareOptions={
              releaseOptions.data?.items.filter(
                (item) => item.id !== releaseId,
              ) ?? []
            }
            onCompare={() => onCompare(compareId)}
          />
        ) : null}
      </ResourceState>
      {release.data && action ? (
        <ReleaseActionDialog
          action={action}
          release={release.data}
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

const ReleaseContent = memo(function ReleaseContent({
  release,
  verifyPending,
  onVerify,
  exportPending,
  onExport,
  onAction,
  compareId,
  onCompareId,
  compareOptions,
  onCompare,
}: {
  release: ReleaseDetail;
  verifyPending: boolean;
  onVerify: () => void;
  exportPending: boolean;
  onExport: () => void;
  onAction: (action: ReleaseAction) => void;
  compareId: string;
  onCompareId: (id: string) => void;
  compareOptions: ReleaseSummary[];
  onCompare: () => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const revoked = Boolean(release.revoked_at) || release.state === "revoked";
  const legacy = Boolean(release.legacy);
  return (
    <div className="space-y-5">
      <PageHeading
        title={release.name ?? release.id}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <StatusBadge state={revoked ? "revoked" : release.state} />
            <span>
              Published <Timestamp value={release.published_at} />
            </span>
            {release.rollback_eligible ? (
              <span className="text-emerald-300">Last known-good eligible</span>
            ) : null}
          </span>
        }
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={onVerify}
              disabled={
                capabilitiesLoading ||
                !permissions.read ||
                !capabilities.read ||
                verifyPending
              }
            >
              <ShieldCheckIcon aria-hidden="true" /> Verify
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={onExport}
              disabled={
                capabilitiesLoading ||
                !permissions.read ||
                !capabilities.read ||
                revoked ||
                exportPending
              }
            >
              <DownloadIcon aria-hidden="true" /> Export
            </Button>
            <Button
              type="button"
              onClick={() => onAction("promote")}
              disabled={
                capabilitiesLoading ||
                !permissions.operate ||
                !capabilities.canary ||
                revoked ||
                legacy ||
                release.state === "deprecated"
              }
              title={
                legacy
                  ? "Legacy snapshots are historical evidence and cannot be promoted"
                  : !capabilities.canary
                    ? "Requires canary mode"
                    : undefined
              }
            >
              <RocketIcon aria-hidden="true" /> Promote
            </Button>
          </>
        }
      />

      {revoked ? (
        <div className="rounded-lg border border-red-500/40 bg-red-500/10 p-4 text-sm text-red-200">
          This release is revoked. New rollout and export operations are
          blocked; inspect deployment coverage and complete the incident
          runbook.
        </div>
      ) : null}

      {legacy ? (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm text-amber-100">
          <p className="font-medium">Protected legacy evidence</p>
          <p className="mt-1 text-xs text-amber-100/80">
            This imported snapshot has unverified provenance and is not rollback
            eligible, rollout eligible, or selectable for a managed release
            re-scan. Importing it did not change desired release or runtime
            assignments.
          </p>
        </div>
      ) : null}

      <dl className="grid gap-3 rounded-lg border border-border/70 bg-card p-4 sm:grid-cols-2 xl:grid-cols-4">
        <Metadata label="Manifest digest">
          <CodeValue title={release.manifest_digest}>
            {release.manifest_digest}
          </CodeValue>
        </Metadata>
        <Metadata label="Lock digest">
          <CodeValue title={release.lock_digest}>
            {release.lock_digest}
          </CodeValue>
        </Metadata>
        <Metadata label="Definition commit">
          <CodeValue title={release.definition_commit}>
            {release.definition_commit}
          </CodeValue>
        </Metadata>
        <Metadata label="Signer">
          {release.signer_identity ?? "Unreported"}
        </Metadata>
        {legacy ? (
          <Metadata label="Retention">
            {release.retention_class ?? "legacy / protected"}
          </Metadata>
        ) : null}
      </dl>

      <div className="flex flex-col gap-2 rounded-lg border border-border/70 bg-card p-3 sm:flex-row sm:items-center">
        <GitCompareArrowsIcon
          className="size-4 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <label htmlFor="release-compare" className="text-sm font-medium">
          Compare with
        </label>
        <select
          id="release-compare"
          value={compareId}
          onChange={(event) => onCompareId(event.target.value)}
          className="h-9 min-w-0 flex-1 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <option value="">Choose a release</option>
          {compareOptions.map((option) => (
            <option key={option.id} value={option.id}>
              {option.name ?? option.id}
            </option>
          ))}
        </select>
        <Button
          type="button"
          size="sm"
          disabled={!compareId}
          onClick={onCompare}
        >
          Compare
        </Button>
      </div>

      <Tabs defaultValue="tools">
        <div className="overflow-x-auto pb-1">
          <TabsList className="min-w-max">
            <TabsTrigger value="tools">Tools</TabsTrigger>
            <TabsTrigger value="images">Images</TabsTrigger>
            <TabsTrigger value="artifacts">Artifacts</TabsTrigger>
            <TabsTrigger value="changes">Changes</TabsTrigger>
            <TabsTrigger value="verification">Verification</TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="tools">
          <InventoryTable
            headers={["Tool", "Version", "Source", "Parser compatibility"]}
            rows={(release.tools ?? []).map((tool) => [
              tool.tool_key,
              tool.version,
              tool.source_reference ?? "—",
              tool.parser_compatibility ?? "—",
            ])}
            empty="No tool inventory was attached to this release."
          />
        </TabsContent>
        <TabsContent value="images">
          <InventoryTable
            headers={[
              "Image",
              "Repository",
              "Digest",
              "Platform digests",
              "Signature",
              "Signature artifact",
              "Signer identity",
            ]}
            rows={(release.images ?? []).map((image) => [
              image.image_key,
              image.repository,
              image.digest,
              formatPlatformDigests(image.platform_digests),
              image.signature_status ?? "unknown",
              image.signature_artifact_digest ?? "—",
              image.signature_identity ?? "—",
            ])}
            empty="No image inventory was attached to this release."
          />
        </TabsContent>
        <TabsContent value="artifacts">
          <InventoryTable
            headers={["Artifact", "Media type", "Digest", "Availability"]}
            rows={(release.artifacts ?? []).map((artifact) => [
              artifact.artifact_type,
              artifact.media_type ?? "—",
              artifact.digest,
              artifact.artifact_type === "manifest_diff" ||
              artifact.artifact_type === "lock_diff"
                ? "View in Changes"
                : "Stored evidence",
            ])}
            empty="No artifact evidence was attached to this release."
          />
        </TabsContent>
        <TabsContent value="changes">
          <ArtifactDiffViewer ownerType="release" ownerId={release.id} />
        </TabsContent>
        <TabsContent value="verification">
          <div className="grid gap-3 md:grid-cols-2">
            {(["registry", "signature", "provenance", "mirrors"] as const).map(
              (key) => {
                const verification = release.verification?.[key];
                return (
                  <section
                    key={key}
                    className="rounded-lg border border-border/70 bg-card p-4"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <h3 className="text-sm font-medium">{humanize(key)}</h3>
                      <StatusBadge state={verification?.state ?? "unknown"} />
                    </div>
                    <p className="mt-2 text-xs text-muted-foreground">
                      {safeDisplayText(
                        verification?.detail ??
                          "Verification has not been reported.",
                        1_024,
                      )}
                    </p>
                    <CodeValue title={verification?.digest}>
                      {verification?.digest}
                    </CodeValue>
                  </section>
                );
              },
            )}
          </div>
        </TabsContent>
      </Tabs>

      <div className="flex flex-wrap justify-end gap-2 border-t border-border/60 pt-4">
        <Button
          type="button"
          variant="outline"
          onClick={() => onAction("deprecate")}
          disabled={
            capabilitiesLoading ||
            !permissions.administer ||
            !capabilities.stable_control ||
            revoked ||
            legacy ||
            release.state === "deprecated"
          }
        >
          <HistoryIcon aria-hidden="true" /> Deprecate
        </Button>
        <Button
          type="button"
          variant="destructive"
          onClick={() => onAction("revoke")}
          disabled={
            capabilitiesLoading ||
            !permissions.administer ||
            !capabilities.stable_control ||
            revoked ||
            legacy
          }
        >
          <ShieldBanIcon aria-hidden="true" /> Revoke
        </Button>
      </div>
    </div>
  );
});

export function formatPlatformDigests(
  value: Record<string, string> | string | undefined,
): string {
  if (!value) return "Unreported";
  let parsed: unknown = value;
  if (typeof value === "string") {
    try {
      parsed = JSON.parse(value);
    } catch {
      return "Invalid platform evidence";
    }
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return "Invalid platform evidence";
  }
  const entries = Object.entries(parsed as Record<string, unknown>);
  if (entries.length === 0 || entries.length > 4) {
    return "Invalid platform evidence";
  }
  const normalized = entries
    .map(([platform, digest]) => ({ platform, digest }))
    .filter(
      (entry): entry is { platform: string; digest: string } =>
        /^linux\/(?:amd64|arm64)$/.test(entry.platform) &&
        typeof entry.digest === "string" &&
        /^sha256:[a-f0-9]{64}$/.test(entry.digest),
    )
    .sort((left, right) => left.platform.localeCompare(right.platform));
  if (normalized.length !== entries.length) return "Invalid platform evidence";
  return normalized
    .map((entry) => `${entry.platform}: ${entry.digest}`)
    .join(" · ");
}

function ReleaseCompare({
  fromId,
  toId,
  onBack,
}: {
  fromId: string;
  toId: string;
  onBack: () => void;
}) {
  const results = useQueries({
    queries: [
      {
        queryKey: ["scanner-supply-chain", "release", fromId],
        queryFn: () => scannerSupplyChainApi.release(fromId),
      },
      {
        queryKey: ["scanner-supply-chain", "release", toId],
        queryFn: () => scannerSupplyChainApi.release(toId),
      },
    ],
  });
  const [from, to] = results;
  const error = from.error ?? to.error;
  const loading = from.isPending || to.isPending;
  const toolRows = useMemo(() => {
    if (!from.data || !to.data) return [];
    const before = new Map(
      (from.data.tools ?? []).map((tool) => [tool.tool_key, tool]),
    );
    const after = new Map(
      (to.data.tools ?? []).map((tool) => [tool.tool_key, tool]),
    );
    const keys = new Set([...before.keys(), ...after.keys()]);
    return [...keys].sort().map((key) => {
      const left = before.get(key);
      const right = after.get(key);
      return {
        key,
        from: left?.version ?? "Not present",
        to: right?.version ?? "Not present",
        changed: left?.version !== right?.version,
      };
    });
  }, [from.data, to.data]);

  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> Release detail
      </Button>
      <PageHeading
        title="Compare scanner releases"
        description={`${fromId} → ${toId}`}
      />
      <ResourceState
        loading={loading}
        error={error}
        onRetry={() => {
          from.refetch();
          to.refetch();
        }}
      >
        {from.data && to.data ? (
          <div className="space-y-4">
            <div className="grid gap-4 md:grid-cols-2">
              <ReleaseIdentity release={from.data} label="Baseline" />
              <ReleaseIdentity release={to.data} label="Comparison" />
            </div>
            <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
              <PanelHeading
                title="Tool version changes"
                description={`${toolRows.filter((row) => row.changed).length} of ${toolRows.length} tools changed`}
              />
              <div
                className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                role="region"
                tabIndex={0}
                aria-label="Tool version changes"
              >
                <table className="w-full min-w-[42rem] text-sm">
                  <thead className="bg-muted/20 text-left text-xs text-muted-foreground">
                    <tr>
                      <th className="px-4 py-2">Tool</th>
                      <th className="px-4 py-2">Baseline</th>
                      <th className="px-4 py-2">Comparison</th>
                      <th className="px-4 py-2">Change</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border/50">
                    {toolRows.map((row) => (
                      <tr
                        key={row.key}
                        className={row.changed ? "bg-amber-500/5" : ""}
                      >
                        <td className="px-4 py-2 font-medium">{row.key}</td>
                        <td className="px-4 py-2 font-mono text-xs">
                          {row.from}
                        </td>
                        <td className="px-4 py-2 font-mono text-xs">
                          {row.to}
                        </td>
                        <td className="px-4 py-2">
                          <StatusBadge
                            state={row.changed ? "changed" : "unchanged"}
                          />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        ) : null}
      </ResourceState>
    </div>
  );
}

function ReleaseIdentity({
  release,
  label,
}: {
  release: ReleaseDetail;
  label: string;
}) {
  return (
    <section className="rounded-lg border border-border/70 bg-card p-4">
      <p className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <h2 className="mt-1 font-semibold">{release.name ?? release.id}</h2>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <StatusBadge state={release.state} />
        <span className="text-xs text-muted-foreground">
          Policy {release.policy_revision ?? "—"}
        </span>
      </div>
      <CodeValue title={release.manifest_digest}>
        {release.manifest_digest}
      </CodeValue>
    </section>
  );
}

function InventoryTable({
  headers,
  rows,
  empty,
}: {
  headers: string[];
  rows: string[][];
  empty: string;
}) {
  if (!rows.length) {
    return (
      <p className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
        {empty}
      </p>
    );
  }
  return (
    <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
      <div
        className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        role="region"
        tabIndex={0}
        aria-label={`${headers.join(", ")} inventory`}
      >
        <table className="w-full min-w-[50rem] text-sm">
          <thead className="bg-muted/20 text-left text-xs text-muted-foreground">
            <tr>
              {headers.map((header) => (
                <th key={header} className="px-4 py-2 font-medium">
                  {header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border/50">
            {rows.map((row, index) => (
              <tr key={`${row[0]}-${index}`}>
                {row.map((cell, cellIndex) => (
                  <td
                    key={`${cellIndex}-${cell}`}
                    className={`max-w-80 truncate px-4 py-2 ${
                      cellIndex > 0
                        ? "font-mono text-xs text-muted-foreground"
                        : ""
                    }`}
                    title={cell}
                  >
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ReleaseActionDialog({
  action,
  release,
  pending,
  onClose,
  onConfirm,
}: {
  action: ReleaseAction;
  release: ReleaseDetail;
  pending: boolean;
  onClose: () => void;
  onConfirm: (reason: string) => void;
}) {
  const descriptions: Record<ReleaseAction, string> = {
    promote: `Create a canary-first rollout of ${release.name ?? release.id} to the stable target. Stable workers are unchanged until verification passes.`,
    deprecate: `Mark ${release.name ?? release.id} deprecated. Existing assignments remain valid, but new promotion is blocked.`,
    revoke: `Revoke ${release.name ?? release.id}. New assignments stop and affected deployments require rollback or an explicit incident policy.`,
  };
  return (
    <ActionDialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={`${humanize(action)} release?`}
      description={descriptions[action]}
      confirmLabel={humanize(action)}
      pending={pending}
      destructive={action === "revoke"}
      confirmationText={action === "revoke" ? release.id : undefined}
      onConfirm={onConfirm}
    />
  );
}

function Metadata({
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
