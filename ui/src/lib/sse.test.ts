import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useSSE } from "./sse";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  close = vi.fn();
  constructor(public url: string) { FakeEventSource.instances.push(this); }
}
afterEach(() => { vi.unstubAllGlobals(); FakeEventSource.instances = []; });

it("retains the native replay cursor on errors and closes on disable/path change", () => {
  vi.stubGlobal("EventSource", FakeEventSource);
  const onEvent = vi.fn();
  const { result, rerender, unmount } = renderHook(({ path, enabled }) => useSSE({ path, enabled, onEvent }), {
    initialProps: { path: "/scans/one/stream", enabled: true },
  });
  const first = FakeEventSource.instances[0];
  act(() => first.onopen?.());
  expect(result.current.connected).toBe(true);
  act(() => first.onerror?.(new Event("error")));
  expect(result.current.connected).toBe(false);
  expect(first.close).not.toHaveBeenCalled();
  expect(FakeEventSource.instances).toHaveLength(1);
  act(() => { first.onopen?.(); first.onmessage?.({ data: '{"type":"scan_progress"}' }); });
  expect(onEvent).toHaveBeenCalledExactlyOnceWith({ type: "scan_progress" });
  rerender({ path: "/scans/two/stream", enabled: true });
  expect(first.close).toHaveBeenCalledOnce();
  expect(first.onmessage).toBeNull();
  const second = FakeEventSource.instances[1];
  rerender({ path: "/scans/two/stream", enabled: false });
  expect(second.close).toHaveBeenCalledOnce();
  expect(result.current.connected).toBe(false);
  unmount();
});
