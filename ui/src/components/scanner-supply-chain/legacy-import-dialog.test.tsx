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
import {
  deriveLegacyConfiguredImages,
  LegacyImportDialog,
} from "./legacy-import-dialog";
import { ScannerReleaseCapabilitiesBoundary } from "./capabilities";
import { ReleasesPanel } from "./releases-panel";
import {
  scannerSupplyChainApi,
  type LegacyConfigurationSnapshot,
  type ScannerReleaseCapabilities,
} from "@/lib/scanner-supply-chain";

const defaultDigest = `sha256:${"a".repeat(64)}`;
const overrideDigest = `sha256:${"b".repeat(64)}`;
const upstreamDigest = `sha256:${"c".repeat(64)}`;

const configuration: LegacyConfigurationSnapshot = {
  config: {
    image: "registry.example/wolf-scanners:2.0.0",
    image_overrides: {
      semgrep: `registry.example/wolf-semgrep@${overrideDigest}`,
    },
  },
  tools: [
    {
      name: "trivy",
      display_name: "Trivy",
      integration_tier: "upstream",
      configured_image: "aquasec/trivy:0.64.1",
    },
    {
      name: "semgrep",
      display_name: "Semgrep",
      integration_tier: "default",
      configured_image: "registry.example/wolf-scanners:2.0.0",
    },
  ],
};

function queryWrapper(
  capabilities?: ScannerReleaseCapabilities,
) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return function QueryWrapper({ children }: PropsWithChildren) {
    const content = (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    return capabilities ? (
      <ScannerReleaseCapabilitiesBoundary capabilities={capabilities}>
        {content}
      </ScannerReleaseCapabilitiesBoundary>
    ) : (
      content
    );
  };
}

describe("legacy scanner configuration import", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("derives the exact backend image keys without treating default tools as upstream", () => {
    expect(deriveLegacyConfiguredImages(configuration)).toEqual([
      {
        key: "default",
        reference: "registry.example/wolf-scanners:2.0.0",
        source: "default",
        embeddedDigest: undefined,
        invalidEmbeddedDigest: false,
      },
      {
        key: "upstream-trivy",
        reference: "aquasec/trivy:0.64.1",
        source: "upstream",
        embeddedDigest: undefined,
        invalidEmbeddedDigest: false,
      },
      {
        key: "wolf-semgrep",
        reference: `registry.example/wolf-semgrep@${overrideDigest}`,
        source: "override",
        embeddedDigest: overrideDigest,
        invalidEmbeddedDigest: false,
      },
    ]);
  });

  it("previews limitations, validates tagged digests, and reuses its idempotency key after an error", async () => {
    vi.spyOn(
      scannerSupplyChainApi,
      "legacyConfiguration",
    ).mockResolvedValue(configuration);
    const imported = {
      release: {
        id: "legacy-release-1",
        name: "legacy-config-aaaaaaaaaaaa",
        state: "published",
        manifest_digest: defaultDigest,
        signer_identity: "legacy-unverified",
        published_at: "2026-07-30T12:00:00Z",
        legacy: true,
        imported: true,
        protected: true,
        rollback_eligible: false,
        retention_class: "legacy",
      },
      images: [
        {
          image_key: "default",
          repository: "registry.example/wolf-scanners:2.0.0",
          digest: defaultDigest,
          signature_status: "legacy_unverified",
        },
      ],
      created: true,
      provenance_limitations: [
        "image signatures were not verified by the managed release pipeline",
        "SBOM and build provenance are unavailable",
        "the snapshot is historical evidence and is not rollout eligible",
      ],
      runtime_assignments_changed: false as const,
    };
    const importLegacy = vi
      .spyOn(scannerSupplyChainApi, "importLegacyConfiguration")
      .mockRejectedValueOnce(new Error("temporary database outage"))
      .mockResolvedValueOnce(imported);

    render(<LegacyImportDialog open onOpenChange={vi.fn()} />, {
      wrapper: queryWrapper(),
    });

    const dialog = screen.getByRole("dialog", {
      name: "Import legacy configuration snapshot",
    });
    expect(
      within(dialog).getByText(/Evidence-only import with permanent limitations/i),
    ).toBeVisible();
    expect(
      within(dialog).getByText(/does not change the desired release/i),
    ).toBeVisible();
    expect(
      await within(dialog).findByRole("heading", {
        name: "Configuration preview",
      }),
    ).toBeVisible();
    expect(within(dialog).getByText("wolf-semgrep")).toBeVisible();
    expect(within(dialog).getByText(overrideDigest)).toBeVisible();

    fireEvent.change(within(dialog).getByLabelText("Digest for default"), {
      target: { value: defaultDigest },
    });
    fireEvent.change(
      within(dialog).getByLabelText("Digest for upstream-trivy"),
      {
        target: { value: upstreamDigest },
      },
    );
    fireEvent.change(within(dialog).getByLabelText("Audit reason"), {
      target: { value: "Preserve deployment state before managed rollout" },
    });
    fireEvent.change(
      within(dialog).getByLabelText(/Type IMPORT LEGACY SNAPSHOT/i),
      {
        target: { value: "IMPORT LEGACY SNAPSHOT" },
      },
    );

    const submit = within(dialog).getByRole("button", {
      name: "Import evidence snapshot",
    });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "temporary database outage",
    );
    fireEvent.click(submit);

    await waitFor(() => expect(importLegacy).toHaveBeenCalledTimes(2));
    expect(importLegacy.mock.calls[0][0]).toEqual({
      reason: "Preserve deployment state before managed rollout",
      resolved_digests: {
        default: defaultDigest,
        "upstream-trivy": upstreamDigest,
      },
    });
    expect(importLegacy.mock.calls[0][1]).toMatch(/^wolf-ui-/);
    expect(importLegacy.mock.calls[1][1]).toBe(importLegacy.mock.calls[0][1]);

    expect(
      await within(dialog).findByRole("heading", {
        name: "Legacy snapshot imported",
      }),
    ).toBeVisible();
    expect(within(dialog).getByText(/Runtime unchanged/)).toBeVisible();
    expect(within(dialog).getByText(/Not rollback eligible/)).toBeVisible();
    expect(
      within(dialog).getByRole("link", { name: "Open imported release" }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=releases&release=legacy-release-1",
    );
  });

  it("keeps legacy import unavailable in observe-only mode", async () => {
    vi.spyOn(scannerSupplyChainApi, "releases").mockResolvedValue({ items: [] });
    render(
      <ReleasesPanel
        onSelectRelease={vi.fn()}
        onCompare={vi.fn()}
        onRolloutCreated={vi.fn()}
      />,
      {
        wrapper: queryWrapper({
          mode: "read_only",
          read: true,
          candidates: false,
          canary: false,
          stable_control: false,
        }),
      },
    );

    expect(
      await screen.findByRole("button", { name: "Import legacy snapshot" }),
    ).toBeDisabled();
  });
});
