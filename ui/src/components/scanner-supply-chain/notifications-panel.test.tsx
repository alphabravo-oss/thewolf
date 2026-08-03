import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsPanel } from "./notifications-panel";
import { ScannerReleaseCapabilitiesBoundary } from "./capabilities";
import {
  scannerSupplyChainApi,
  type ScannerNotification,
  type ScannerReleaseCapabilities,
} from "@/lib/scanner-supply-chain";

vi.mock("@/lib/me", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/me")>();
  return {
    ...actual,
    useMe: () => ({
      data: { id: "admin-1", email: "admin@example.test", role: "admin" },
      isPending: false,
    }),
  };
});

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const stableCapabilities: ScannerReleaseCapabilities = {
  mode: "stable_control",
  read: true,
  candidates: true,
  canary: true,
  stable_control: true,
};

const readOnlyCapabilities: ScannerReleaseCapabilities = {
  mode: "read_only",
  read: true,
  candidates: false,
  canary: false,
  stable_control: false,
};

const deadLetter: ScannerNotification = {
  id: "notification-dead",
  event_id: "event-dead",
  aggregate_type: "release",
  aggregate_id: "release-next",
  event_type: "scanner.release.health_issue",
  notification_type: "stable_release_health_issue",
  destination_type: "webhook",
  destination_ref: "security-operations",
  policy_id: "default",
  policy_revision: 7,
  state: "dead_letter",
  payload:
    '{"html":"<script>alert(1)</script>","secret":"dckr_pat_never_render"}',
  attempt: 5,
  max_attempts: 5,
  available_at: "2026-07-30T12:00:00Z",
  dead_lettered_at: "2026-07-30T12:05:00Z",
  error_class: "destination_unavailable",
  error_detail: "Endpoint returned 503 after bounded retries.",
  version: 7,
  created_at: "2026-07-30T12:00:00Z",
  updated_at: "2026-07-30T12:05:00Z",
};

const uiNotification: ScannerNotification = {
  ...deadLetter,
  id: "notification-ui",
  event_id: "event-ui",
  aggregate_type: "candidate",
  aggregate_id: "candidate-1",
  notification_type: "candidate_ready_for_approval",
  destination_type: "ui",
  destination_ref: "wolf-ui",
  state: "delivered",
  attempt: 1,
  delivered_at: "2026-07-30T12:01:00Z",
  dead_lettered_at: undefined,
  error_class: undefined,
  error_detail: undefined,
  version: 2,
};

function wrapper(
  capabilities: ScannerReleaseCapabilities = stableCapabilities,
) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return function QueryWrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={queryClient}>
        <ScannerReleaseCapabilitiesBoundary capabilities={capabilities}>
          {children}
        </ScannerReleaseCapabilitiesBoundary>
      </QueryClientProvider>
    );
  };
}

describe("scanner notification center", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    const values = new Map<string, string>();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => values.set(key, value),
        removeItem: (key: string) => values.delete(key),
        clear: () => values.clear(),
        key: (index: number) => [...values.keys()][index] ?? null,
        get length() {
          return values.size;
        },
      },
    });
  });

  afterEach(() => {
    cleanup();
  });

  it("shows accessible operational semantics and keeps raw payloads out of the DOM", async () => {
    vi.spyOn(scannerSupplyChainApi, "notification").mockResolvedValue({
      notification: deadLetter,
      etag: '"7"',
    });

    render(
      <NotificationsPanel
        notificationId={deadLetter.id}
        onSelectNotification={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Stable Release Health Issue",
      }),
    ).toBeVisible();
    expect(screen.getByLabelText("Severity: Critical")).toBeVisible();
    expect(screen.getByLabelText("Status: Dead Letter")).toBeVisible();
    expect(
      screen.getByText(/Destination Unavailable\. Review bounded evidence/i),
    ).toBeVisible();
    expect(
      screen.queryByText("Endpoint returned 503 after bounded retries."),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open release/i })).toHaveAttribute(
      "href",
      "/scanners?tab=releases&release=release-next",
    );
    expect(
      screen.getByText(
        /Raw delivery payloads are intentionally not displayed/i,
      ),
    ).toBeVisible();
    expect(document.body).not.toHaveTextContent("dckr_pat_never_render");
    expect(document.body).not.toHaveTextContent("<script>");
  });

  it("retries a dead letter with the loaded ETag after collecting an audit reason", async () => {
    vi.spyOn(scannerSupplyChainApi, "notification").mockResolvedValue({
      notification: deadLetter,
      etag: '"notification-version-7"',
    });
    const retry = vi
      .spyOn(scannerSupplyChainApi, "retryNotification")
      .mockResolvedValue({ ...deadLetter, state: "retry", version: 8 });

    render(
      <NotificationsPanel
        notificationId={deadLetter.id}
        onSelectNotification={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    fireEvent.click(
      await screen.findByRole("button", { name: "Retry delivery" }),
    );
    const dialog = screen.getByRole("dialog", {
      name: "Retry notification delivery?",
    });
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "Webhook routing has been repaired" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Retry delivery" }),
    );

    await waitFor(() =>
      expect(retry).toHaveBeenCalledWith(
        deadLetter.id,
        "Webhook routing has been repaired",
        '"notification-version-7"',
      ),
    );
  });

  it("keeps dead-letter retry unavailable in observe-only mode", async () => {
    vi.spyOn(scannerSupplyChainApi, "notification").mockResolvedValue({
      notification: deadLetter,
      etag: '"7"',
    });

    render(
      <NotificationsPanel
        notificationId={deadLetter.id}
        onSelectNotification={vi.fn()}
      />,
      { wrapper: wrapper(readOnlyCapabilities) },
    );

    expect(
      await screen.findByRole("button", { name: "Retry delivery" }),
    ).toBeDisabled();
    expect(
      screen.getByText(
        /Retry unavailable: Retries are disabled in observe-only mode/i,
      ),
    ).toBeVisible();
  });

  it("tracks unread UI notifications locally without changing delivery state", async () => {
    vi.spyOn(scannerSupplyChainApi, "notifications").mockResolvedValue({
      items: [uiNotification, deadLetter],
    });
    const select = vi.fn();

    render(<NotificationsPanel onSelectNotification={select} />, {
      wrapper: wrapper(),
    });

    fireEvent.click(await screen.findByRole("button", { name: "Unread" }));
    const open = await screen.findByRole("button", {
      name: "Open notification Candidate Ready For Approval",
    });
    expect(
      screen.queryByRole("button", {
        name: "Open notification Stable Release Health Issue",
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/Unread is personal to this browser/i),
    ).toBeVisible();
    fireEvent.click(open);

    expect(select).toHaveBeenCalledWith(uiNotification.id);
    expect(
      JSON.parse(
        window.localStorage.getItem("wolf.scanner-notifications.seen.v1") ??
          "[]",
      ),
    ).toContain(uiNotification.id);
    expect(uiNotification.state).toBe("delivered");
  });
});
