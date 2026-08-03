import { memo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2Icon,
  CloudCogIcon,
  KeyRoundIcon,
  PlusIcon,
  RefreshCwIcon,
  SaveIcon,
  ShieldCheckIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
  parseJson,
  scannerSupplyChainApi,
  type RegistryInput,
  type RegistryJobKind,
  type RegistryJobState,
  type RegistryQuarantineState,
  type RegistrySummary,
} from "@/lib/scanner-supply-chain";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  RegistryJobsPanel,
  RegistryQuarantinePanel,
  RegistrySectionNavigation,
  type RegistryWorkspaceFilters,
  type RegistryWorkspaceView,
} from "./registry-operations-panel";
import {
  safeBackendFailureMessage,
  safeDisplayText,
  safeErrorMessage,
} from "@/lib/safe-display";

type RegistryDraft = {
  name: string;
  type: string;
  host: string;
  namespace: string;
  secret_reference: string;
  trust_policy_reference: string;
  platforms: string;
  enabled: boolean;
};

export const RegistriesPanel = memo(function RegistriesPanel({
  view = "targets",
  registryId,
  jobId,
  jobCursor,
  jobKind,
  jobState,
  quarantineState,
  onViewChange,
  onJobCursorChange = () => undefined,
  onFiltersChange,
  onSelectRegistry,
}: {
  view?: RegistryWorkspaceView;
  registryId?: string;
  jobId?: string;
  jobCursor?: string;
  jobKind?: RegistryJobKind | "";
  jobState?: RegistryJobState | "";
  quarantineState?: RegistryQuarantineState | "";
  onViewChange: (view: RegistryWorkspaceView) => void;
  onJobCursorChange?: (cursor?: string) => void;
  onFiltersChange: (filters: Partial<RegistryWorkspaceFilters>) => void;
  onSelectRegistry: (id?: string) => void;
}) {
  const filters: RegistryWorkspaceFilters = {
    registryId,
    jobId,
    jobKind,
    jobState,
    quarantineState,
  };
  if (view === "jobs") {
    return (
      <RegistryJobsPanel
        filters={filters}
        cursor={jobCursor}
        onCursorChange={onJobCursorChange}
        onViewChange={onViewChange}
        onFiltersChange={onFiltersChange}
      />
    );
  }
  if (view === "quarantine") {
    return (
      <RegistryQuarantinePanel
        filters={filters}
        onViewChange={onViewChange}
        onFiltersChange={onFiltersChange}
      />
    );
  }
  return (
    <RegistryTargetsPanel
      registryId={registryId}
      onSelectRegistry={onSelectRegistry}
      onViewChange={onViewChange}
    />
  );
});

const RegistryTargetsPanel = memo(function RegistryTargetsPanel({
  registryId,
  onSelectRegistry,
  onViewChange,
}: {
  registryId?: string;
  onSelectRegistry: (id?: string) => void;
  onViewChange: (view: RegistryWorkspaceView) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const registries = useQuery({
    queryKey: ["scanner-supply-chain", "registries"],
    queryFn: scannerSupplyChainApi.registries,
    refetchInterval: 60_000,
  });
  const releases = useQuery({
    queryKey: ["scanner-supply-chain", "releases", "registry-reconcile"],
    queryFn: () => scannerSupplyChainApi.releases({ limit: 100 }),
    staleTime: 60_000,
  });
  const items = registries.data?.items ?? [];
  const selected = items.find((item) => item.id === registryId);

  const action = useMutation({
    mutationFn: ({
      id,
      command,
      releaseId,
    }: {
      id: string;
      command: "check" | "reconcile";
      releaseId?: string;
    }) => {
      if (!permissions.manageRegistries || !capabilities.candidates) {
        throw new Error("Registry operations require candidate mode");
      }
      return scannerSupplyChainApi.registryAction(id, command, releaseId);
    },
    onSuccess: (receipt, variables) => {
      toast.success(
        variables.command === "check"
          ? receipt.reachable
            ? "Registry connectivity verified"
            : "Registry check completed with an error"
          : receipt.matched
            ? "Registry digest parity verified"
            : "Registry reconciliation found drift",
      );
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "registries"],
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Registry action failed")),
  });
  const create = useMutation({
    mutationFn: (draft: RegistryDraft) => {
      if (!permissions.manageRegistries || !capabilities.candidates) {
        throw new Error("Registry creation requires candidate mode");
      }
      return scannerSupplyChainApi.createRegistry(registryPayload(draft));
    },
    onSuccess: (registry) => {
      toast.success(`Registry ${registry.name} created`);
      setCreating(false);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "registries"],
      });
      onSelectRegistry(registry.id);
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Registry creation failed")),
  });
  const update = useMutation({
    mutationFn: (draft: RegistryDraft) => {
      if (!permissions.manageRegistries || !capabilities.candidates) {
        throw new Error("Registry changes require candidate mode");
      }
      if (!selected) throw new Error("Registry is unavailable");
      return scannerSupplyChainApi.updateRegistry(
        selected.id,
        registryPayload(draft),
        selected.version,
      );
    },
    onSuccess: (registry) => {
      toast.success(`Registry ${registry.name} updated`);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "registries"],
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Registry update failed")),
  });

  return (
    <div className="space-y-5">
      <PageHeading
        title="Registries and trust"
        description="Primary, mirror, private, and offline targets. Wolf stores credential and trust references only; secret material is never returned to this page."
        actions={
          <Button
            type="button"
            onClick={() => {
              setCreating(true);
              onSelectRegistry(undefined);
            }}
            disabled={
              capabilitiesLoading ||
              !permissions.manageRegistries ||
              !capabilities.candidates
            }
            title={
              !capabilities.candidates ? "Requires candidate mode" : undefined
            }
          >
            <PlusIcon aria-hidden="true" /> Add registry
          </Button>
        }
      />
      <RegistrySectionNavigation view="targets" onViewChange={onViewChange} />
      <ResourceState
        loading={registries.isPending}
        error={registries.error}
        empty={items.length === 0 && !creating}
        emptyTitle="No registry targets"
        emptyDescription="Add a managed, mirror, private, or offline registry target using secret references."
        onRetry={() => registries.refetch()}
      >
        <div className="grid gap-4 xl:grid-cols-[minmax(18rem,0.8fr)_minmax(28rem,1.2fr)]">
          <div className="space-y-2">
            {items.map((registry) => (
              <button
                key={registry.id}
                type="button"
                onClick={() => {
                  setCreating(false);
                  onSelectRegistry(registry.id);
                }}
                className={`w-full rounded-lg border p-4 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                  selected?.id === registry.id
                    ? "border-primary/50 bg-primary/5"
                    : "border-border/70 bg-card hover:bg-muted/15"
                }`}
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate font-medium">{registry.name}</p>
                    <p className="mt-1 truncate text-xs text-muted-foreground">
                      {registry.host}/{registry.namespace}
                    </p>
                  </div>
                  <StatusBadge
                    state={
                      registry.health ??
                      (registry.enabled ? "enabled" : "disabled")
                    }
                  />
                </div>
                <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted-foreground">
                  <span>{humanize(registry.type)}</span>
                  {registry.type === "mirror" ? (
                    <span>
                      {registry.digest_parity
                        ? "Digest parity"
                        : "Parity unknown"}
                    </span>
                  ) : null}
                  {registry.last_checked_at ? (
                    <span>
                      Checked <Timestamp value={registry.last_checked_at} />
                    </span>
                  ) : null}
                </div>
              </button>
            ))}
          </div>

          {creating ? (
            <RegistryEditor
              title="Add registry target"
              description="Reference an existing Wolf secret; do not paste a registry password or signing key."
              initial={emptyDraft()}
              submitLabel="Create registry"
              pending={create.isPending}
              onSubmit={(draft) => create.mutate(draft)}
            />
          ) : selected ? (
            <RegistryDetail
              registry={selected}
              actionPending={action.isPending}
              actionResult={action.data}
              updatePending={update.isPending}
              releases={releases.data?.items ?? []}
              onAction={(command, releaseId) =>
                action.mutate({ id: selected.id, command, releaseId })
              }
              onUpdate={(draft) => update.mutate(draft)}
            />
          ) : (
            <div className="grid min-h-72 place-items-center rounded-lg border border-dashed border-border p-8 text-center">
              <div>
                <CloudCogIcon
                  className="mx-auto size-8 text-muted-foreground"
                  aria-hidden="true"
                />
                <p className="mt-3 text-sm font-medium">Select a registry</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  Inspect trust, permission, mirror, and retention health.
                </p>
              </div>
            </div>
          )}
        </div>
      </ResourceState>
    </div>
  );
});

function RegistryDetail({
  registry,
  actionPending,
  actionResult,
  updatePending,
  releases,
  onAction,
  onUpdate,
}: {
  registry: RegistrySummary;
  actionPending: boolean;
  actionResult?: Awaited<
    ReturnType<typeof scannerSupplyChainApi.registryAction>
  >;
  updatePending: boolean;
  releases: Array<{ id: string; name: string; state: string }>;
  onAction: (action: "check" | "reconcile", releaseId?: string) => void;
  onUpdate: (draft: RegistryDraft) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const [releaseId, setReleaseId] = useState("");
  const platformPolicy = parseJson<Record<string, unknown>>(
    registry.platform_policy,
    {},
  );
  return (
    <div className="space-y-4">
      <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <PanelHeading
          title={registry.name}
          description={`${humanize(registry.type)} registry target`}
          actions={
            <>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={
                  capabilitiesLoading ||
                  !permissions.manageRegistries ||
                  !capabilities.candidates ||
                  actionPending
                }
                onClick={() => onAction("check")}
              >
                <ShieldCheckIcon aria-hidden="true" /> Test connectivity
              </Button>
            </>
          }
        />
        <dl className="grid gap-3 p-4 sm:grid-cols-2">
          <HealthValue label="Health">
            <StatusBadge state={registry.health ?? "unknown"} />
          </HealthValue>
          <HealthValue label="Digest parity">
            {registry.digest_parity === undefined ? (
              "Not checked"
            ) : registry.digest_parity ? (
              <span className="inline-flex items-center gap-1 text-emerald-300">
                <CheckCircle2Icon className="size-4" aria-hidden="true" />
                Matches primary
              </span>
            ) : (
              <span className="text-red-300">Drift detected</span>
            )}
          </HealthValue>
          <HealthValue label="Mirror lag">
            {registry.mirror_lag_seconds !== undefined
              ? `${registry.mirror_lag_seconds}s`
              : "Not reported"}
          </HealthValue>
          <HealthValue label="Signer identity">
            {registry.signer_identity ?? "From trust policy"}
          </HealthValue>
          <HealthValue label="Permissions">
            {registry.permissions?.join(", ") ?? "Not checked"}
          </HealthValue>
          <HealthValue label="Credential reference">
            {registry.credential_reference_configured
              ? "Configured (opaque Wolf secret)"
              : "Anonymous access"}
          </HealthValue>
          <HealthValue label="Protected releases">
            {registry.protected_releases ?? "Not reported"}
          </HealthValue>
        </dl>
        {registry.error ? (
          <p className="border-t border-border/50 bg-red-500/10 p-3 text-sm text-red-300">
            Registry health verification failed. Review the operation audit and
            credential-reference configuration before retrying.
          </p>
        ) : null}
        {actionResult?.registry_id === registry.id ? (
          <div
            className={`border-t border-border/50 p-3 text-sm ${
              actionResult.reachable === false || actionResult.matched === false
                ? "bg-amber-500/10 text-amber-200"
                : "bg-emerald-500/10 text-emerald-200"
            }`}
            role="status"
          >
            {actionResult.error
              ? safeBackendFailureMessage(
                  "registry_check_failed",
                  "Registry verification did not complete. Review bounded evidence before retrying.",
                )
              : actionResult.matched !== undefined
                ? actionResult.matched
                  ? "Every checked release image matches its expected digest."
                  : "One or more release images did not match the expected digest."
                : actionResult.reachable
                  ? `Registry reachable${actionResult.latency_ms !== undefined ? ` in ${actionResult.latency_ms} ms` : ""}.`
                  : "Registry is not reachable."}
          </div>
        ) : null}
        {registry.type === "mirror" ? (
          <div className="grid gap-2 border-t border-border/50 p-4 sm:grid-cols-[1fr_auto]">
            <label>
              <span className="mb-1 block text-xs font-medium">
                Release for digest reconciliation
              </span>
              <select
                value={releaseId}
                onChange={(event) => setReleaseId(event.target.value)}
                className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
              >
                <option value="">Choose a published release</option>
                {releases.map((release) => (
                  <option key={release.id} value={release.id}>
                    {release.name || release.id} · {humanize(release.state)}
                  </option>
                ))}
              </select>
            </label>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="self-end"
              disabled={
                capabilitiesLoading ||
                !permissions.manageRegistries ||
                !capabilities.candidates ||
                actionPending ||
                !releaseId
              }
              onClick={() => onAction("reconcile", releaseId)}
            >
              <RefreshCwIcon aria-hidden="true" /> Reconcile parity
            </Button>
          </div>
        ) : null}
        {Object.keys(platformPolicy).length ? (
          <div className="border-t border-border/50 p-4">
            <p className="text-xs font-medium text-muted-foreground">
              Platform policy
            </p>
            <CodeValue>
              {Array.isArray(platformPolicy.platforms)
                ? platformPolicy.platforms
                    .filter(
                      (value): value is string => typeof value === "string",
                    )
                    .map((value) => safeDisplayText(value, 64))
                    .join(", ") || "No platforms reported"
                : "No platforms reported"}
            </CodeValue>
          </div>
        ) : null}
      </section>
      <RegistryEditor
        key={registry.id}
        title="Registry configuration"
        description="Credential and trust fields are references to server-side secrets and policy objects."
        initial={registryDraft(registry)}
        submitLabel="Save changes"
        pending={updatePending}
        onSubmit={onUpdate}
      />
    </div>
  );
}

function RegistryEditor({
  title,
  description,
  initial,
  submitLabel,
  pending,
  onSubmit,
}: {
  title: string;
  description: string;
  initial: RegistryDraft;
  submitLabel: string;
  pending: boolean;
  onSubmit: (draft: RegistryDraft) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const [draft, setDraft] = useState(initial);
  const valid =
    draft.name.trim() &&
    draft.type &&
    draft.host.trim() &&
    draft.namespace.trim();
  return (
    <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
      <PanelHeading title={title} description={description} />
      <form
        className="grid gap-4 p-4 sm:grid-cols-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) onSubmit(draft);
        }}
      >
        <Field label="Name">
          <Input
            value={draft.name}
            onChange={(event) =>
              setDraft({ ...draft, name: event.target.value })
            }
            required
          />
        </Field>
        <Field label="Role">
          <select
            value={draft.type}
            onChange={(event) =>
              setDraft({ ...draft, type: event.target.value })
            }
            className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
          >
            <option value="managed">Primary managed</option>
            <option value="mirror">Mirror</option>
            <option value="private">Private</option>
            <option value="air_gap">Offline target</option>
          </select>
        </Field>
        <Field label="Registry host">
          <Input
            value={draft.host}
            onChange={(event) =>
              setDraft({ ...draft, host: event.target.value })
            }
            placeholder="registry.example.com"
            required
          />
        </Field>
        <Field label="Namespace">
          <Input
            value={draft.namespace}
            onChange={(event) =>
              setDraft({ ...draft, namespace: event.target.value })
            }
            placeholder="security/scanners"
            required
          />
        </Field>
        <Field label="Credential secret reference">
          <div className="relative">
            <KeyRoundIcon
              className="pointer-events-none absolute left-3 top-3 size-4 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              className="pl-9"
              value={draft.secret_reference}
              onChange={(event) =>
                setDraft({ ...draft, secret_reference: event.target.value })
              }
              placeholder="secret:00000000-0000-0000-0000-000000000000"
              autoComplete="off"
              aria-describedby="registry-credential-reference-help"
            />
          </div>
          <span
            id="registry-credential-reference-help"
            className="block text-xs text-muted-foreground"
          >
            Use secret:&lt;uuid&gt;. The value is write-only; leave blank to
            keep an existing reference.
          </span>
        </Field>
        <Field label="Trust policy reference">
          <Input
            value={draft.trust_policy_reference}
            onChange={(event) =>
              setDraft({ ...draft, trust_policy_reference: event.target.value })
            }
            placeholder="trust-policy://alpha-bravo-managed"
          />
        </Field>
        <Field label="Platforms">
          <Input
            value={draft.platforms}
            onChange={(event) =>
              setDraft({ ...draft, platforms: event.target.value })
            }
            placeholder="linux/amd64, linux/arm64"
          />
        </Field>
        <label className="flex items-center gap-2 self-end pb-2 text-sm">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(event) =>
              setDraft({ ...draft, enabled: event.target.checked })
            }
            className="size-4 rounded border-border accent-primary"
          />
          Enabled
        </label>
        <div className="flex justify-end sm:col-span-2">
          <Button
            type="submit"
            disabled={
              capabilitiesLoading ||
              !permissions.manageRegistries ||
              !capabilities.candidates ||
              !valid ||
              pending
            }
          >
            <SaveIcon aria-hidden="true" /> {submitLabel}
          </Button>
        </div>
      </form>
    </section>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="space-y-1.5">
      <span className="text-xs font-medium">{label}</span>
      {children}
    </label>
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
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0 text-sm">{children}</dd>
    </div>
  );
}

function emptyDraft(): RegistryDraft {
  return {
    name: "",
    type: "private",
    host: "",
    namespace: "",
    secret_reference: "",
    trust_policy_reference: "",
    platforms: "linux/amd64, linux/arm64",
    enabled: true,
  };
}

function registryDraft(registry: RegistrySummary): RegistryDraft {
  const policy = parseJson<{ platforms?: string[] }>(
    registry.platform_policy,
    {},
  );
  return {
    name: registry.name,
    type: registry.type,
    host: registry.host,
    namespace: registry.namespace,
    secret_reference: "",
    trust_policy_reference: registry.trust_policy_reference ?? "",
    platforms: policy.platforms?.join(", ") ?? "",
    enabled: registry.enabled,
  };
}

function registryPayload(draft: RegistryDraft): RegistryInput {
  return {
    name: draft.name.trim(),
    type: draft.type,
    host: draft.host.trim(),
    namespace: draft.namespace.trim(),
    secret_reference: draft.secret_reference.trim() || undefined,
    trust_policy_reference: draft.trust_policy_reference.trim() || undefined,
    platform_policy: {
      platforms: draft.platforms
        .split(",")
        .map((platform) => platform.trim())
        .filter(Boolean),
    },
    enabled: draft.enabled,
  };
}
