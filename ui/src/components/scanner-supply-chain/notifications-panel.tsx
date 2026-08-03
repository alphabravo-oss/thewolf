import {
  lazy,
  memo,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangleIcon,
  ArrowLeftIcon,
  BellRingIcon,
  CheckCheckIcon,
  CircleAlertIcon,
  ExternalLinkIcon,
  InfoIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
} from "lucide-react";
import { CursorNavigation } from "./cursor-navigation";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { CardSkeleton } from "@/components/skeleton";
import { ActionDialog } from "./action-dialog";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  CodeValue,
  humanize,
  MetricCard,
  PageHeading,
  PartialFailureBanner,
  ResourceState,
  StatusBadge,
  Timestamp,
} from "./primitives";
import {
  scannerSupplyChainApi,
  type ScannerNotification,
  type ScannerNotificationDestination,
  type ScannerNotificationFilters,
  type ScannerNotificationState,
} from "@/lib/scanner-supply-chain";
import { cn } from "@/lib/utils";
import {
  safeBackendFailureMessage,
  safeErrorMessage,
} from "@/lib/safe-display";

const PAGE_SIZE = 25;
const MAX_SEEN_IDS = 500;
const SEEN_STORAGE_KEY = "wolf.scanner-notifications.seen.v1";

const NOTIFICATION_TYPES = [
  "critical_update_discovered",
  "candidate_ready_for_approval",
  "gate_failure",
  "release_published",
  "stable_release_health_issue",
  "canary_started",
  "canary_passed",
  "canary_failed",
  "rollout_paused",
  "rollout_rolled_back",
  "rollout_completed",
  "mirror_drift",
] as const;

type AttentionFilter = "all" | "unread" | "dead_letter";
type Severity = "critical" | "warning" | "info";
export type NotificationCenterView = "deliveries" | "alerts";
export interface NotificationListFilters {
  attention: AttentionFilter;
  state: ScannerNotificationState | "";
  destination: ScannerNotificationDestination | "";
  notificationType: string;
}

const DEFAULT_NOTIFICATION_FILTERS: NotificationListFilters = {
  attention: "all",
  state: "",
  destination: "",
  notificationType: "",
};

const AlertsPanel = lazy(() =>
  import("./alerts-panel").then((module) => ({
    default: module.AlertsPanel,
  })),
);

export const NotificationsPanel = memo(function NotificationsPanel({
  view = "deliveries",
  notificationId,
  cursor,
  onCursorChange = () => undefined,
  filters,
  onFiltersChange,
  alertId,
  onViewChange,
  onSelectNotification,
  onSelectAlert,
}: {
  view?: NotificationCenterView;
  notificationId?: string;
  cursor?: string;
  onCursorChange?: (cursor?: string) => void;
  filters?: NotificationListFilters;
  onFiltersChange?: (filters: NotificationListFilters) => void;
  alertId?: string;
  onViewChange?: (view: NotificationCenterView) => void;
  onSelectNotification: (notification?: string) => void;
  onSelectAlert?: (alert?: string) => void;
}) {
  const [seenIds, setSeenIds] = useState<Set<string>>(readSeenIds);
  const [localFilters, setLocalFilters] = useState(
    DEFAULT_NOTIFICATION_FILTERS,
  );
  const activeFilters = filters ?? localFilters;
  const changeFilters = onFiltersChange ?? setLocalFilters;

  const markRead = useCallback((id: string) => {
    setSeenIds((current) => {
      if (current.has(id)) return current;
      const nextIds = [id, ...current].slice(0, MAX_SEEN_IDS);
      writeSeenIds(nextIds);
      return new Set(nextIds);
    });
  }, []);

  return (
    <div className="space-y-5">
      <nav
        aria-label="Notification center views"
        className="flex w-fit gap-1 rounded-lg border border-border/70 bg-card p-1"
      >
        <Button
          type="button"
          size="sm"
          variant={view === "deliveries" ? "default" : "ghost"}
          aria-current={view === "deliveries" ? "page" : undefined}
          onClick={() => onViewChange?.("deliveries")}
        >
          <BellRingIcon aria-hidden="true" /> Delivery records
        </Button>
        <Button
          type="button"
          size="sm"
          variant={view === "alerts" ? "default" : "ghost"}
          aria-current={view === "alerts" ? "page" : undefined}
          onClick={() => onViewChange?.("alerts")}
        >
          <ShieldAlertIcon aria-hidden="true" /> Operational alerts
        </Button>
      </nav>
      {view === "alerts" ? (
        <Suspense fallback={<CardSkeleton />}>
          <AlertsPanel
            alertId={alertId}
            onSelectAlert={onSelectAlert ?? (() => undefined)}
          />
        </Suspense>
      ) : notificationId ? (
        <NotificationDetail
          notificationId={notificationId}
          onBack={() => onSelectNotification(undefined)}
          onRead={markRead}
        />
      ) : (
        <NotificationList
          cursor={cursor}
          onCursorChange={onCursorChange}
          filters={activeFilters}
          onFiltersChange={changeFilters}
          seenIds={seenIds}
          onMarkRead={markRead}
          onSelectNotification={onSelectNotification}
        />
      )}
    </div>
  );
});

function NotificationList({
  cursor,
  onCursorChange,
  filters: selectedFilters,
  onFiltersChange,
  seenIds,
  onMarkRead,
  onSelectNotification,
}: {
  cursor?: string;
  onCursorChange: (cursor?: string) => void;
  filters: NotificationListFilters;
  onFiltersChange: (filters: NotificationListFilters) => void;
  seenIds: Set<string>;
  onMarkRead: (id: string) => void;
  onSelectNotification: (notification: string) => void;
}) {
  const { attention, state, destination, notificationType } = selectedFilters;
  const filters = useMemo<ScannerNotificationFilters>(
    () => ({
      state: attention === "dead_letter" ? "dead_letter" : state || undefined,
      destination_type:
        attention === "unread" ? "ui" : destination || undefined,
      notification_type: notificationType || undefined,
      cursor,
      limit: PAGE_SIZE,
    }),
    [attention, cursor, destination, notificationType, state],
  );
  const notifications = useQuery({
    queryKey: ["scanner-supply-chain", "notifications", filters],
    queryFn: () => scannerSupplyChainApi.notifications(filters),
    placeholderData: (previous) => previous,
  });
  const serverItems = notifications.data?.items ?? [];
  const items = useMemo(
    () =>
      attention === "unread"
        ? serverItems.filter(
            (item) => item.destination_type === "ui" && !seenIds.has(item.id),
          )
        : serverItems,
    [attention, seenIds, serverItems],
  );
  const unreadOnPage = serverItems.filter(
    (item) => item.destination_type === "ui" && !seenIds.has(item.id),
  ).length;
  const deadLettersOnPage = serverItems.filter(
    (item) => item.state === "dead_letter",
  ).length;
  const inFlightOnPage = serverItems.filter((item) =>
    ["pending", "delivering", "retry"].includes(item.state),
  ).length;

  const resetPage = useCallback(
    () => onCursorChange(undefined),
    [onCursorChange],
  );
  const selectAttention = useCallback(
    (next: AttentionFilter) => {
      onFiltersChange({
        ...selectedFilters,
        attention: next,
        state: next === "all" ? state : "",
        destination: next === "all" ? destination : "",
      });
      resetPage();
    },
    [destination, onFiltersChange, resetPage, selectedFilters, state],
  );

  function openNotification(notification: ScannerNotification) {
    if (notification.destination_type === "ui") onMarkRead(notification.id);
    onSelectNotification(notification.id);
  }

  function markLoadedRead() {
    serverItems.forEach((item) => {
      if (item.destination_type === "ui") onMarkRead(item.id);
    });
  }

  return (
    <div className="space-y-5">
      <PageHeading
        title="Scanner notifications"
        description="Delivery history and operational alerts for scanner updates, candidates, releases, rollouts, and registries."
        actions={
          unreadOnPage > 0 ? (
            <Button type="button" variant="outline" onClick={markLoadedRead}>
              <CheckCheckIcon aria-hidden="true" /> Mark loaded read
            </Button>
          ) : undefined
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Loaded records"
          value={serverItems.length}
          detail={`Bounded to ${PAGE_SIZE} per page`}
        />
        <MetricCard
          label="Unread here"
          value={unreadOnPage}
          detail="UI notifications in this browser"
          state={unreadOnPage ? "warning" : "good"}
        />
        <MetricCard
          label="In delivery"
          value={inFlightOnPage}
          detail="Pending, delivering, or retrying"
          state={inFlightOnPage ? "warning" : "neutral"}
        />
        <MetricCard
          label="Dead letters"
          value={deadLettersOnPage}
          detail="Operator remediation required"
          state={deadLettersOnPage ? "danger" : "good"}
        />
      </div>

      <section
        className="space-y-3 rounded-lg border border-border/70 bg-card p-3"
        aria-label="Notification filters"
      >
        <div className="flex flex-wrap gap-2" aria-label="Attention filters">
          {(
            [
              ["all", "All"],
              ["unread", "Unread"],
              ["dead_letter", "Dead letters"],
            ] as const
          ).map(([value, label]) => (
            <Button
              key={value}
              type="button"
              size="sm"
              variant={attention === value ? "default" : "outline"}
              aria-pressed={attention === value}
              onClick={() => selectAttention(value)}
            >
              {label}
            </Button>
          ))}
        </div>
        <div className="grid gap-2 md:grid-cols-3">
          <FilterSelect
            label="Delivery status"
            value={state}
            onChange={(value) => {
              onFiltersChange({
                ...selectedFilters,
                attention: "all",
                state: value as ScannerNotificationState | "",
              });
              resetPage();
            }}
          >
            <option value="">All statuses</option>
            <option value="pending">Pending</option>
            <option value="delivering">Delivering</option>
            <option value="retry">Retry</option>
            <option value="delivered">Delivered</option>
            <option value="dead_letter">Dead letter</option>
          </FilterSelect>
          <FilterSelect
            label="Destination"
            value={destination}
            onChange={(value) => {
              onFiltersChange({
                ...selectedFilters,
                attention: "all",
                destination: value as ScannerNotificationDestination | "",
              });
              resetPage();
            }}
          >
            <option value="">All destinations</option>
            <option value="ui">Wolf UI</option>
            <option value="webhook">Webhook</option>
            <option value="email">Email</option>
            <option value="siem">SIEM</option>
          </FilterSelect>
          <FilterSelect
            label="Notification type"
            value={notificationType}
            onChange={(value) => {
              onFiltersChange({ ...selectedFilters, notificationType: value });
              resetPage();
            }}
          >
            <option value="">All notification types</option>
            {NOTIFICATION_TYPES.map((type) => (
              <option key={type} value={type}>
                {humanize(type)}
              </option>
            ))}
          </FilterSelect>
        </div>
        <p className="text-xs text-muted-foreground">
          Unread is personal to this browser and does not alter immutable
          delivery history. Filters and pagination are server bounded.
        </p>
      </section>

      <PartialFailureBanner
        failures={
          notifications.isPlaceholderData
            ? [
                {
                  resource: "Notification filters",
                  message:
                    "Showing the prior page while the requested server page loads.",
                },
              ]
            : undefined
        }
      />

      <ResourceState
        loading={notifications.isPending}
        error={notifications.error}
        empty={items.length === 0}
        emptyTitle={
          attention === "unread"
            ? "No unread notifications on this page"
            : "No matching notifications"
        }
        emptyDescription={
          attention === "unread"
            ? "Unread state is local to this browser. Continue to the next page or change the filter."
            : "Delivery records appear here when scanner release events match a configured destination."
        }
        onRetry={() => notifications.refetch()}
      >
        <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
          <div
            className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            role="region"
            tabIndex={0}
            aria-label="Scanner notification deliveries"
          >
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead className="border-b border-border/60 bg-muted/20 text-xs text-muted-foreground">
                <tr>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Severity
                  </th>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Notification
                  </th>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Aggregate
                  </th>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Destination
                  </th>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Status
                  </th>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Attempts
                  </th>
                  <th scope="col" className="px-4 py-3 font-medium">
                    Created
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {items.map((notification) => {
                  const unread =
                    notification.destination_type === "ui" &&
                    !seenIds.has(notification.id);
                  return (
                    <tr key={notification.id} className="hover:bg-muted/15">
                      <td className="px-4 py-3 align-top">
                        <SeverityBadge
                          severity={notificationSeverity(notification)}
                        />
                      </td>
                      <td className="px-4 py-3 align-top">
                        <button
                          type="button"
                          onClick={() => openNotification(notification)}
                          className="group max-w-xs text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          aria-label={`Open notification ${humanize(notification.notification_type)}`}
                        >
                          <span className="flex items-center gap-2 font-medium group-hover:underline">
                            {unread ? (
                              <>
                                <span
                                  className="size-2 shrink-0 rounded-full bg-sky-400"
                                  aria-hidden="true"
                                />
                                <span className="sr-only">Unread: </span>
                              </>
                            ) : null}
                            {humanize(notification.notification_type)}
                          </span>
                          <CodeValue>{notification.id}</CodeValue>
                        </button>
                      </td>
                      <td className="px-4 py-3 align-top">
                        <span className="block">
                          {humanize(notification.aggregate_type)}
                        </span>
                        <CodeValue>{notification.aggregate_id}</CodeValue>
                      </td>
                      <td className="px-4 py-3 align-top">
                        <span className="block">
                          {humanize(notification.destination_type)}
                        </span>
                        <CodeValue>{notification.destination_ref}</CodeValue>
                      </td>
                      <td className="px-4 py-3 align-top">
                        <StatusBadge state={notification.state} />
                      </td>
                      <td className="px-4 py-3 align-top tabular-nums">
                        {notification.attempt}/{notification.max_attempts}
                      </td>
                      <td className="whitespace-nowrap px-4 py-3 align-top text-xs">
                        <Timestamp value={notification.created_at} />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      </ResourceState>

      <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
        <CursorNavigation
          currentCursor={cursor}
          nextCursor={notifications.data?.next_cursor}
          loading={notifications.isFetching}
          label="Notification history"
          onCursorChange={onCursorChange}
        />
      </div>
    </div>
  );
}

function NotificationDetail({
  notificationId,
  onBack,
  onRead,
}: {
  notificationId: string;
  onBack: () => void;
  onRead: (id: string) => void;
}) {
  const queryClient = useQueryClient();
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const [retryOpen, setRetryOpen] = useState(false);
  const detail = useQuery({
    queryKey: ["scanner-supply-chain", "notification", notificationId],
    queryFn: () => scannerSupplyChainApi.notification(notificationId),
  });
  const notification = detail.data?.notification;

  useEffect(() => {
    if (notification?.destination_type === "ui") onRead(notification.id);
  }, [notification?.destination_type, notification?.id, onRead]);

  const retry = useMutation({
    mutationFn: (reason: string) => {
      if (!detail.data?.etag) {
        throw new Error(
          "The server did not provide a concurrency token. Reload before retrying.",
        );
      }
      return scannerSupplyChainApi.retryNotification(
        notificationId,
        reason,
        detail.data.etag,
      );
    },
    onSuccess: () => {
      toast.success("Notification retry accepted");
      setRetryOpen(false);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "notifications"],
      });
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "notification", notificationId],
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Notification retry failed")),
  });
  const retryUnavailableReason = capabilitiesLoading
    ? "Checking scanner release capabilities."
    : !permissions.administer
      ? "Supply-chain administrator access is required."
      : !capabilities.candidates
        ? "Retries are disabled in observe-only mode."
        : !detail.data?.etag
          ? "The server did not provide an ETag. Reload this record."
          : undefined;
  const link = notification ? aggregateLink(notification) : undefined;

  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> All notifications
      </Button>
      <ResourceState
        loading={detail.isPending}
        error={detail.error}
        onRetry={() => detail.refetch()}
        variant="cards"
      >
        {notification ? (
          <>
            <PageHeading
              title={humanize(notification.notification_type)}
              description={
                <span className="flex flex-wrap items-center gap-2">
                  <SeverityBadge
                    severity={notificationSeverity(notification)}
                  />
                  <StatusBadge state={notification.state} />
                  <CodeValue>{notification.id}</CodeValue>
                </span>
              }
              actions={
                <>
                  {link ? (
                    <Button asChild variant="outline">
                      <a href={link.href}>
                        {link.label} <ExternalLinkIcon aria-hidden="true" />
                      </a>
                    </Button>
                  ) : null}
                  {notification.state === "dead_letter" ? (
                    <Button
                      type="button"
                      onClick={() => setRetryOpen(true)}
                      disabled={Boolean(retryUnavailableReason)}
                      title={retryUnavailableReason}
                    >
                      <RotateCcwIcon aria-hidden="true" /> Retry delivery
                    </Button>
                  ) : null}
                </>
              }
            />

            {notification.state === "dead_letter" ? (
              <section
                className="rounded-lg border border-red-500/40 bg-red-500/10 p-4"
                aria-labelledby="notification-failure-heading"
              >
                <div className="flex items-start gap-3">
                  <CircleAlertIcon
                    className="mt-0.5 size-5 shrink-0 text-red-300"
                    aria-hidden="true"
                  />
                  <div className="min-w-0">
                    <h2
                      id="notification-failure-heading"
                      className="font-medium text-red-200"
                    >
                      Notification delivery requires review
                    </h2>
                    <p className="mt-1 whitespace-pre-wrap break-words text-sm text-red-100/80">
                      {safeBackendFailureMessage(
                        notification.error_class,
                        "Delivery attempts were exhausted. Inspect the affected resource and operation audit before retrying.",
                      )}
                    </p>
                    {link ? (
                      <a
                        href={link.href}
                        className="mt-3 inline-flex items-center gap-1 text-sm font-medium text-red-100 underline underline-offset-4"
                      >
                        Inspect the affected resource
                        <ExternalLinkIcon
                          className="size-3.5"
                          aria-hidden="true"
                        />
                      </a>
                    ) : null}
                  </div>
                </div>
              </section>
            ) : null}

            <div className="grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(18rem,1fr)]">
              <section className="rounded-lg border border-border/70 bg-card p-4">
                <h2 className="text-sm font-semibold">Delivery record</h2>
                <dl className="mt-4 grid gap-4 sm:grid-cols-2">
                  <DetailItem label="Aggregate">
                    <span className="block">
                      {humanize(notification.aggregate_type)}
                    </span>
                    <CodeValue>{notification.aggregate_id}</CodeValue>
                  </DetailItem>
                  <DetailItem label="Event">
                    <span className="block">
                      {humanize(notification.event_type)}
                    </span>
                    <CodeValue>{notification.event_id}</CodeValue>
                  </DetailItem>
                  <DetailItem label="Destination">
                    <span className="block">
                      {humanize(notification.destination_type)}
                    </span>
                    <CodeValue>{notification.destination_ref}</CodeValue>
                  </DetailItem>
                  <DetailItem label="Attempt">
                    {notification.attempt} of {notification.max_attempts}
                  </DetailItem>
                  <DetailItem label="Policy">
                    {notification.policy_id ? (
                      <>
                        <CodeValue>{notification.policy_id}</CodeValue>
                        <span className="ml-2">
                          revision {notification.policy_revision ?? "—"}
                        </span>
                      </>
                    ) : (
                      "No policy reference"
                    )}
                  </DetailItem>
                  <DetailItem label="Available at">
                    <Timestamp value={notification.available_at} />
                  </DetailItem>
                  <DetailItem label="Created">
                    <Timestamp value={notification.created_at} />
                  </DetailItem>
                  <DetailItem label="Updated">
                    <Timestamp value={notification.updated_at} />
                  </DetailItem>
                  {notification.delivered_at ? (
                    <DetailItem label="Delivered">
                      <Timestamp value={notification.delivered_at} />
                    </DetailItem>
                  ) : null}
                  {notification.dead_lettered_at ? (
                    <DetailItem label="Dead-lettered">
                      <Timestamp value={notification.dead_lettered_at} />
                    </DetailItem>
                  ) : null}
                </dl>
              </section>

              <section className="rounded-lg border border-border/70 bg-card p-4">
                <div className="flex items-start gap-3">
                  <ShieldCheckIcon
                    className="mt-0.5 size-5 shrink-0 text-emerald-300"
                    aria-hidden="true"
                  />
                  <div>
                    <h2 className="text-sm font-semibold">
                      Delivery payload protected
                    </h2>
                    <p className="mt-1 text-sm text-muted-foreground">
                      Raw delivery payloads are intentionally not displayed.
                      This prevents rendered markup, credentials, and
                      destination secrets from entering the operator UI.
                    </p>
                    <p className="mt-3 text-xs text-muted-foreground">
                      Destination references are opaque aliases. Credentials
                      remain server-side.
                    </p>
                  </div>
                </div>
              </section>
            </div>

            {retryUnavailableReason && notification.state === "dead_letter" ? (
              <p className="text-sm text-muted-foreground" role="status">
                Retry unavailable: {retryUnavailableReason}
              </p>
            ) : null}

            <ActionDialog
              open={retryOpen}
              onOpenChange={setRetryOpen}
              title="Retry notification delivery?"
              description="Requeue this dead-lettered delivery using its current server version. A new idempotency key is generated for the command."
              confirmLabel="Retry delivery"
              pending={retry.isPending}
              onConfirm={(reason) => retry.mutate(reason)}
            />
          </>
        ) : null}
      </ResourceState>
    </div>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: React.ReactNode;
}) {
  return (
    <label>
      <span className="mb-1 block text-xs font-medium">{label}</span>
      <select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
      >
        {children}
      </select>
    </label>
  );
}

function DetailItem({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </dt>
      <dd className="mt-1 break-words text-sm">{children}</dd>
    </div>
  );
}

const SeverityBadge = memo(function SeverityBadge({
  severity,
}: {
  severity: Severity;
}) {
  const Icon =
    severity === "critical"
      ? CircleAlertIcon
      : severity === "warning"
        ? AlertTriangleIcon
        : InfoIcon;
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center gap-1 rounded-full border px-2 text-xs font-medium",
        severity === "critical"
          ? "border-red-500/30 bg-red-500/10 text-red-800 dark:text-red-300"
          : severity === "warning"
            ? "border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-300"
            : "border-sky-500/30 bg-sky-500/10 text-sky-800 dark:text-sky-300",
      )}
      aria-label={`Severity: ${humanize(severity)}`}
    >
      <Icon className="size-3" aria-hidden="true" />
      {humanize(severity)}
    </span>
  );
});

function notificationSeverity(notification: ScannerNotification): Severity {
  if (
    notification.state === "dead_letter" ||
    [
      "critical_update_discovered",
      "gate_failure",
      "canary_failed",
      "mirror_drift",
      "stable_release_health_issue",
    ].includes(notification.notification_type)
  ) {
    return "critical";
  }
  if (
    notification.state === "retry" ||
    [
      "candidate_ready_for_approval",
      "rollout_paused",
      "rollout_rolled_back",
    ].includes(notification.notification_type)
  ) {
    return "warning";
  }
  return "info";
}

function aggregateLink(
  notification: ScannerNotification,
): { href: string; label: string } | undefined {
  const id = encodeURIComponent(notification.aggregate_id);
  switch (notification.aggregate_type) {
    case "candidate":
      return {
        href: `/scanners?tab=candidates&candidate=${id}`,
        label: "Open candidate",
      };
    case "release":
      return {
        href: `/scanners?tab=releases&release=${id}`,
        label: "Open release",
      };
    case "rollout":
      return {
        href: `/scanners?tab=rollouts&rollout=${id}`,
        label: "Open rollout",
      };
    case "registry":
      return {
        href: `/scanners?tab=registries&registry=${id}`,
        label: "Open registry",
      };
    case "policy":
      return { href: "/scanners?tab=policy", label: "Open policy" };
    case "discovery":
      return { href: "/scanners?tab=updates", label: "Open updates" };
    case "rollout_cohort":
      return { href: "/scanners?tab=rollouts", label: "Open rollouts" };
    case "build":
    case "build_step":
      return { href: "/scanners?tab=candidates", label: "Open candidates" };
    default:
      return { href: "/scanners?tab=audit", label: "Open audit" };
  }
}

function readSeenIds(): Set<string> {
  if (typeof window === "undefined") return new Set();
  try {
    const value = JSON.parse(
      window.localStorage.getItem(SEEN_STORAGE_KEY) ?? "[]",
    );
    return new Set(
      Array.isArray(value)
        ? value
            .filter((id): id is string => typeof id === "string")
            .slice(0, MAX_SEEN_IDS)
        : [],
    );
  } catch {
    return new Set();
  }
}

function writeSeenIds(ids: string[]) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      SEEN_STORAGE_KEY,
      JSON.stringify(ids.slice(0, MAX_SEEN_IDS)),
    );
  } catch {
    // Notification delivery history remains available if browser storage is
    // blocked; only the personal unread affordance becomes session-local.
  }
}
