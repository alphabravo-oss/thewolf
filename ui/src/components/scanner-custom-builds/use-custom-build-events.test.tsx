import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useCustomBuildEvents } from "./use-custom-build-events";

function Probe() {
  const stream = useCustomBuildEvents("build-fallback", false);
  return <output aria-label="stream state">{stream.state}</output>;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("custom build event reconnect", () => {
  it("falls back to status polling semantics after repeated stream failures", async () => {
    vi.useFakeTimers();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new Error("stream unavailable")),
    );

    render(<Probe />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByLabelText("stream state")).toHaveTextContent(
      "reconnecting",
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(screen.getByLabelText("stream state")).toHaveTextContent("polling");
    expect(fetch).toHaveBeenCalledTimes(3);
  });
});
