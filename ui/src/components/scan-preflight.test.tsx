import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  navigate: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  api: {
    get: mocks.get,
    post: mocks.post,
  },
}));

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => mocks.navigate,
}));

vi.mock("sonner", () => ({
  toast: {
    success: mocks.success,
    error: mocks.error,
  },
}));

import { useScanWithPreflight } from "./scan-preflight";

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

function PreflightHarness() {
  const flow = useScanWithPreflight();
  return (
    <>
      <button
        type="button"
        onClick={() => flow.launch({ repo_id: "repo-pull" })}
      >
        Start test scan
      </button>
      {flow.dialog}
    </>
  );
}

describe("useScanWithPreflight runtime compatibility", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("submits directly when Kubernetes manages scanner images", async () => {
    mocks.get.mockResolvedValueOnce({
      data: {
        scan_runtime: "kubernetes",
        queue_execution: true,
        docker_image_management: false,
        scanner_jobs: true,
        durable_events: true,
        tool_cancellation: true,
      },
    });
    mocks.post.mockResolvedValueOnce({ data: { id: "scan-k8s" } });

    const { result } = renderHook(() => useScanWithPreflight(), {
      wrapper: wrapper(),
    });
    await act(async () => {
      await result.current.launch({ repo_id: "repo-1" });
    });

    expect(mocks.get).toHaveBeenCalledWith("/scanners/runtime-capabilities");
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith("/scans", { repo_id: "repo-1" });
    expect(mocks.navigate).toHaveBeenCalledWith({
      to: "/scans/$scanId",
      params: { scanId: "scan-k8s" },
    });
  });

  it("preserves Docker preflight before submitting a scan", async () => {
    mocks.get.mockResolvedValueOnce({
      data: {
        scan_runtime: "docker",
        queue_execution: true,
        docker_image_management: true,
        scanner_jobs: false,
        durable_events: true,
        tool_cancellation: true,
      },
    });
    mocks.post
      .mockResolvedValueOnce({ data: { missing: [] } })
      .mockResolvedValueOnce({ data: { id: "scan-docker" } });

    const params = { repo_id: "repo-2", tools: ["semgrep"] };
    const { result } = renderHook(() => useScanWithPreflight(), {
      wrapper: wrapper(),
    });
    await act(async () => {
      await result.current.launch(params);
    });

    expect(mocks.post.mock.calls).toEqual([
      ["/scans/preflight", params],
      ["/scans", params],
    ]);
  });

  it("keeps scan submission working during a rolling upgrade", async () => {
    mocks.get.mockRejectedValueOnce(
      new Error("capabilities endpoint unavailable"),
    );
    mocks.post.mockResolvedValueOnce({ data: { id: "scan-compatible" } });

    const { result } = renderHook(() => useScanWithPreflight(), {
      wrapper: wrapper(),
    });
    await act(async () => {
      await result.current.launch({ repo_id: "repo-3" });
    });

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith("/scans", { repo_id: "repo-3" });
  });

  it("does not start a scan when an image pull fails", async () => {
    mocks.get.mockResolvedValueOnce({
      data: {
        scan_runtime: "docker",
        queue_execution: true,
        docker_image_management: true,
        scanner_jobs: false,
        durable_events: true,
        tool_cancellation: true,
      },
    });
    mocks.post
      .mockResolvedValueOnce({
        data: {
          missing: [
            { tool: "semgrep", image: "registry.example/semgrep@sha256:abc" },
          ],
        },
      })
      .mockRejectedValueOnce(new Error("registry unavailable"));

    render(<PreflightHarness />, { wrapper: wrapper() });
    fireEvent.click(screen.getByRole("button", { name: "Start test scan" }));
    await screen.findByRole("button", { name: "Pull & scan" });
    fireEvent.click(screen.getByRole("button", { name: "Pull & scan" }));

    await screen.findByText("Some scanner images could not be pulled");
    expect(
      screen.getByRole("button", { name: "Retry failed pulls" }),
    ).toBeTruthy();
    await waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(2));
    expect(mocks.post.mock.calls.some(([path]) => path === "/scans")).toBe(
      false,
    );
  });
});
