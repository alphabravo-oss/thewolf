import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useTransition,
} from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import {
  ActivityIcon,
  BellRingIcon,
  BoxesIcon,
  GitPullRequestArrowIcon,
  HammerIcon,
  HistoryIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  RefreshCwIcon,
  RocketIcon,
  ScrollTextIcon,
  Settings2Icon,
} from "lucide-react";
import { CardSkeleton } from "@/components/skeleton";
import type { UpdateFilters } from "@/components/scanner-supply-chain/updates-panel";
import type { NotificationListFilters } from "@/components/scanner-supply-chain/notifications-panel";
import type { AuditPanelFilters } from "@/components/scanner-supply-chain/audit-panel";
import {
  CapabilityBanner,
  ScannerReleaseCapabilitiesProvider,
  useScannerReleaseCapabilities,
} from "@/components/scanner-supply-chain/capabilities";
import { cn } from "@/lib/utils";
import {
  isValidScannerOperationId,
  isValidScannerTraceId,
} from "@/lib/scanner-supply-chain";
import type { CustomBuildState } from "@/lib/scanner-custom-build";
import { OperationReceiptCenter } from "@/components/scanner-supply-chain/operation-receipt-center";

const OverviewPanel = lazy(() =>
  import("@/components/scanner-supply-chain/overview-panel").then((module) => ({
    default: module.OverviewPanel,
  })),
);
const OperationsPanel = lazy(() =>
  import("@/components/scanner-supply-chain/operations-panel").then(
    (module) => ({
      default: module.OperationsPanel,
    }),
  ),
);
const UpdatesPanel = lazy(() =>
  import("@/components/scanner-supply-chain/updates-panel").then((module) => ({
    default: module.UpdatesPanel,
  })),
);
const CandidatesPanel = lazy(() =>
  import("@/components/scanner-supply-chain/candidates-panel").then(
    (module) => ({
      default: module.CandidatesPanel,
    }),
  ),
);
const ReleasesPanel = lazy(() =>
  import("@/components/scanner-supply-chain/releases-panel").then((module) => ({
    default: module.ReleasesPanel,
  })),
);
const RolloutsPanel = lazy(() =>
  import("@/components/scanner-supply-chain/rollouts-panel").then((module) => ({
    default: module.RolloutsPanel,
  })),
);
const PolicyPanel = lazy(() =>
  import("@/components/scanner-supply-chain/policy-panel").then((module) => ({
    default: module.PolicyPanel,
  })),
);
const RegistriesPanel = lazy(() =>
  import("@/components/scanner-supply-chain/registries-panel").then(
    (module) => ({
      default: module.RegistriesPanel,
    }),
  ),
);
const SignersPanel = lazy(() =>
  import("@/components/scanner-supply-chain/signers-panel").then((module) => ({
    default: module.SignersPanel,
  })),
);
const AuditPanel = lazy(() =>
  import("@/components/scanner-supply-chain/audit-panel").then((module) => ({
    default: module.AuditPanel,
  })),
);
const NotificationsPanel = lazy(() =>
  import("@/components/scanner-supply-chain/notifications-panel").then(
    (module) => ({
      default: module.NotificationsPanel,
    }),
  ),
);
const CustomBuildsPanel = lazy(() =>
  import("@/components/scanner-custom-builds/custom-builds-panel").then(
    (module) => ({
      default: module.CustomBuildsPanel,
    }),
  ),
);

type ScannerTab =
  | "overview"
  | "operations"
  | "updates"
  | "candidates"
  | "releases"
  | "rollouts"
  | "policy"
  | "registries"
  | "custom_builds"
  | "signing"
  | "notifications"
  | "audit";
type NotificationCenterView = "deliveries" | "alerts";
type RegistryWorkspaceView = "targets" | "jobs" | "quarantine";
type RegistryJobKind = "reconcile" | "repair" | "cleanup";
type RegistryJobState =
  | "queued"
  | "claimed"
  | "retry"
  | "completed"
  | "dead_letter"
  | "cancelled";
type RegistryQuarantineState =
  | "quarantined"
  | "promoted"
  | "orphaned"
  | "deleting"
  | "deleted"
  | "retained"
  | "delete_failed";

const TAB_KEYS = new Set<ScannerTab>([
  "overview",
  "operations",
  "updates",
  "candidates",
  "releases",
  "rollouts",
  "policy",
  "registries",
  "custom_builds",
  "signing",
  "notifications",
  "audit",
]);

type ScannerSearch = {
  tab?: ScannerTab;
  candidate?: string;
  candidate_cursor?: string;
  candidate_state?: string;
  release?: string;
  release_cursor?: string;
  release_state?: string;
  compare?: string;
  rollout?: string;
  rollout_cursor?: string;
  rollout_state?: string;
  registry?: string;
  registry_view?: RegistryWorkspaceView;
  registry_job?: string;
  registry_job_cursor?: string;
  registry_job_kind?: RegistryJobKind | "";
  registry_job_state?: RegistryJobState | "";
  registry_quarantine_state?: RegistryQuarantineState | "";
  custom_build?: string;
  custom_build_state?: CustomBuildState | "";
  custom_build_trace_id?: string;
  custom_build_operation_id?: string;
  signer?: string;
  notification?: string;
  notification_cursor?: string;
  notification_attention?: "all" | "unread" | "dead_letter";
  notification_state?: NotificationListFilters["state"];
  notification_destination?: NotificationListFilters["destination"];
  notification_type?: string;
  alert?: string;
  notification_view?: NotificationCenterView;
  q?: string;
  update_cursor?: string;
  risk?: string;
  update_status?: string;
  update_source?: string;
  integration_tier?: string;
  trace_id?: string;
  operation_id?: string;
  audit_cursor?: string;
  audit_aggregate?: string;
  audit_event_type?: string;
  audit_actor?: string;
};

export const Route = createFileRoute("/_authed/scanners")({
  validateSearch: (search: Record<string, unknown>): ScannerSearch => ({
    tab:
      typeof search.tab === "string" && TAB_KEYS.has(search.tab as ScannerTab)
        ? (search.tab as ScannerTab)
        : "overview",
    candidate: stringValue(search.candidate),
    candidate_cursor: stringValue(search.candidate_cursor),
    candidate_state: stringValue(search.candidate_state) ?? "",
    release: stringValue(search.release),
    release_cursor: stringValue(search.release_cursor),
    release_state: stringValue(search.release_state) ?? "",
    compare: stringValue(search.compare),
    rollout: stringValue(search.rollout),
    rollout_cursor: stringValue(search.rollout_cursor),
    rollout_state: stringValue(search.rollout_state) ?? "",
    registry: stringValue(search.registry),
    registry_view: registryWorkspaceView(search.registry_view),
    registry_job: stringValue(search.registry_job),
    registry_job_cursor: stringValue(search.registry_job_cursor),
    registry_job_kind: registryJobKind(search.registry_job_kind),
    registry_job_state: registryJobState(search.registry_job_state),
    registry_quarantine_state: registryQuarantineState(
      search.registry_quarantine_state,
    ),
    custom_build: stringValue(search.custom_build),
    custom_build_state: customBuildState(search.custom_build_state),
    custom_build_trace_id:
      typeof search.custom_build_trace_id === "string" &&
      isValidScannerTraceId(search.custom_build_trace_id)
        ? search.custom_build_trace_id.trim()
        : undefined,
    custom_build_operation_id:
      typeof search.custom_build_operation_id === "string" &&
      isValidScannerOperationId(search.custom_build_operation_id)
        ? search.custom_build_operation_id.trim()
        : undefined,
    signer: stringValue(search.signer),
    notification: stringValue(search.notification),
    notification_cursor: stringValue(search.notification_cursor),
    notification_attention: notificationAttention(
      search.notification_attention,
    ),
    notification_state: notificationState(search.notification_state),
    notification_destination: notificationDestination(
      search.notification_destination,
    ),
    notification_type: stringValue(search.notification_type) ?? "",
    alert: stringValue(search.alert),
    notification_view:
      search.notification_view === "alerts" || stringValue(search.alert)
        ? "alerts"
        : "deliveries",
    q: stringValue(search.q) ?? "",
    update_cursor: stringValue(search.update_cursor),
    risk: stringValue(search.risk) ?? "",
    update_status: stringValue(search.update_status),
    update_source: stringValue(search.update_source),
    integration_tier: stringValue(search.integration_tier),
    trace_id:
      typeof search.trace_id === "string" &&
      isValidScannerTraceId(search.trace_id)
        ? search.trace_id.trim()
        : undefined,
    operation_id:
      typeof search.operation_id === "string" &&
      isValidScannerOperationId(search.operation_id)
        ? search.operation_id.trim()
        : undefined,
    audit_cursor: stringValue(search.audit_cursor),
    audit_aggregate: stringValue(search.audit_aggregate) ?? "",
    audit_event_type: stringValue(search.audit_event_type) ?? "",
    audit_actor: stringValue(search.audit_actor) ?? "",
  }),
  component: ScannersPage,
});

const TABS: Array<{
  key: ScannerTab;
  label: string;
  icon: typeof LayoutDashboardIcon;
  permission?: "manageRegistries" | "administer" | "systemAdmin";
}> = [
  { key: "overview", label: "Overview", icon: LayoutDashboardIcon },
  { key: "operations", label: "Operations", icon: ActivityIcon },
  { key: "updates", label: "Updates", icon: RefreshCwIcon },
  { key: "candidates", label: "Candidates", icon: GitPullRequestArrowIcon },
  { key: "releases", label: "Releases", icon: BoxesIcon },
  { key: "rollouts", label: "Rollouts", icon: RocketIcon },
  { key: "policy", label: "Policy", icon: Settings2Icon },
  {
    key: "registries",
    label: "Registries",
    icon: HistoryIcon,
    permission: "manageRegistries",
  },
  {
    key: "custom_builds",
    label: "Custom builds",
    icon: HammerIcon,
    permission: "systemAdmin",
  },
  {
    key: "signing",
    label: "Signing",
    icon: KeyRoundIcon,
    permission: "administer",
  },
  { key: "notifications", label: "Notifications", icon: BellRingIcon },
  { key: "audit", label: "Audit", icon: ScrollTextIcon },
];

function ScannersPage() {
  return (
    <ScannerReleaseCapabilitiesProvider>
      <ScannersWorkspace />
    </ScannerReleaseCapabilitiesProvider>
  );
}

function ScannersWorkspace() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: "/scanners" });
  const [isNavigating, startTransition] = useTransition();
  const activeTabRef = useRef<HTMLButtonElement>(null);
  const { permissions, loading: capabilitiesLoading } =
    useScannerReleaseCapabilities();
  const availableTabs = useMemo(
    () =>
      TABS.filter(
        (tab) => !tab.permission || permissions[tab.permission] === true,
      ),
    [permissions],
  );
  const activeTab =
    availableTabs.find((tab) => tab.key === (search.tab ?? "overview")) ??
    availableTabs[0] ??
    TABS[0];
  const activeTabKey = activeTab.key;

  useEffect(() => {
    activeTabRef.current?.scrollIntoView({
      block: "nearest",
      inline: "nearest",
    });
  }, [activeTabKey, capabilitiesLoading, availableTabs.length]);

  useEffect(() => {
    if (capabilitiesLoading || search.tab === activeTabKey) return;
    navigate({
      to: "/scanners",
      search: (previous) => ({ ...previous, tab: activeTabKey }),
      replace: true,
    });
  }, [activeTabKey, capabilitiesLoading, navigate, search.tab]);

  function updateSearch(next: Partial<ScannerSearch>, replace = false) {
    startTransition(() => {
      navigate({
        to: "/scanners",
        search: (previous) => ({ ...previous, ...next }),
        replace,
      });
    });
  }

  function selectTab(tab: ScannerTab) {
    updateSearch({ tab });
  }

  const updateFilters: UpdateFilters = {
    q: search.q ?? "",
    risk: search.risk ?? "",
    status: search.update_status ?? "",
    source: search.update_source ?? "",
    tier: search.integration_tier ?? "",
  };

  return (
    <div className="page stack min-w-0 max-w-full">
      <CapabilityBanner />
      <OperationReceiptCenter />
      <p className="sr-only" aria-live="polite" aria-atomic="true">
        Showing {activeTab.label} scanner release panel
      </p>
      <nav
        aria-label="Scanner release management"
        className="sticky top-0 z-10 -mx-1 max-w-[calc(100%+0.5rem)] overflow-x-auto overscroll-x-contain border-b border-border/60 bg-background/90 px-1 pt-1 backdrop-blur"
      >
        <div className="flex min-w-max gap-1">
          {availableTabs.map((tab) => {
            const Icon = tab.icon;
            const active = activeTabKey === tab.key;
            return (
              <button
                key={tab.key}
                ref={active ? activeTabRef : undefined}
                type="button"
                aria-current={active ? "page" : undefined}
                onClick={() => selectTab(tab.key)}
                className={cn(
                  "inline-flex h-10 items-center gap-1.5 border-b-2 px-3 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
                  active
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="size-4" aria-hidden="true" />
                {tab.label}
              </button>
            );
          })}
        </div>
      </nav>
      <div
        className={cn("min-w-0", isNavigating && "opacity-80")}
        aria-busy={isNavigating}
      >
        <Suspense fallback={<PanelFallback />}>
          {activeTabKey === "overview" ? (
            <OverviewPanel
              onNavigate={(tab, resourceId) => {
                if (tab === "candidates") {
                  updateSearch({ tab, candidate: resourceId });
                } else if (tab === "releases") {
                  updateSearch({ tab, release: resourceId });
                } else if (tab === "rollouts") {
                  updateSearch({ tab, rollout: resourceId });
                } else {
                  updateSearch({ tab });
                }
              }}
            />
          ) : null}
          {activeTabKey === "operations" ? <OperationsPanel /> : null}
          {activeTabKey === "updates" ? (
            <UpdatesPanel
              filters={updateFilters}
              cursor={search.update_cursor}
              onCursorChange={(update_cursor) =>
                updateSearch({ update_cursor })
              }
              onFiltersChange={(filters) =>
                updateSearch(
                  {
                    q: filters.q,
                    risk: filters.risk,
                    update_status: filters.status,
                    update_source: filters.source,
                    integration_tier: filters.tier,
                    update_cursor: undefined,
                  },
                  true,
                )
              }
              onCandidateCreated={(candidate) =>
                updateSearch({ tab: "candidates", candidate })
              }
            />
          ) : null}
          {activeTabKey === "candidates" ? (
            <CandidatesPanel
              candidateId={search.candidate}
              cursor={search.candidate_cursor}
              state={search.candidate_state}
              onStateChange={(candidate_state) =>
                updateSearch(
                  { candidate_state, candidate_cursor: undefined },
                  true,
                )
              }
              onCursorChange={(candidate_cursor) =>
                updateSearch({ candidate_cursor })
              }
              onSelectCandidate={(candidate) => updateSearch({ candidate })}
            />
          ) : null}
          {activeTabKey === "releases" ? (
            <ReleasesPanel
              releaseId={search.release}
              cursor={search.release_cursor}
              state={search.release_state}
              onStateChange={(release_state) =>
                updateSearch({ release_state, release_cursor: undefined }, true)
              }
              compareId={search.compare}
              onSelectRelease={(release) =>
                updateSearch({ release, compare: undefined })
              }
              onCompare={(compare) => updateSearch({ compare })}
              onCursorChange={(release_cursor) =>
                updateSearch({ release_cursor })
              }
              onRolloutCreated={(rollout) =>
                updateSearch({ tab: "rollouts", rollout })
              }
            />
          ) : null}
          {activeTabKey === "rollouts" ? (
            <RolloutsPanel
              rolloutId={search.rollout}
              cursor={search.rollout_cursor}
              state={search.rollout_state}
              onStateChange={(rollout_state) =>
                updateSearch({ rollout_state, rollout_cursor: undefined }, true)
              }
              onCursorChange={(rollout_cursor) =>
                updateSearch({ rollout_cursor })
              }
              onSelectRollout={(rollout) => updateSearch({ rollout })}
            />
          ) : null}
          {activeTabKey === "policy" ? <PolicyPanel /> : null}
          {activeTabKey === "registries" ? (
            <RegistriesPanel
              view={search.registry_view}
              registryId={search.registry}
              jobId={search.registry_job}
              jobCursor={search.registry_job_cursor}
              jobKind={search.registry_job_kind}
              jobState={search.registry_job_state}
              quarantineState={search.registry_quarantine_state}
              onViewChange={(registry_view) => updateSearch({ registry_view })}
              onJobCursorChange={(registry_job_cursor) =>
                updateSearch({ registry_job_cursor })
              }
              onFiltersChange={(filters) =>
                updateSearch(
                  {
                    ...("registryId" in filters
                      ? { registry: filters.registryId }
                      : {}),
                    ...("jobId" in filters
                      ? { registry_job: filters.jobId }
                      : {}),
                    ...("jobKind" in filters
                      ? { registry_job_kind: filters.jobKind }
                      : {}),
                    ...("jobState" in filters
                      ? { registry_job_state: filters.jobState }
                      : {}),
                    ...("quarantineState" in filters
                      ? {
                          registry_quarantine_state: filters.quarantineState,
                        }
                      : {}),
                    ...("registryId" in filters ||
                    "jobKind" in filters ||
                    "jobState" in filters
                      ? { registry_job_cursor: undefined }
                      : {}),
                  },
                  true,
                )
              }
              onSelectRegistry={(registry) => updateSearch({ registry })}
            />
          ) : null}
          {activeTabKey === "custom_builds" ? (
            <CustomBuildsPanel
              buildId={search.custom_build}
              state={search.custom_build_state}
              traceId={search.custom_build_trace_id}
              operationId={search.custom_build_operation_id}
              onSelectBuild={(custom_build) =>
                updateSearch({
                  custom_build,
                  custom_build_trace_id: undefined,
                  custom_build_operation_id: undefined,
                })
              }
              onBuildAccepted={(receipt) =>
                updateSearch({
                  custom_build: receipt.id,
                  custom_build_trace_id: receipt.trace_id,
                  custom_build_operation_id: receipt.operation_id,
                })
              }
              onStateChange={(custom_build_state) =>
                updateSearch({ custom_build_state }, true)
              }
            />
          ) : null}
          {activeTabKey === "signing" ? (
            <SignersPanel
              signerId={search.signer}
              onSelectSigner={(signer) => updateSearch({ signer })}
            />
          ) : null}
          {activeTabKey === "notifications" ? (
            <NotificationsPanel
              view={search.notification_view}
              notificationId={search.notification}
              cursor={search.notification_cursor}
              filters={{
                attention: search.notification_attention ?? "all",
                state: search.notification_state ?? "",
                destination: search.notification_destination ?? "",
                notificationType: search.notification_type ?? "",
              }}
              onFiltersChange={(filters) =>
                updateSearch(
                  {
                    notification_attention: filters.attention,
                    notification_state: filters.state,
                    notification_destination: filters.destination,
                    notification_type: filters.notificationType,
                    notification_cursor: undefined,
                  },
                  true,
                )
              }
              alertId={search.alert}
              onViewChange={(notification_view) =>
                updateSearch({
                  notification_view,
                  notification:
                    notification_view === "deliveries"
                      ? search.notification
                      : undefined,
                  alert:
                    notification_view === "alerts" ? search.alert : undefined,
                  notification_cursor: undefined,
                })
              }
              onSelectNotification={(notification) =>
                updateSearch({ notification })
              }
              onCursorChange={(notification_cursor) =>
                updateSearch({ notification_cursor })
              }
              onSelectAlert={(alert) => updateSearch({ alert })}
            />
          ) : null}
          {activeTabKey === "audit" ? (
            <AuditPanel
              traceId={search.trace_id}
              operationId={search.operation_id}
              cursor={search.audit_cursor}
              filters={{
                aggregateType: search.audit_aggregate ?? "",
                eventType: search.audit_event_type ?? "",
                actor: search.audit_actor ?? "",
              }}
              onFiltersChange={(filters: AuditPanelFilters) =>
                updateSearch(
                  {
                    audit_aggregate: filters.aggregateType,
                    audit_event_type: filters.eventType,
                    audit_actor: filters.actor,
                    audit_cursor: undefined,
                  },
                  true,
                )
              }
              onCursorChange={(audit_cursor) => updateSearch({ audit_cursor })}
              onCorrelationChange={({ traceId, operationId }) =>
                updateSearch({
                  trace_id: traceId,
                  operation_id: operationId,
                })
              }
            />
          ) : null}
        </Suspense>
      </div>
    </div>
  );
}

function PanelFallback() {
  return (
    <div
      className="grid gap-4 md:grid-cols-2 xl:grid-cols-4"
      role="status"
      aria-live="polite"
      aria-label="Loading scanner release panel"
    >
      <span className="sr-only">Loading scanner release panel…</span>
      <CardSkeleton />
      <CardSkeleton />
      <CardSkeleton />
      <CardSkeleton />
    </div>
  );
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined;
}

function registryWorkspaceView(value: unknown): RegistryWorkspaceView {
  return value === "jobs" || value === "quarantine" ? value : "targets";
}

function registryJobKind(value: unknown): RegistryJobKind | "" {
  return value === "reconcile" || value === "repair" || value === "cleanup"
    ? value
    : "";
}

function registryJobState(value: unknown): RegistryJobState | "" {
  return value === "queued" ||
    value === "claimed" ||
    value === "retry" ||
    value === "completed" ||
    value === "dead_letter" ||
    value === "cancelled"
    ? value
    : "";
}

function registryQuarantineState(value: unknown): RegistryQuarantineState | "" {
  return value === "quarantined" ||
    value === "promoted" ||
    value === "orphaned" ||
    value === "deleting" ||
    value === "deleted" ||
    value === "retained" ||
    value === "delete_failed"
    ? value
    : "";
}

function customBuildState(value: unknown): CustomBuildState | "" {
  return value === "queued" ||
    value === "claimed" ||
    value === "running" ||
    value === "completed" ||
    value === "partial" ||
    value === "failed" ||
    value === "cancelled"
    ? value
    : "";
}

function notificationAttention(
  value: unknown,
): "all" | "unread" | "dead_letter" {
  return value === "unread" || value === "dead_letter" ? value : "all";
}

function notificationState(value: unknown): NotificationListFilters["state"] {
  return value === "pending" ||
    value === "delivering" ||
    value === "retry" ||
    value === "delivered" ||
    value === "dead_letter"
    ? value
    : "";
}

function notificationDestination(
  value: unknown,
): NotificationListFilters["destination"] {
  return value === "ui" ||
    value === "webhook" ||
    value === "email" ||
    value === "siem"
    ? value
    : "";
}
