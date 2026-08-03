import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ScannerReleaseCapabilitiesBoundary } from "./capabilities";
import { RolloutsPanel } from "./rollouts-panel";
import {
  scannerSupplyChainApi,
  type RolloutDetail,
} from "@/lib/scanner-supply-chain";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("./use-events", () => ({
  useScannerEvents: () => ({
    state: "stopped",
    events: [],
  }),
}));

const rollout: RolloutDetail = {
  id: "rollout-health",
  target: "production",
  from_release_id: "release-stable",
  to_release_id: "release-next",
  strategy: "canary",
  state: "paused",
  version: 4,
  created_at: "2026-07-30T12:00:00Z",
  updated_at: "2026-07-30T12:10:00Z",
  automatic_rollback: true,
  cohorts: [
    {
      id: "cohort-canary",
      name: "canary",
      state: "paused",
      total_workers: 4,
      ready_workers: 3,
      failed_workers: 1,
    },
  ],
  synthetic_health: {
    corpus_id: "synthetic-corpus-raw-id-never-render",
    corpus_digest:
      "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    current: false,
    state: "passed",
    fixture_total: 24,
    fixture_passed: 24,
    fixture_failed: 0,
    observed_at: "2026-07-30T12:09:00Z",
  },
  real_scan_health: {
    state: "degraded",
    candidate_samples: 12,
    stable_samples: 30,
    candidate_infrastructure_failures: 2,
    stable_infrastructure_failures: 0,
    parser_failures: 1,
    expected_finding_losses: 1,
    candidate_p95_duration_ms: 142_000,
    stable_p95_duration_ms: 118_000,
    workers_total: 4,
    workers_ready: 3,
    workers_failed: 1,
    observed_at: "2026-07-30T12:10:00Z",
  },
};

function wrapper() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return function Wrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={client}>
        <ScannerReleaseCapabilitiesBoundary
          capabilities={{
            mode: "stable_control",
            read: true,
            candidates: true,
            canary: true,
            stable_control: true,
          }}
        >
          {children}
        </ScannerReleaseCapabilitiesBoundary>
      </QueryClientProvider>
    );
  };
}

describe("rollout evidence classes", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(scannerSupplyChainApi, "rollout").mockResolvedValue(rollout);
  });

  afterEach(() => cleanup());

  it("renders synthetic currentness separately from sampled real-scan deltas", async () => {
    render(
      <RolloutsPanel rolloutId={rollout.id} onSelectRollout={vi.fn()} />,
      { wrapper: wrapper() },
    );

    const synthetic = await screen.findByRole("region", {
      name: "Synthetic fixture verification",
    });
    expect(
      within(synthetic).getByText(
        "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      ),
    ).toBeVisible();
    expect(within(synthetic).getByLabelText("Status: Stale")).toBeVisible();
    expect(within(synthetic).getByText("24/24")).toBeVisible();
    expect(synthetic).toHaveTextContent(
      "The synthetic result is stale for the current approved corpus.",
    );

    const realScans = screen.getByRole("region", {
      name: "Sampled real-scan health",
    });
    expect(within(realScans).getByLabelText("Status: Degraded")).toBeVisible();
    expect(realScans).toHaveTextContent("Candidate samples12");
    expect(realScans).toHaveTextContent("Stable samples30");
    expect(realScans).toHaveTextContent("Expected finding losses1");
    expect(realScans).toHaveTextContent("p95 duration delta+24,000 ms");
    expect(document.body).not.toHaveTextContent(
      "synthetic-corpus-raw-id-never-render",
    );
    expect(document.body).not.toHaveTextContent("Verification scans");
  });
});
