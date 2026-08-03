import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ScanReleaseControls } from "./scan-release-controls";
import {
  scannerSupplyChainApi,
  type ScannerReleaseCapabilities,
} from "@/lib/scanner-supply-chain";
import type { Scan } from "@/lib/types";

const sourceScan: Scan = {
  id: "scan-source",
  user_id: "admin-1",
  repo_id: "repo-1",
  branch: "main",
  status: "completed",
  tools_selected: '["semgrep"]',
  tools_completed: '["semgrep"]',
  tools_running: "[]",
  tools_failed: "[]",
  finding_count: 2,
  scanner_release_id: "release-current",
  release_manifest_digest: `sha256:${"a".repeat(64)}`,
  created_at: "2026-07-30T11:00:00Z",
  updated_at: "2026-07-30T11:05:00Z",
};

function wrapper() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return function QueryWrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}

function overview(capabilities: ScannerReleaseCapabilities) {
  return {
    capabilities,
    generated_at: "2026-07-30T12:00:00Z",
  };
}

describe("scan release provenance and explicit re-scan", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("creates a distinct lineage-bearing scan under a different runnable release", async () => {
    vi.spyOn(scannerSupplyChainApi, "overview").mockResolvedValue(
      overview({
        mode: "stable_control",
        read: true,
        candidates: true,
        canary: true,
        stable_control: true,
      }),
    );
    vi.spyOn(scannerSupplyChainApi, "releases").mockResolvedValue({
      items: [
        {
          id: "release-current",
          name: "Current release",
          state: "stable",
          manifest_digest: sourceScan.release_manifest_digest,
          published_at: "2026-07-29T12:00:00Z",
        },
        {
          id: "release-next",
          name: "Approved next release",
          state: "published",
          manifest_digest: `sha256:${"b".repeat(64)}`,
          published_at: "2026-07-30T12:00:00Z",
        },
        {
          id: "release-legacy",
          name: "Legacy evidence",
          state: "published",
          manifest_digest: `sha256:${"c".repeat(64)}`,
          legacy: true,
          published_at: "2026-07-28T12:00:00Z",
        },
        {
          id: "release-revoked",
          name: "Revoked release",
          state: "revoked",
          manifest_digest: `sha256:${"d".repeat(64)}`,
          revoked_at: "2026-07-30T12:00:00Z",
          published_at: "2026-07-27T12:00:00Z",
        },
      ],
    });
    const created: Scan = {
      ...sourceScan,
      id: "scan-new",
      status: "pending",
      finding_count: 0,
      tools_completed: "[]",
      scanner_release_id: "release-next",
      release_manifest_digest: `sha256:${"b".repeat(64)}`,
      rescan_of_scan_id: sourceScan.id,
      release_selection_reason: "Compare the approved scanner rules",
    };
    const create = vi
      .spyOn(scannerSupplyChainApi, "createReleaseRescan")
      .mockResolvedValue(created);

    render(<ScanReleaseControls scan={sourceScan} authorized />, {
      wrapper: wrapper(),
    });

    expect(screen.getByText("release-current")).toBeVisible();
    expect(screen.getByText(sourceScan.release_manifest_digest!)).toBeVisible();
    const open = await screen.findByRole("button", {
      name: "Re-scan under different release",
    });
    await waitFor(() => expect(open).toBeEnabled());
    fireEvent.click(open);

    const dialog = await screen.findByRole("dialog", {
      name: "Create a distinct release re-scan?",
    });
    expect(within(dialog).getByText("This is not Retry.")).toBeVisible();
    const select = await within(dialog).findByLabelText(
      "Immutable scanner release",
    );
    expect(select).toHaveValue("release-next");
    expect(
      within(select).queryByRole("option", { name: /Legacy evidence/ }),
    ).not.toBeInTheDocument();
    expect(
      within(select).queryByRole("option", { name: /Revoked release/ }),
    ).not.toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText("Reason"), {
      target: { value: "Compare the approved scanner rules" },
    });
    fireEvent.change(within(dialog).getByLabelText(/Type release-next/i), {
      target: { value: "release-next" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", {
        name: "Create distinct re-scan",
      }),
    );

    await waitFor(() => expect(create).toHaveBeenCalledOnce());
    expect(create).toHaveBeenCalledWith(
      "scan-source",
      {
        release_id: "release-next",
        reason: "Compare the approved scanner rules",
      },
      expect.stringMatching(/^wolf-ui-/),
    );
    expect(
      await within(dialog).findByRole("heading", {
        name: "Distinct re-scan created",
      }),
    ).toBeVisible();
    expect(
      within(dialog).getByRole("link", { name: "Open new scan" }),
    ).toHaveAttribute("href", "/scans/scan-new");
    expect(within(dialog).getByText("scan-source")).toBeVisible();
  });

  it("hides the privileged action from unauthorized users", () => {
    render(<ScanReleaseControls scan={sourceScan} authorized={false} />, {
      wrapper: wrapper(),
    });

    expect(
      screen.queryByRole("button", {
        name: "Re-scan under different release",
      }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("release-current")).toBeVisible();
  });

  it("keeps release re-scan disabled in observe-only mode", async () => {
    vi.spyOn(scannerSupplyChainApi, "overview").mockResolvedValue(
      overview({
        mode: "read_only",
        read: true,
        candidates: false,
        canary: false,
        stable_control: false,
      }),
    );
    render(<ScanReleaseControls scan={sourceScan} authorized />, {
      wrapper: wrapper(),
    });

    expect(
      await screen.findByText(
        /New-release re-scan unavailable: Requires candidate mode or higher/i,
      ),
    ).toBeVisible();
    expect(
      screen.getByRole("button", {
        name: "Re-scan under different release",
      }),
    ).toBeDisabled();
  });
});
