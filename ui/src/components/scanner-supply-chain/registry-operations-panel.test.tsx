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
import {
  RegistryJobsPanel,
  RegistryQuarantinePanel,
} from "./registry-operations-panel";
import { ScannerReleaseCapabilitiesBoundary } from "./capabilities";
import {
  scannerSupplyChainApi,
  type RegistryImageObservation,
  type RegistryJob,
  type RegistryQuarantineObject,
  type RegistrySummary,
  type ScannerReleaseCapabilities,
} from "@/lib/scanner-supply-chain";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("./use-events", () => ({
  useScannerEvents: () => ({
    state: "stopped",
    events: [
      {
        id: "event-1",
        sequence: 1,
        event_type: "scanner.registry_job.completed",
        new_state: "completed",
        actor: "registry-worker",
        reason: "Exact destination closure verified",
        trace_id: "fedcba9876543210fedcba9876543210",
        operation_id: "op_registry_repair_0001",
        payload: { secret: "dckr_pat_event_never_render" },
        created_at: "2026-07-30T12:06:00Z",
      },
    ],
  }),
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

const targets: RegistrySummary[] = [
  {
    id: "registry-primary",
    name: "Managed primary",
    type: "managed",
    host: "ghcr.io",
    namespace: "wolf/scanners",
    enabled: true,
    health: "healthy",
    version: 3,
  },
  {
    id: "registry-mirror",
    name: "Docker Hub mirror",
    type: "mirror",
    host: "docker.io",
    namespace: "wolf",
    enabled: true,
    health: "degraded",
    version: 7,
  },
];

const completedJob: RegistryJob = {
  id: "registry-job-completed",
  registry_target_id: "registry-mirror",
  source_registry_target_id: "registry-primary",
  release_id: "release-next",
  kind: "repair",
  re_sign_policy: "preserve",
  state: "completed",
  actor: "admin@example.test",
  reason: "Restore exact closure",
  attempt: 1,
  max_attempts: 5,
  available_at: "2026-07-30T12:00:00Z",
  version: 4,
  started_at: "2026-07-30T12:01:00Z",
  completed_at: "2026-07-30T12:06:00Z",
  created_at: "2026-07-30T12:00:00Z",
  updated_at: "2026-07-30T12:06:00Z",
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
  return function Wrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={queryClient}>
        <ScannerReleaseCapabilitiesBoundary capabilities={capabilities}>
          {children}
        </ScannerReleaseCapabilitiesBoundary>
      </QueryClientProvider>
    );
  };
}

describe("registry reconciliation operations", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(scannerSupplyChainApi, "registries").mockResolvedValue({
      items: targets,
    });
    vi.spyOn(scannerSupplyChainApi, "releases").mockResolvedValue({
      items: [
        {
          id: "release-next",
          name: "scanner-set-next",
          state: "published",
          published_at: "2026-07-30T12:00:00Z",
        },
      ],
    });
    vi.spyOn(scannerSupplyChainApi, "registryJobs").mockResolvedValue({
      items: [completedJob],
    });
    vi.spyOn(scannerSupplyChainApi, "registryJob").mockResolvedValue({
      job: {
        ...completedJob,
        summary: {
          secret: "dckr_pat_summary_never_render",
        },
      } as RegistryJob,
      images: [
        {
          id: "image-1",
          job_id: completedJob.id,
          image_key: "semgrep",
          destination_reference:
            "docker.io/wolf/semgrep@sha256:manifest-semgrep",
          expected_digest: "sha256:manifest-semgrep",
          source_digest: "sha256:manifest-semgrep",
          destination_digest: "sha256:manifest-semgrep",
          expected_signature_digest: "sha256:signature-semgrep",
          destination_signature_digest: "sha256:signature-semgrep",
          expected_provenance_digest: "sha256:provenance-semgrep",
          destination_provenance_digest: "sha256:provenance-semgrep",
          expected_sbom_digest: "sha256:sbom-semgrep",
          destination_sbom_digest: "sha256:sbom-semgrep",
          state: "verified",
          detail: {
            secret: "dckr_pat_image_detail_never_render",
          },
          checked_at: "2026-07-30T12:06:00Z",
          created_at: "2026-07-30T12:01:00Z",
          updated_at: "2026-07-30T12:06:00Z",
        } as RegistryImageObservation,
      ],
      events_url: `/registry-jobs/${completedJob.id}/events`,
      etag: '"4"',
    });
  });

  afterEach(() => cleanup());

  it("renders exact digest evidence and correlation without raw backend payloads", async () => {
    render(
      <RegistryJobsPanel
        filters={{
          registryId: "registry-mirror",
          jobId: completedJob.id,
        }}
        onViewChange={vi.fn()}
        onFiltersChange={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(
      await screen.findByRole("heading", { name: "Exact per-image evidence" }),
    ).toBeVisible();
    const evidence = screen.getByRole("region", {
      name: "Exact evidence for semgrep",
    });
    expect(
      within(evidence).getAllByText("sha256:manifest-semgrep"),
    ).toHaveLength(3);
    expect(within(evidence).getAllByText("Verified")).toHaveLength(4);
    expect(
      screen.getByRole("link", { name: "Audit operation" }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=audit&operation_id=op_registry_repair_0001",
    );
    expect(document.body).not.toHaveTextContent(
      "dckr_pat_summary_never_render",
    );
    expect(document.body).not.toHaveTextContent(
      "dckr_pat_image_detail_never_render",
    );
    expect(document.body).not.toHaveTextContent(
      "dckr_pat_event_never_render",
    );
  });

  it("requires an exact typed destination confirmation before repair", async () => {
    const create = vi
      .spyOn(scannerSupplyChainApi, "createRegistryJob")
      .mockResolvedValue({ id: "repair-new", state: "queued" });

    render(
      <RegistryJobsPanel
        filters={{ registryId: "registry-mirror" }}
        onViewChange={vi.fn()}
        onFiltersChange={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    await screen.findByRole("option", { name: /scanner-set-next/i });
    fireEvent.change(
      screen.getByLabelText("Published release"),
      { target: { value: "release-next" } },
    );
    fireEvent.change(screen.getByLabelText("Verified repair source"), {
      target: { value: "registry-primary" },
    });
    const repair = screen.getByRole("button", { name: "Repair drift" });
    await waitFor(() => expect(repair).toBeEnabled());
    fireEvent.click(repair);

    const dialog = screen.getByRole("dialog", {
      name: "Repair registry drift?",
    });
    const submit = within(dialog).getByRole("button", {
      name: "Queue repair",
    });
    fireEvent.change(within(dialog).getByLabelText("Reason"), {
      target: { value: "Restore exact verified closure" },
    });
    fireEvent.change(
      within(dialog).getByLabelText(/Type Docker Hub mirror to confirm/),
      { target: { value: "Docker Hub mirro" } },
    );
    expect(submit).toBeDisabled();
    fireEvent.change(
      within(dialog).getByLabelText(/Type Docker Hub mirror to confirm/),
      { target: { value: "Docker Hub mirror" } },
    );
    fireEvent.click(submit);

    await waitFor(() =>
      expect(create).toHaveBeenCalledWith("registry-mirror", {
        kind: "repair",
        release_id: "release-next",
        source_registry_id: "registry-primary",
        re_sign_policy: "preserve",
        reason: "Restore exact verified closure",
        max_attempts: 5,
      }),
    );
  });

  it("keeps commands unavailable while retaining evidence access in observe-only mode", async () => {
    render(
      <RegistryJobsPanel
        filters={{ registryId: "registry-mirror" }}
        onViewChange={vi.fn()}
        onFiltersChange={vi.fn()}
      />,
      { wrapper: wrapper(readOnlyCapabilities) },
    );

    expect(
      await screen.findByRole("button", { name: "Reconcile exact evidence" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "Repair drift" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Queue guarded cleanup" }),
    ).toBeDisabled();
    expect(
      screen.getByText(/Job and evidence inspection remain available/i),
    ).toBeVisible();
  });

  it("labels quarantine eligibility as provisional and omits raw metadata", async () => {
    vi.spyOn(scannerSupplyChainApi, "registryQuarantine").mockResolvedValue({
      items: [
        {
          id: "quarantine-1",
          registry_target_id: "registry-mirror",
          repository: "wolf/partial",
          digest: "sha256:partial",
          object_kind: "manifest",
          state: "orphaned",
          protected: false,
          retention_class: "quarantine",
          retain_until: "2020-01-01T00:00:00Z",
          discovered_at: "2026-07-30T10:00:00Z",
          metadata: {
            secret: "dckr_pat_metadata_never_render",
          },
          version: 2,
          created_at: "2026-07-30T10:00:00Z",
          updated_at: "2026-07-30T12:00:00Z",
        } as RegistryQuarantineObject,
      ],
    });

    render(
      <RegistryQuarantinePanel
        filters={{ registryId: "registry-mirror" }}
        onViewChange={vi.fn()}
        onFiltersChange={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(
      await screen.findByText("Potentially cleanup eligible"),
    ).toBeVisible();
    expect(
      screen.getByText(/Database-reference authorization is still required/i),
    ).toBeVisible();
    expect(document.body).not.toHaveTextContent(
      "dckr_pat_metadata_never_render",
    );
  });
});
