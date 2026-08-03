import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AlertsPanel } from "./alerts-panel";
import { ScannerReleaseCapabilitiesBoundary } from "./capabilities";
import { ApiError } from "@/lib/api";
import {
  scannerSupplyChainApi,
  type ScannerAlert,
  type ScannerReleaseCapabilities,
} from "@/lib/scanner-supply-chain";

const readableCapabilities: ScannerReleaseCapabilities = {
  mode: "read_only",
  read: true,
  candidates: false,
  canary: false,
  stable_control: false,
};

const disabledCapabilities: ScannerReleaseCapabilities = {
  mode: "disabled",
  read: false,
  candidates: false,
  canary: false,
  stable_control: false,
};

const rolloutAlert: ScannerAlert = {
  id: "alert-rollout",
  fingerprint: "fingerprint-rollout",
  kind: "rollout_failure",
  severity: "critical",
  state: "open",
  scope_type: "rollout_target",
  scope_id: "production",
  summary: "The latest production scanner rollout failed or rolled back.",
  evidence: {
    rollout_id: "rollout-1",
    state: "failed",
    secret: "dckr_pat_never_render",
    nested: { html: "<script>alert(1)</script>" },
  },
  policy_id: "default",
  policy_revision: 7,
  trigger_count: 4,
  generation: 2,
  version: 5,
  first_triggered_at: "2026-07-30T10:00:00Z",
  last_triggered_at: "2026-07-30T12:00:00Z",
  created_at: "2026-07-30T10:00:00Z",
  updated_at: "2026-07-30T12:00:00Z",
};

const resolvedAlert: ScannerAlert = {
  ...rolloutAlert,
  id: "alert-discovery",
  fingerprint: "fingerprint-discovery",
  kind: "missed_discovery",
  severity: "warning",
  state: "resolved",
  scope_type: "policy_scope",
  scope_id: "global",
  summary: "Scanner discovery exceeded its configured maximum age.",
  evidence: {
    age_seconds: 90_000,
    threshold_seconds: 86_400,
    last_completed_at: "2026-07-29T10:00:00Z",
  },
  trigger_count: 8,
  generation: 3,
  version: 9,
  resolved_at: "2026-07-30T12:05:00Z",
};

function wrapper(
  capabilities: ScannerReleaseCapabilities = readableCapabilities,
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

describe("scanner operational alerts", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it("renders accessible condition semantics and only allowlisted scalar evidence", async () => {
    vi.spyOn(scannerSupplyChainApi, "alerts").mockResolvedValue({
      items: [rolloutAlert],
    });

    render(<AlertsPanel onSelectAlert={vi.fn()} />, { wrapper: wrapper() });

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Scanner alerts",
      }),
    ).toBeVisible();
    expect(
      await screen.findByLabelText("Severity: Critical"),
    ).toBeVisible();
    expect(screen.getByLabelText("Status: Open")).toBeVisible();
    expect(
      screen.getByText(
        "The latest production scanner rollout failed or rolled back.",
      ),
    ).toBeVisible();
    expect(screen.getByText("rollout-1")).toBeVisible();
    expect(screen.getByText("failed")).toBeVisible();
    expect(document.body).not.toHaveTextContent("dckr_pat_never_render");
    expect(document.body).not.toHaveTextContent("<script>");
    expect(
      screen.getByRole("link", { name: /Review rollout/i }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=rollouts&rollout=rollout-1",
    );
  });

  it("applies lifecycle filters and advances one bounded cursor page", async () => {
    const list = vi
      .spyOn(scannerSupplyChainApi, "alerts")
      .mockResolvedValue({
        items: [resolvedAlert],
        next_cursor: "cursor-2",
      });

    render(<AlertsPanel onSelectAlert={vi.fn()} />, { wrapper: wrapper() });
    await screen.findByRole("heading", { name: "Missed Discovery" });

    fireEvent.change(screen.getByLabelText("Lifecycle status"), {
      target: { value: "resolved" },
    });
    await waitFor(() =>
      expect(list).toHaveBeenCalledWith(
        expect.objectContaining({ state: "resolved", limit: 25 }),
      ),
    );
    const next = screen.getByRole("button", { name: "Next" });
    await waitFor(() => expect(next).toBeEnabled());
    fireEvent.click(next);
    await waitFor(() =>
      expect(list).toHaveBeenCalledWith(
        expect.objectContaining({ cursor: "cursor-2", limit: 25 }),
      ),
    );
    expect(screen.getByText("Page 2")).toBeVisible();
  });

  it("shows resolution and aggregate reopen history without inventing transition timestamps", async () => {
    vi.spyOn(scannerSupplyChainApi, "alert").mockResolvedValue(resolvedAlert);

    render(
      <AlertsPanel
        alertId={resolvedAlert.id}
        onSelectAlert={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Missed Discovery",
      }),
    ).toBeVisible();
    expect(screen.getByLabelText("Severity: Warning")).toBeVisible();
    expect(screen.getByLabelText("Status: Resolved")).toBeVisible();
    expect(screen.getByText("2 recorded cycles")).toBeVisible();
    expect(
      screen.getByText(/Exact transition timestamps are not exposed/i),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: /Review updates/i }),
    ).toHaveAttribute("href", "/scanners?tab=updates");
    expect(screen.queryByRole("button", { name: /resolve|reopen/i })).not.toBeInTheDocument();
  });

  it("explains empty active state and scoped authorization failures", async () => {
    const list = vi
      .spyOn(scannerSupplyChainApi, "alerts")
      .mockResolvedValueOnce({ items: [] })
      .mockRejectedValueOnce(
        new ApiError(403, "FORBIDDEN", "read scope missing"),
      );

    const first = render(<AlertsPanel onSelectAlert={vi.fn()} />, {
      wrapper: wrapper(),
    });
    expect(
      await screen.findByText("No active scanner alerts"),
    ).toBeVisible();
    first.unmount();

    render(<AlertsPanel onSelectAlert={vi.fn()} />, { wrapper: wrapper() });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Additional permission required",
    );
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("does not query alerts when release-management read capability is disabled", () => {
    const list = vi.spyOn(scannerSupplyChainApi, "alerts");

    render(<AlertsPanel onSelectAlert={vi.fn()} />, {
      wrapper: wrapper(disabledCapabilities),
    });

    expect(
      screen.getByRole("heading", { name: "Scanner alerts unavailable" }),
    ).toBeVisible();
    expect(list).not.toHaveBeenCalled();
  });
});
