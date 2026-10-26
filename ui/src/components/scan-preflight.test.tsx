import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  admin: vi.fn(),
  get: vi.fn(),
  post: vi.fn(),
  navigate: vi.fn(),
  success: vi.fn(),
  warning: vi.fn(),
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

vi.mock("@/lib/me", () => ({ useIsAdmin: mocks.admin }));

vi.mock("sonner", () => ({
  toast: {
    success: mocks.success,
    warning: mocks.warning,
    error: mocks.error,
  },
}));

import { useScanWithPreflight } from "@/components/scan-preflight";

afterEach(cleanup);

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
    vi.resetAllMocks();
    mocks.admin.mockReturnValue(true);
    mocks.get.mockResolvedValue({ data: { docker_image_management: true } });
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
    expect(mocks.post).toHaveBeenCalledWith("/scans", { repo_id: "repo-1" }, { headers: { "Idempotency-Key": expect.any(String) } });
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
      ["/scans", params, { headers: { "Idempotency-Key": expect.any(String) } }],
    ]);
  });

  it("does not submit when runtime readiness is unavailable", async () => {
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

    expect(mocks.post).not.toHaveBeenCalled();
    expect(result.current.busy).toBe(true);
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
    await screen.findByRole("button", { name: /^Download \d+ images? and scan$/ });
    fireEvent.click(screen.getByRole("button", { name: /^Download \d+ images? and scan$/ }));

    await screen.findByText("Some scanner images could not be pulled");
    expect(
      screen.getByRole("button", { name: /^Retry \d+ downloads and scan$/ }),
    ).toBeTruthy();
    await waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(2));
    expect(mocks.post.mock.calls.some(([path]) => path === "/scans")).toBe(
      false,
    );
  });

  it("ignores overlapping launches and snapshots the selected intent", async () => {
    mocks.post.mockResolvedValueOnce({ data: { missing: [] } }).mockResolvedValueOnce({ data: { id: "one" } });
    const { result } = renderHook(() => useScanWithPreflight(), { wrapper: wrapper() });
    const params = { repo_id: "original", branch: "main", tools: ["gokart"] };
    await act(async () => {
      const first = result.current.launch(params);
      params.branch = "changed";
      params.tools.push("semgrep");
      await result.current.launch({ repo_id: "second" });
      await first;
    });
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1]).toEqual({ repo_id: "original", branch: "main", tools: ["gokart"] });
  });

  it.each([new Error("preflight unavailable"), null, { missing: null }, { missing: [{}] }])("fails closed on readiness errors or malformed responses: %s", async (response) => {
    if (response instanceof Error) mocks.post.mockRejectedValueOnce(response);
    else mocks.post.mockResolvedValueOnce({ data: response });
    render(<PreflightHarness />, { wrapper: wrapper() });
    fireEvent.click(screen.getByRole("button", { name: "Start test scan" }));
    await screen.findByRole("button", { name: "Retry readiness check" });
    expect(mocks.post.mock.calls.map(([path]) => path)).toEqual(["/scans/preflight"]);
  });

  it("retries ambiguous submission with the same idempotency key", async () => {
    mocks.post.mockResolvedValueOnce({ data: { missing: [] } })
      .mockRejectedValueOnce(new Error("response lost"))
      .mockResolvedValueOnce({ data: { id: "recovered" } });
    render(<PreflightHarness />, { wrapper: wrapper() });
    fireEvent.click(screen.getByRole("button", { name: "Start test scan" }));
    const retry = await screen.findByRole("button", { name: "Retry scan submission" });
    fireEvent.click(retry);
    await waitFor(() => expect(mocks.navigate).toHaveBeenCalledTimes(1));
    const submissions = mocks.post.mock.calls.filter(([path]) => path === "/scans");
    expect(submissions).toHaveLength(2);
    expect(submissions[0]).toEqual(submissions[1]);
    expect(submissions[0][2].headers["Idempotency-Key"]).toBeTruthy();
  });

  it("downloads each image once, retries only failures, and rechecks readiness", async () => {
    let checks = 0, attemptsB = 0;
    mocks.post.mockImplementation(async (path, body) => {
      if (path === "/scans/preflight") return { data: { missing: checks++ === 0 ? [
        { tool: "first", image: "image-a" }, { tool: "shared", image: "image-a" }, { tool: "second", image: "image-b" },
      ] : [] } };
      if (path === "/scanners/images/pull" && body.image === "image-b" && attemptsB++ === 0) throw new Error("offline");
      return { data: { id: "ready" } };
    });
    render(<PreflightHarness />, { wrapper: wrapper() });
    fireEvent.click(screen.getByRole("button", { name: "Start test scan" }));
    const pull = await screen.findByRole("button", { name: /^Download \d+ images? and scan$/ });
    act(() => { fireEvent.click(pull); fireEvent.click(pull); });
    fireEvent.click(await screen.findByRole("button", { name: /^Retry \d+ downloads and scan$/ }));
    await waitFor(() => expect(mocks.navigate).toHaveBeenCalledTimes(1));
    expect(mocks.post.mock.calls.filter(([path]) => path === "/scanners/images/pull").map(([, body]) => body.image)).toEqual(["image-a", "image-b", "image-b"]);
    expect(mocks.post.mock.calls.filter(([path]) => path === "/scans/preflight")).toHaveLength(2);
    expect(mocks.post.mock.calls.filter(([path]) => path === "/scans")).toHaveLength(1);
  });

  it("explains missing permissions without offering an admin-only download", async () => {
    mocks.admin.mockReturnValue(false);
    mocks.post.mockResolvedValueOnce({ data: { missing: [{ tool: "gokart", image: "scanner" }] } });
    render(<PreflightHarness />, { wrapper: wrapper() });
    fireEvent.click(screen.getByRole("button", { name: "Start test scan" }));
    await screen.findByText(/An administrator must download/);
    expect(screen.queryByRole("button", { name: /^Download \d+ images? and scan$/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Scan without them" })).toBeTruthy();
  });
});
