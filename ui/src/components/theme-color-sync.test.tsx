import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeColorSync } from "./theme-color-sync";

const { mockUseTheme } = vi.hoisted(() => ({
  mockUseTheme: vi.fn(),
}));

vi.mock("next-themes", () => ({
  useTheme: mockUseTheme,
}));

describe("ThemeColorSync", () => {
  beforeEach(() => {
    document.head.innerHTML =
      '<meta name="theme-color" content="#0a0c10" data-theme-color-sync="true">';
  });

  afterEach(() => {
    cleanup();
    document.head.innerHTML = "";
    vi.clearAllMocks();
  });

  it("keeps browser chrome synchronized with explicit light and dark themes", () => {
    mockUseTheme.mockReturnValue({ resolvedTheme: "light" });
    const view = render(<ThemeColorSync />);
    const themeColor = document.querySelector('meta[name="theme-color"]');
    expect(themeColor).toHaveAttribute("content", "#f8f9fb");

    mockUseTheme.mockReturnValue({ resolvedTheme: "dark" });
    view.rerender(<ThemeColorSync />);
    expect(themeColor).toHaveAttribute("content", "#0a0c10");
  });

  it("preserves the safe document default while the theme is unresolved", () => {
    mockUseTheme.mockReturnValue({ resolvedTheme: undefined });
    render(<ThemeColorSync />);
    expect(document.querySelector('meta[name="theme-color"]')).toHaveAttribute(
      "content",
      "#0a0c10",
    );
  });
});
