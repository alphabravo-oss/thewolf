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
import { ActionDialog } from "./action-dialog";
import { ArtifactDiffViewer } from "./artifact-diff-viewer";
import {
  CandidatesPanel,
  candidateFreshnessPresentation,
} from "./candidates-panel";
import { PolicyPanel } from "./policy-panel";
import { OperationsPanel } from "./operations-panel";
import { ResourceState } from "./primitives";
import { ReleasesPanel, formatPlatformDigests } from "./releases-panel";
import { UpdatesPanel, type UpdateFilters } from "./updates-panel";
import { ApiError } from "@/lib/api";
import {
  scannerSupplyChainApi,
  type ScannerReleaseCapabilities,
} from "@/lib/scanner-supply-chain";
import {
  CapabilityBanner,
  ScannerReleaseCapabilitiesBoundary,
} from "./capabilities";
import type { ScannerSupplyChainPermissions } from "@/lib/me";

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const stableControlCapabilities: ScannerReleaseCapabilities = {
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

const fullScannerPermissions: ScannerSupplyChainPermissions = {
  read: true,
  operate: true,
  approve: true,
  manageRegistries: true,
  administer: true,
  systemAdmin: true,
};

const viewerPermissions: ScannerSupplyChainPermissions = {
  read: true,
  operate: false,
  approve: false,
  manageRegistries: false,
  administer: false,
  systemAdmin: false,
};

describe("release platform evidence", () => {
  it("renders a bounded, sorted exact-digest inventory", () => {
    const amd64 = `sha256:${"a".repeat(64)}`;
    const arm64 = `sha256:${"b".repeat(64)}`;
    expect(
      formatPlatformDigests({ "linux/arm64": arm64, "linux/amd64": amd64 }),
    ).toBe(`linux/amd64: ${amd64} · linux/arm64: ${arm64}`);
  });

  it("rejects malformed or unbounded platform evidence", () => {
    expect(formatPlatformDigests("hostile backend text")).toBe(
      "Invalid platform evidence",
    );
    expect(
      formatPlatformDigests({ "linux/s390x": `sha256:${"c".repeat(64)}` }),
    ).toBe("Invalid platform evidence");
  });
});

function wrapper(
  capabilities: ScannerReleaseCapabilities = stableControlCapabilities,
  permissions: ScannerSupplyChainPermissions = fullScannerPermissions,
) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return function QueryWrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={client}>
        <ScannerReleaseCapabilitiesBoundary
          capabilities={capabilities}
          permissions={permissions}
        >
          {children}
        </ScannerReleaseCapabilitiesBoundary>
      </QueryClientProvider>
    );
  };
}

describe("scanner supply-chain enterprise states", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it("explains authorization failures and offers a retry", () => {
    const retry = vi.fn();
    render(
      <ResourceState
        loading={false}
        error={new ApiError(403, "FORBIDDEN", "forbidden")}
        onRetry={retry}
      >
        hidden
      </ResourceState>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Additional permission required",
    );
    expect(screen.getByText(/scanner supply-chain read scope/i)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it("announces generic scanner data loading without exposing stale content", () => {
    render(
      <ResourceState loading error={null}>
        stale scanner data
      </ResourceState>,
    );

    expect(
      screen.getByRole("status", { name: "Loading scanner release data" }),
    ).toHaveTextContent("Loading scanner release data…");
    expect(screen.queryByText("stale scanner data")).not.toBeInTheDocument();
  });

  it("requires an audit reason and exact typed confirmation for destructive work", () => {
    const confirm = vi.fn();
    render(
      <ActionDialog
        open
        onOpenChange={vi.fn()}
        title="Revoke release?"
        description="Stops new assignments."
        confirmLabel="Revoke"
        destructive
        confirmationText="scanner-set-42"
        onConfirm={confirm}
      />,
    );

    const revoke = screen.getByRole("button", { name: "Revoke" });
    expect(revoke).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "Compromised upstream artifact" },
    });
    fireEvent.change(screen.getByLabelText(/Type scanner-set-42/i), {
      target: { value: "wrong" },
    });
    expect(revoke).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/Type scanner-set-42/i), {
      target: { value: "scanner-set-42" },
    });
    fireEvent.click(revoke);
    expect(confirm).toHaveBeenCalledWith("Compromised upstream artifact");
  });

  it("scopes update selection to the current query and creates a candidate from exact item IDs", async () => {
    vi.spyOn(scannerSupplyChainApi, "updates").mockResolvedValue({
      items: [
        {
          id: "update-semgrep",
          discovery_run_id: "discovery-1",
          component_type: "tool",
          component_name: "semgrep",
          current_value: "1.0.0",
          available_value: "1.1.0",
          source_evidence: {
            source: "pypi",
            checked_at: "2026-07-30T12:00:00Z",
          },
          risk_class: "low",
          compatibility: {
            compatible: true,
            required_gates: ["parser", "smoke"],
          },
          selection_state: "available",
        },
      ],
      next_cursor: "update-cursor-2",
    });
    vi.spyOn(scannerSupplyChainApi, "discoveryRuns").mockResolvedValue({
      items: [
        {
          id: "discovery-1",
          trigger: "scheduled",
          definition_commit: "abc",
          policy_revision: 1,
          state: "completed",
          available_count: 1,
          selected_count: 0,
          completed_at: "2026-07-30T12:00:00Z",
          created_at: "2026-07-30T11:00:00Z",
          updated_at: "2026-07-30T12:00:00Z",
        },
      ],
    });
    const create = vi
      .spyOn(scannerSupplyChainApi, "createCandidate")
      .mockResolvedValue({ id: "candidate-1", state: "queued" });
    const onCandidateCreated = vi.fn();
    const onCursorChange = vi.fn();
    const onFiltersChange = vi.fn();
    const filters: UpdateFilters = {
      q: "",
      risk: "",
      status: "",
      source: "",
      tier: "",
    };

    const view = render(
      <UpdatesPanel
        filters={filters}
        onCursorChange={onCursorChange}
        onFiltersChange={onFiltersChange}
        onCandidateCreated={onCandidateCreated}
      />,
      { wrapper: wrapper() },
    );

    const select = await screen.findByRole("checkbox", {
      name: "Select semgrep",
    });
    fireEvent.click(select);
    expect(
      screen.getByRole("button", { name: "Create candidate (1)" }),
    ).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect(onCursorChange).toHaveBeenCalledWith("update-cursor-2");
    expect(
      screen.getByRole("button", { name: "Create candidate (0)" }),
    ).toBeDisabled();

    fireEvent.click(select);
    fireEvent.change(
      screen.getByPlaceholderText("Search scanners and sources"),
      {
        target: { value: "semgrep" },
      },
    );
    expect(onFiltersChange).toHaveBeenCalledWith({
      ...filters,
      q: "semgrep",
    });
    expect(
      screen.getByRole("button", { name: "Create candidate (0)" }),
    ).toBeDisabled();

    fireEvent.click(select);
    view.rerender(
      <UpdatesPanel
        filters={{ ...filters, risk: "low" }}
        onCursorChange={onCursorChange}
        onFiltersChange={onFiltersChange}
        onCandidateCreated={onCandidateCreated}
      />,
    );
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Create candidate (0)" }),
      ).toBeDisabled();
    });

    const refreshedSelect = screen.getByRole("checkbox", {
      name: "Select semgrep",
    });
    await waitFor(() => expect(refreshedSelect).toBeEnabled());
    fireEvent.click(refreshedSelect);
    fireEvent.click(
      screen.getByRole("button", { name: "Create candidate (1)" }),
    );

    await waitFor(() => {
      expect(create).toHaveBeenCalledWith(
        ["update-semgrep"],
        "Candidate created from 1 selected update",
        "discovery-1",
      );
    });
    expect(onCandidateCreated).toHaveBeenCalledWith("candidate-1");
  });

  it("binds approval to the candidate lock and policy decision digest", async () => {
    vi.spyOn(scannerSupplyChainApi, "candidate").mockResolvedValue({
      id: "candidate-secure",
      state: "awaiting_approval",
      definition_commit: "commit-123",
      lock_digest: "sha256:lock",
      policy_decision: "sha256:policy",
      publication_receipt_digest: "sha256:receipt",
      policy_revision: 7,
      actor: "release-bot",
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:05:00Z",
      gates: [
        {
          name: "signature",
          state: "passed",
          evidence_digest: "sha256:receipt",
        },
      ],
      separation_of_duties: {
        current_actor_can_approve: true,
        required_approvals: 1,
        valid_approvals: 0,
      },
    });
    const action = vi
      .spyOn(scannerSupplyChainApi, "candidateAction")
      .mockResolvedValue({
        id: "candidate-secure",
        state: "approved",
      });

    render(
      <CandidatesPanel
        candidateId="candidate-secure"
        onSelectCandidate={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "All mandatory gates passed" },
    });
    const approveButtons = screen.getAllByRole("button", { name: "Approve" });
    fireEvent.click(approveButtons[approveButtons.length - 1]);

    await waitFor(() => {
      expect(action).toHaveBeenCalledWith(
        "candidate-secure",
        "approve",
        {
          reason: "All mandatory gates passed",
          lock_digest: "sha256:lock",
          policy_decision_digest: "sha256:policy",
          evidence_digest: "sha256:receipt",
        },
        undefined,
      );
    });
  });

  it.each([
    ["no_stable_release", "Baseline rebuild"],
    ["maximum_stable_image_age_exceeded", "Age-triggered rebuild"],
    ["policy_forced_weekly_rebuild", "Policy-forced rebuild"],
    ["stable_release_within_maximum_age", "Freshness no-op"],
  ] as const)(
    "explains scheduled freshness reason %s",
    (rebuildReason, expectedLabel) => {
      expect(
        candidateFreshnessPresentation({
          force_rebuild: rebuildReason !== "stable_release_within_maximum_age",
          rebuild_reason: rebuildReason,
          no_op_if_unchanged:
            rebuildReason === "stable_release_within_maximum_age",
        })?.label,
      ).toBe(expectedLabel);
    },
  );

  it("renders aggregate signature and provenance evidence without choosing an arbitrary image", async () => {
    vi.spyOn(scannerSupplyChainApi, "candidate").mockResolvedValue({
      id: "candidate-evidence",
      state: "building",
      definition_commit: "commit-123",
      policy_revision: 7,
      actor: "scheduler",
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:05:00Z",
      selection: {
        force_rebuild: true,
        rebuild_reason: "maximum_stable_image_age_exceeded",
        no_op_if_unchanged: false,
      },
      signature: {
        state: "verified",
        total_count: 9,
        verified_count: 9,
        failed_count: 0,
        pending_count: 0,
        digests: [`sha256:${"a".repeat(64)}`],
      },
      provenance: {
        state: "verified",
        total_count: 8,
        verified_count: 8,
        failed_count: 0,
        pending_count: 0,
        digests: [`sha256:${"b".repeat(64)}`],
      },
    });

    render(
      <CandidatesPanel
        candidateId="candidate-evidence"
        onSelectCandidate={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(await screen.findByText("Age-triggered rebuild")).toBeVisible();
    expect(screen.getByText("Signature evidence")).toBeVisible();
    expect(
      screen.getByText("9 of 9 required evidence steps verified."),
    ).toBeVisible();
    expect(screen.getByText("Provenance evidence")).toBeVisible();
    expect(
      screen.getByText("8 of 8 required evidence steps verified."),
    ).toBeVisible();
  });

  it("records and displays an expiring exception only for an eligible gate", async () => {
    const digest = `sha256:${"a".repeat(64)}`;
    const expiresAt = "2026-08-06T12:00:00Z";
    const baseCandidate = {
      id: "candidate-exception",
      state: "blocked",
      definition_commit: "commit-123",
      lock_digest: "sha256:lock",
      policy_decision: "sha256:policy",
      publication_receipt_digest: "sha256:receipt",
      policy_revision: 7,
      actor: "release-bot",
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:05:00Z",
      gates: [
        {
          name: "vulnerability",
          state: "failed",
          evidence_digest: digest,
        },
        {
          name: "signature",
          state: "failed",
          evidence_digest: digest,
        },
      ],
      separation_of_duties: {
        current_actor_can_approve: true,
        required_approvals: 1,
        valid_approvals: 0,
      },
    };
    const candidate = vi
      .spyOn(scannerSupplyChainApi, "candidate")
      .mockResolvedValueOnce(baseCandidate)
      .mockResolvedValue({
        ...baseCandidate,
        approvals: [
          {
            id: "approval-exception",
            actor: "security-reviewer",
            action: "exception",
            reason: "Bounded vulnerability exposure",
            exception_scope: "vulnerability",
            exception_owner_id: "team-security-platform",
            compensating_control: "Network isolation and active monitoring",
            evidence_digest: digest,
            expires_at: expiresAt,
            created_at: "2026-07-30T12:10:00Z",
          },
        ],
      });
    const createException = vi
      .spyOn(scannerSupplyChainApi, "createCandidateException")
      .mockResolvedValue({
        id: "approval-exception",
        actor: "security-reviewer",
        action: "exception",
        reason: "Bounded vulnerability exposure",
        exception_scope: "vulnerability",
        exception_owner_id: "team-security-platform",
        compensating_control: "Network isolation and active monitoring",
        evidence_digest: digest,
        expires_at: expiresAt,
        created_at: "2026-07-30T12:10:00Z",
      });

    render(
      <CandidatesPanel
        candidateId="candidate-exception"
        onSelectCandidate={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    const exceptionButton = await screen.findByRole("button", {
      name: "Record exception",
    });
    expect(
      screen.getAllByRole("button", { name: "Record exception" }),
    ).toHaveLength(1);
    fireEvent.click(exceptionButton);
    expect(
      screen.getByRole("dialog", {
        name: "Record Vulnerability gate exception?",
      }),
    ).toBeVisible();
    fireEvent.change(screen.getByLabelText("Compensating-control owner"), {
      target: { value: "team-security-platform" },
    });
    fireEvent.change(screen.getByLabelText("Exception reason"), {
      target: { value: "Bounded vulnerability exposure" },
    });
    fireEvent.change(screen.getByLabelText("Compensating control"), {
      target: { value: "Network isolation and active monitoring" },
    });
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Record exception",
      }),
    );

    await waitFor(() => {
      expect(createException).toHaveBeenCalledWith("candidate-exception", {
        gate: "vulnerability",
        owner_id: "team-security-platform",
        reason: "Bounded vulnerability exposure",
        compensating_control: "Network isolation and active monitoring",
        evidence_digest: digest,
        expires_at: expect.any(String),
      });
    });
    const submittedExpiration = new Date(
      createException.mock.calls[0][1].expires_at,
    ).getTime();
    expect(submittedExpiration).toBeGreaterThan(Date.now());
    expect(submittedExpiration).toBeLessThanOrEqual(
      Date.now() + 90 * 24 * 60 * 60 * 1_000,
    );
    await waitFor(() => expect(candidate).toHaveBeenCalledTimes(2));

    fireEvent.mouseDown(screen.getByRole("tab", { name: "Approvals" }), {
      button: 0,
      ctrlKey: false,
    });
    expect(await screen.findByText("team-security-platform")).toBeVisible();
    expect(
      screen.getByText("Network isolation and active monitoring"),
    ).toBeVisible();
    expect(screen.getByText("Vulnerability")).toBeVisible();
  });

  it("loads bounded candidate diffs with truncation, empty, and keyboard-focus states", async () => {
    vi.spyOn(scannerSupplyChainApi, "candidate").mockResolvedValue({
      id: "candidate-diffs",
      state: "awaiting_approval",
      definition_commit: "commit-123",
      lock_digest: "sha256:lock",
      policy_decision: "sha256:policy",
      policy_revision: 7,
      created_at: "2026-07-30T12:00:00Z",
      updated_at: "2026-07-30T12:05:00Z",
    });
    const artifactDiff = vi
      .spyOn(scannerSupplyChainApi, "artifactDiff")
      .mockImplementation(async (owner, id, kind) => {
        if (kind === "lock") {
          return {
            owner_type: owner,
            owner_id: id,
            kind,
            format: "unified",
            available: false,
            content: "",
            truncated: false,
            total_bytes: 0,
            returned_bytes: 0,
            total_lines: 0,
            returned_lines: 0,
          };
        }
        return {
          owner_type: owner,
          owner_id: id,
          kind,
          format: "unified",
          available: true,
          content: "--- scanners.yaml\n+++ scanners.yaml\n+semgrep: 1.2.3\n",
          truncated: true,
          total_bytes: 400_000,
          returned_bytes: 262_144,
          total_lines: 9_000,
          returned_lines: 5_500,
          digest: "sha256:manifest",
          media_type: "text/x-diff",
        };
      });

    render(
      <CandidatesPanel
        candidateId="candidate-diffs"
        onSelectCandidate={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    fireEvent.mouseDown(await screen.findByRole("tab", { name: "Changes" }), {
      button: 0,
      ctrlKey: false,
    });
    expect(
      await screen.findByText("+semgrep: 1.2.3", { exact: false }),
    ).toBeVisible();
    expect(screen.getByText(/Large diff truncated/i)).toBeVisible();
    expect(screen.getByText("No Release Lock Diff Available")).toBeVisible();
    const diffRegion = screen.getByRole("region", {
      name: "Manifest Diff content",
    });
    diffRegion.focus();
    expect(diffRegion).toHaveFocus();
    expect(artifactDiff).toHaveBeenCalledWith(
      "candidate",
      "candidate-diffs",
      "manifest",
    );
    expect(artifactDiff).toHaveBeenCalledWith(
      "candidate",
      "candidate-diffs",
      "lock",
    );
  });

  it("shows independent loading and recoverable error states for diff evidence", async () => {
    vi.spyOn(scannerSupplyChainApi, "artifactDiff").mockImplementation(
      (_owner, _id, kind) => {
        if (kind === "manifest") {
          return new Promise(() => undefined);
        }
        return Promise.reject(
          new Error("Artifact storage is temporarily unavailable"),
        );
      },
    );

    render(
      <ArtifactDiffViewer ownerType="candidate" ownerId="candidate-loading" />,
      { wrapper: wrapper() },
    );

    expect(
      screen.getByRole("status", { name: "Loading manifest diff" }),
    ).toBeVisible();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "Artifact storage is temporarily unavailable",
    );
    expect(
      screen.getByRole("button", { name: "Retry Release Lock Diff" }),
    ).toBeEnabled();
  });

  it("keeps release inventory usable while read-only mode disables release mutations", async () => {
    vi.spyOn(scannerSupplyChainApi, "release").mockResolvedValue({
      id: "release-read-only",
      name: "scanner-set-2026.07.30",
      state: "stable",
      definition_commit: "commit-123",
      lock_digest: "sha256:lock",
      manifest_digest: "sha256:manifest",
      signer_identity: "release@example.com",
      published_at: "2026-07-30T12:00:00Z",
      version: 3,
      tools: [
        {
          tool_key: "semgrep",
          version: "1.130.0",
          source_reference: "pypi:semgrep",
          parser_compatibility: "verified",
        },
      ],
    });
    vi.spyOn(scannerSupplyChainApi, "releases").mockResolvedValue({
      items: [],
    });

    render(
      <ReleasesPanel
        releaseId="release-read-only"
        onSelectRelease={vi.fn()}
        onCompare={vi.fn()}
        onRolloutCreated={vi.fn()}
      />,
      { wrapper: wrapper(readOnlyCapabilities) },
    );

    expect(await screen.findByText("semgrep")).toBeVisible();
    expect(screen.getByText("1.130.0")).toBeVisible();
    expect(screen.getByRole("button", { name: "Verify" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Export" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Promote" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Deprecate" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Revoke" })).toBeDisabled();
  });

  it("loads release diffs from the authenticated changes tab without hiding existing tabs", async () => {
    vi.spyOn(scannerSupplyChainApi, "release").mockResolvedValue({
      id: "release-diffs",
      name: "scanner-set-2026.07.30",
      state: "stable",
      definition_commit: "commit-123",
      lock_digest: "sha256:lock",
      manifest_digest: "sha256:manifest",
      published_at: "2026-07-30T12:00:00Z",
      artifacts: [],
    });
    vi.spyOn(scannerSupplyChainApi, "releases").mockResolvedValue({
      items: [],
    });
    const artifactDiff = vi
      .spyOn(scannerSupplyChainApi, "artifactDiff")
      .mockImplementation(async (owner, id, kind) => ({
        owner_type: owner,
        owner_id: id,
        kind,
        format: "unified",
        available: true,
        content:
          kind === "manifest"
            ? "+manifest release change\n"
            : "+lock release change\n",
        truncated: false,
        total_bytes: 24,
        returned_bytes: 24,
        total_lines: 1,
        returned_lines: 1,
      }));

    render(
      <ReleasesPanel
        releaseId="release-diffs"
        onSelectRelease={vi.fn()}
        onCompare={vi.fn()}
        onRolloutCreated={vi.fn()}
      />,
      { wrapper: wrapper(readOnlyCapabilities) },
    );

    expect(await screen.findByRole("tab", { name: "Tools" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "Artifacts" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "Verification" })).toBeVisible();
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Changes" }), {
      button: 0,
      ctrlKey: false,
    });
    expect(
      await screen.findByText("+manifest release change", { exact: false }),
    ).toBeVisible();
    expect(artifactDiff).toHaveBeenCalledWith(
      "release",
      "release-diffs",
      "manifest",
    );
    expect(artifactDiff).toHaveBeenCalledWith(
      "release",
      "release-diffs",
      "lock",
    );
  });

  it("keeps policy settings inspectable and validatable in read-only mode", async () => {
    const policy = {
      id: "policy-global-1",
      scope: "global",
      revision: 4,
      enabled: true,
      schedule: {
        timezone: "UTC",
        daily_discovery: { frequency: "daily", at: "02:00" },
        weekly_candidate: {
          frequency: "weekly",
          weekday: "Sunday",
          at: "03:00",
        },
      },
      rules: {
        required_gates: ["lock", "artifacts", "platforms", "parser"],
        required_approvals: 1,
      },
      created_by: "release-admin",
      created_at: "2026-07-29T12:00:00Z",
      updated_at: "2026-07-30T12:00:00Z",
    };
    vi.spyOn(scannerSupplyChainApi, "policy").mockResolvedValue(policy);
    vi.spyOn(scannerSupplyChainApi, "policyHistory").mockResolvedValue({
      items: [policy],
    });
    vi.spyOn(scannerSupplyChainApi, "candidates").mockResolvedValue({
      items: [],
    });
    const validate = vi
      .spyOn(scannerSupplyChainApi, "validatePolicy")
      .mockResolvedValue({ valid: true, errors: [], warnings: [] });
    const update = vi.spyOn(scannerSupplyChainApi, "updatePolicy");

    render(<PolicyPanel />, {
      wrapper: wrapper(readOnlyCapabilities),
    });

    expect(await screen.findByText(/Active revision/)).toHaveTextContent("4");
    const timezone = screen.getByLabelText("Timezone");
    expect(timezone).toHaveValue("UTC");
    expect(timezone).toBeDisabled();
    expect(
      screen.getByText(/Policy configuration is read-only/i),
    ).toBeVisible();

    const validateButton = screen.getByRole("button", { name: "Validate" });
    const saveButton = screen.getByRole("button", {
      name: "Save new revision",
    });
    expect(validateButton).toBeEnabled();
    expect(saveButton).toBeDisabled();
    fireEvent.click(validateButton);

    await waitFor(() => {
      expect(validate).toHaveBeenCalledOnce();
    });
    expect(update).not.toHaveBeenCalled();
  });

  it("round-trips, toggles, and reorders every maintenance window", async () => {
    const policy = {
      id: "policy-global-2",
      scope: "global",
      revision: 8,
      enabled: true,
      schedule: {
        timezone: "America/New_York",
        daily_discovery: {
          enabled: true,
          frequency: "daily",
          at: "02:00",
          jitter: "20m",
          catch_up: "6h",
        },
        weekly_candidate: {
          enabled: true,
          frequency: "weekly",
          weekday: "Sunday",
          at: "03:00",
          jitter: "20m",
          catch_up: "48h",
        },
        maintenance_windows: [
          {
            id: "primary",
            name: "Primary window",
            cron: "0 3 * * 0",
            duration: "1h",
          },
          {
            id: "secondary",
            name: "Secondary window",
            cron: "0 5 * * 0",
            duration: "1h",
          },
        ],
      },
      rules: {
        required_gates: ["lock", "artifacts", "platforms", "parser"],
        required_approvals: 1,
      },
      created_by: "release-admin",
      created_at: "2026-07-29T12:00:00Z",
      updated_at: "2026-07-30T12:00:00Z",
    };
    vi.spyOn(scannerSupplyChainApi, "policy").mockResolvedValue(policy);
    vi.spyOn(scannerSupplyChainApi, "policyHistory").mockResolvedValue({
      items: [policy],
    });
    vi.spyOn(scannerSupplyChainApi, "candidates").mockResolvedValue({
      items: [],
    });
    const validate = vi
      .spyOn(scannerSupplyChainApi, "validatePolicy")
      .mockResolvedValue({
        valid: true,
        errors: [],
        warnings: [],
        next_execution: {
          weekly_candidate: "2026-08-02T07:00:00Z",
          maintenance_windows: [
            {
              id: "secondary",
              name: "Secondary window",
              at: "2026-08-02T09:00:00Z",
              duration: "1h0m0s",
            },
          ],
        },
      });

    render(<PolicyPanel />, { wrapper: wrapper() });

    expect(await screen.findAllByLabelText("Name")).toHaveLength(2);
    fireEvent.click(screen.getByLabelText("Enable daily discovery"));
    fireEvent.change(screen.getAllByLabelText("Name")[0], {
      target: { value: "Primary renamed" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Move Secondary window up" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Validate" }));

    await waitFor(() => expect(validate).toHaveBeenCalledOnce());
    const request = validate.mock.calls[0][0];
    expect(request.schedule.daily_discovery?.enabled).toBe(false);
    expect(request.schedule.maintenance_windows).toEqual([
      expect.objectContaining({ id: "secondary", name: "Secondary window" }),
      expect.objectContaining({ id: "primary", name: "Primary renamed" }),
    ]);
    expect(
      await screen.findByText("Next trusted server-clock execution"),
    ).toBeVisible();
    expect(screen.getByText(/Secondary window:/)).toBeVisible();
  });

  it("intersects deployment capabilities with viewer persona access", () => {
    render(<CapabilityBanner />, {
      wrapper: wrapper(stableControlCapabilities, viewerPermissions),
    });
    expect(screen.getByRole("status")).toHaveTextContent(
      "Read-only scanner access",
    );
  });

  it("renders a bounded, semantic operations dashboard in read-only mode", async () => {
    vi.spyOn(scannerSupplyChainApi, "releaseFactoryHealth").mockResolvedValue({
      status: "degraded",
      ready: false,
      database: "ok",
      uptime_ms: 7_500_000,
      components: [
        {
          component: "build",
          enabled: true,
          status: "degraded",
          ready: false,
          last_activity: "2026-07-30T12:00:00Z",
          last_success: "2026-07-30T11:00:00Z",
          stuck_work: { expired_lease: 2 },
          queue_depth: {
            pending: 3,
            retry: 1,
            customer_repository_id: 999,
          },
          run_counts: {
            success: 12,
            error: 2,
            cancelled: 1,
            customer_repository_id: 999,
          },
          result_counts: {
            completed: 10,
            partial: 2,
            failed: 2,
            customer_repository_id: 999,
          },
          average_run_duration_ms: 3_000,
        },
        {
          component: "discovery",
          enabled: true,
          status: "idle",
          ready: true,
          last_activity: "2026-07-30T12:05:00Z",
          last_success: "2026-07-30T12:05:00Z",
        },
        {
          component: "proposal",
          enabled: false,
          status: "disabled",
          ready: true,
        },
      ],
    });
    vi.spyOn(scannerSupplyChainApi, "overview").mockResolvedValue({
      freshness: {
        current: 22,
        updates_available: 2,
        incomplete: 1,
        failed: 0,
        total: 24,
      },
      stable_release: {
        id: "release-stable",
        name: "scanner-set-2026.31.1",
        state: "stable",
        published_at: "2026-07-29T12:00:00Z",
      },
      registry_health: {
        healthy: 1,
        degraded: 0,
        failed: 1,
        total: 2,
      },
      active_rollout: {
        id: "rollout-1",
        target: "production",
        to_release_id: "release-next",
        state: "paused",
        created_at: "2026-07-30T11:00:00Z",
        updated_at: "2026-07-30T12:00:00Z",
        health: {
          samples: 3,
          minimum_samples: 10,
          signature_failures: 1,
        },
      },
      alerts: {
        open_warning: 2,
        open_critical: 1,
        resolved: 5,
      },
      generated_at: "2026-07-30T12:06:00Z",
    });
    render(<OperationsPanel />, {
      wrapper: wrapper(readOnlyCapabilities),
    });

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Release Operations",
      }),
    ).toBeVisible();
    const readinessTable = await screen.findByRole("table", {
      name: "Release-factory component readiness",
    });
    const headers = within(readinessTable).getAllByRole("columnheader");
    expect(headers.map((header) => header.textContent)).toEqual([
      "Component",
      "State",
      "Last Activity",
      "Last Success",
      "Stuck",
      "Remediation",
    ]);
    headers.forEach((header) => expect(header).toHaveAttribute("scope", "col"));
    within(readinessTable)
      .getAllByRole("rowheader")
      .forEach((header) => expect(header).toHaveAttribute("scope", "row"));
    const buildRowHeader = within(readinessTable).getByRole("rowheader", {
      name: "Build: Enabled",
    });
    const buildRow = buildRowHeader.closest("tr");
    expect(buildRow).not.toBeNull();
    const reviewCandidates = within(buildRow as HTMLTableRowElement).getByRole(
      "link",
      { name: "Review Candidates" },
    );
    expect(reviewCandidates).toHaveAttribute(
      "href",
      "/scanners?tab=candidates",
    );

    const readinessRegion = screen.getByRole("region", {
      name: "Release-factory component readiness",
    });
    expect(readinessRegion).toHaveAttribute("tabindex", "0");
    readinessRegion.focus();
    expect(readinessRegion).toHaveFocus();

    expect(screen.getByText("Expired Lease")).toBeVisible();
    expect(screen.getByRole("link", { name: "Review Alerts" })).toHaveAttribute(
      "href",
      "/scanners?tab=notifications&notification_view=alerts",
    );
    const reliability = screen.getByRole("region", {
      name: "Build reliability and durable queues",
    });
    expect(within(reliability).getByText("Completed Builds")).toBeVisible();
    expect(within(reliability).getByText("Partial Builds")).toBeVisible();
    expect(within(reliability).getByText("Failed Builds")).toBeVisible();
    expect(within(reliability).getByText("Queue Backlog")).toBeVisible();
    expect(
      within(reliability).getByText(/4 queued, retrying, or in delivery/i),
    ).toBeVisible();
    within(reliability)
      .getAllByRole("link", { name: "Review Candidates" })
      .forEach((link) =>
        expect(link).toHaveAttribute("href", "/scanners?tab=candidates"),
      );
    expect(
      within(reliability).queryByText(/customer_repository_id/i),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/telemetry are not included/i),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "Refresh release operations dashboard",
      }),
    ).toBeEnabled();
    screen.getAllByRole("button").forEach((button) => {
      expect(button).toHaveAccessibleName();
    });
    screen.getAllByRole("link").forEach((link) => {
      expect(link).toHaveAccessibleName();
      expect(link).toHaveAttribute("href");
    });
    expect(screen.getAllByRole("alert").length).toBeGreaterThanOrEqual(1);
  });

  it("keeps independently available operations data visible when one summary fails", async () => {
    vi.spyOn(scannerSupplyChainApi, "releaseFactoryHealth").mockRejectedValue(
      new Error("Health summary temporarily unavailable"),
    );
    vi.spyOn(scannerSupplyChainApi, "overview").mockResolvedValue({
      freshness: {
        current: 24,
        updates_available: 0,
        incomplete: 0,
        failed: 0,
        total: 24,
      },
    });

    render(<OperationsPanel />, {
      wrapper: wrapper(readOnlyCapabilities),
    });

    expect(await screen.findByText("24/24")).toBeVisible();
    expect(
      screen.getByRole("heading", { name: "Could Not Load Release Factory" }),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Retry Release Factory" }),
    ).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "Retry Factory Health" }),
    ).toBeEnabled();
  });

  it("distinguishes explicit zero reliability counters from absent telemetry", async () => {
    const factory = vi
      .spyOn(scannerSupplyChainApi, "releaseFactoryHealth")
      .mockResolvedValue({
        status: "active",
        ready: true,
        database: "ok",
        uptime_ms: 10_000,
        components: [
          {
            component: "build",
            enabled: true,
            status: "idle",
            ready: true,
            queue_depth: { pending: 0, retry: -2 },
            run_counts: { success: 0, error: 0 },
            result_counts: { completed: 0, partial: 0, failed: 0 },
            average_run_duration_ms: 0,
          },
        ],
      });
    vi.spyOn(scannerSupplyChainApi, "overview").mockResolvedValue({});

    const view = render(<OperationsPanel />, {
      wrapper: wrapper(readOnlyCapabilities),
    });
    const explicit = await screen.findByRole("region", {
      name: "Build reliability and durable queues",
    });
    expect(
      within(explicit).queryByText("Not Reported"),
    ).not.toBeInTheDocument();
    expect(
      within(explicit).getByText(
        "No durable queue backlog is currently reported.",
      ),
    ).toBeVisible();

    view.unmount();
    factory.mockResolvedValue({
      status: "active",
      ready: true,
      database: "ok",
      uptime_ms: 10_000,
      components: [
        {
          component: "build",
          enabled: true,
          status: "idle",
          ready: true,
        },
      ],
    });
    render(<OperationsPanel />, {
      wrapper: wrapper(readOnlyCapabilities),
    });
    const absent = await screen.findByRole("region", {
      name: "Build reliability and durable queues",
    });
    expect(within(absent).getAllByText("Not Reported")).toHaveLength(4);
    expect(
      within(absent).getByText(
        "Queue telemetry has not been reported by this instance.",
      ),
    ).toBeVisible();
  });

  it("announces operations loading and explicit empty states", async () => {
    const factory = vi
      .spyOn(scannerSupplyChainApi, "releaseFactoryHealth")
      .mockImplementation(() => new Promise(() => undefined));
    const overview = vi
      .spyOn(scannerSupplyChainApi, "overview")
      .mockImplementation(() => new Promise(() => undefined));

    const view = render(<OperationsPanel />, {
      wrapper: wrapper(readOnlyCapabilities),
    });

    expect(
      screen.getByRole("status", { name: "Loading release factory" }),
    ).toHaveTextContent("Loading release factory…");
    expect(
      screen.getByRole("status", { name: "Loading supply-chain health" }),
    ).toHaveTextContent("Loading supply-chain health…");

    view.unmount();
    factory.mockResolvedValue({
      status: "disabled",
      ready: true,
      database: "ok",
      uptime_ms: 1_000,
      components: [],
    });
    overview.mockResolvedValue({});
    render(<OperationsPanel />, {
      wrapper: wrapper(readOnlyCapabilities),
    });

    expect(await screen.findByText("No Release Factory Data")).toBeVisible();
    expect(screen.getByText("No Supply-Chain Health Data")).toBeVisible();
  });
});
