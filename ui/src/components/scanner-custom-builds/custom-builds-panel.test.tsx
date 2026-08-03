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
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  scannerCustomBuildApi,
  type CustomBuildInventory,
} from "@/lib/scanner-custom-build";
import { CustomBuildCreateDialog } from "./custom-build-create-dialog";
import { CustomBuildsPanel } from "./custom-builds-panel";
import { parseCustomBuildFrame } from "./use-custom-build-events";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("./use-custom-build-events", async (importOriginal) => {
  const original =
    await importOriginal<typeof import("./use-custom-build-events")>();
  return {
    ...original,
    useCustomBuildEvents: () => ({
      state: "stopped",
      logs: [{ sequence: 1, variant: "default", line: "build completed" }],
    }),
  };
});

const partialInventory: CustomBuildInventory = {
  build: {
    id: "custom-build-partial",
    variants: ["default", "jvm", "rust", "codeql"],
    push: false,
    platforms: ["linux/amd64"],
    state: "partial",
    actor: "security.admin@example.test",
    reason: "Weekly tool refresh",
    attempt: 1,
    max_attempts: 3,
    error_class: "variant_build_failed",
    version: 4,
    created_at: "2026-07-30T12:00:00Z",
    updated_at: "2026-07-30T12:05:00Z",
  },
  variants: [
    {
      id: "variant-default",
      build_id: "custom-build-partial",
      variant: "default",
      ordinal: 0,
      state: "completed",
      refs: ["docker.io/wolf/default:2.0.1"],
      digest: "sha256:default",
      loaded_locally: true,
      pushed: false,
      version: 2,
    },
    {
      id: "variant-codeql",
      build_id: "custom-build-partial",
      variant: "codeql",
      ordinal: 3,
      state: "failed",
      refs: [],
      loaded_locally: false,
      pushed: false,
      error_class: "buildx_unavailable",
      version: 2,
    },
  ],
  etag: '"4"',
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
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}

describe("custom builds workspace", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(scannerCustomBuildApi, "detail").mockResolvedValue(
      partialInventory,
    );
  });

  afterEach(() => cleanup());

  it("shows partial per-variant evidence and never renders raw transport fields", async () => {
    render(
      <CustomBuildsPanel
        buildId="custom-build-partial"
        onSelectBuild={vi.fn()}
        onStateChange={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(
      await screen.findByRole("heading", { name: "Custom build operation" }),
    ).toBeVisible();
    expect(screen.getByText("Some variants did not complete")).toBeVisible();
    expect(screen.getByText("sha256:default")).toBeVisible();
    expect(screen.getAllByText("CodeQL").length).toBeGreaterThan(0);
    expect(
      screen.getByRole("log", { name: "Custom build log" }),
    ).toHaveTextContent("build completed");
    expect(document.body).not.toHaveTextContent("error_detail");
    expect(document.body).not.toHaveTextContent("idempotency");
    expect(document.body).not.toHaveTextContent("secret_reference");
  });

  it("requires reason and exact build ID for retry with If-Match data", async () => {
    const retry = vi.spyOn(scannerCustomBuildApi, "retry").mockResolvedValue({
      ...partialInventory.build,
      state: "queued",
      version: 5,
    });
    render(
      <CustomBuildsPanel
        buildId="custom-build-partial"
        onSelectBuild={vi.fn()}
        onStateChange={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    const dialog = screen.getByRole("dialog", { name: "Retry custom build" });
    const submit = within(dialog).getByRole("button", { name: "Retry build" });
    fireEvent.change(within(dialog).getByLabelText("Reason"), {
      target: { value: "Worker capacity restored" },
    });
    fireEvent.change(
      within(dialog).getByLabelText(/Type custom-build-partial to confirm/),
      { target: { value: "custom-build-partia" } },
    );
    expect(submit).toBeDisabled();
    fireEvent.change(
      within(dialog).getByLabelText(/Type custom-build-partial to confirm/),
      { target: { value: "custom-build-partial" } },
    );
    fireEvent.click(submit);

    await waitFor(() =>
      expect(retry).toHaveBeenCalledWith(
        "custom-build-partial",
        "Worker capacity restored",
        '"4"',
      ),
    );
  });

  it("explains and blocks push-all before any request", async () => {
    const create = vi.spyOn(scannerCustomBuildApi, "create");
    render(
      <CustomBuildCreateDialog
        open
        onOpenChange={vi.fn()}
        defaults={{ variant: "all", push: true }}
        onAccepted={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(
      screen.getByText(
        /Build all cannot be pushed because CodeQL is local-only/,
      ),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Queue build" })).toBeDisabled();
    expect(create).not.toHaveBeenCalled();
  });

  it("queues local CodeQL with a reason and no secret or namespace", async () => {
    const accepted = vi.fn();
    const create = vi.spyOn(scannerCustomBuildApi, "create").mockResolvedValue({
      id: "custom-build-codeql",
      state: "queued",
      status_url: "/api/v1/scanners/custom-builds/custom-build-codeql",
    });
    render(
      <CustomBuildCreateDialog
        open
        onOpenChange={vi.fn()}
        defaults={{ variant: "codeql", push: false }}
        onAccepted={accepted}
      />,
      { wrapper: wrapper() },
    );

    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "Refresh local CodeQL tools" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Queue build" }));

    await waitFor(() =>
      expect(create).toHaveBeenCalledWith({
        variants: ["codeql"],
        push: false,
        platforms: ["linux/amd64"],
        namespace: undefined,
        reason: "Refresh local CodeQL tools",
      }),
    );
    expect(accepted).toHaveBeenCalledWith(
      expect.objectContaining({ id: "custom-build-codeql" }),
    );
  });

  it("renders an actionable unauthorized state without backend detail", async () => {
    vi.spyOn(scannerCustomBuildApi, "list").mockRejectedValue(
      new Error("token=dckr_pat_raw_backend"),
    );
    render(
      <CustomBuildsPanel onSelectBuild={vi.fn()} onStateChange={vi.fn()} />,
      { wrapper: wrapper() },
    );

    expect(
      await screen.findByText(/Verify scanner build read permission/),
    ).toBeVisible();
    expect(document.body).not.toHaveTextContent("dckr_pat_raw_backend");
  });
});

describe("custom build SSE parsing", () => {
  it("admits bounded log frames and ignores terminal JSON payloads", () => {
    expect(
      parseCustomBuildFrame(
        "id: 12\nevent: log\ndata: [rust] cargo build\u0000 complete",
      ),
    ).toEqual({
      kind: "log",
      sequence: 12,
      entry: {
        sequence: 12,
        variant: "rust",
        line: "cargo build complete",
      },
    });
    expect(
      parseCustomBuildFrame(
        "id: 13\nevent: log\ndata: [rust] token=dckr_pat_never_render",
      ),
    ).toEqual({
      kind: "log",
      sequence: 13,
      entry: {
        sequence: 13,
        variant: "rust",
        line: "token=[REDACTED]",
      },
    });
    expect(
      parseCustomBuildFrame(
        'id: 4001\nevent: error\ndata: {"error_detail":"never expose"}',
      ),
    ).toEqual({ kind: "terminal", sequence: 4001 });
  });
});
