import { describe, expect, it } from "vitest";
import { parseFrameworks } from "./parse-frameworks";

describe("parseFrameworks", () => {
  it("parses a JSON array of strings", () => {
    expect(parseFrameworks(`["react","vite","tailwindcss"]`)).toEqual([
      "react", "vite", "tailwindcss",
    ]);
  });

  it("returns [] for an empty string", () => {
    expect(parseFrameworks("")).toEqual([]);
  });

  it("returns [] for null/undefined", () => {
    expect(parseFrameworks(null as unknown as string)).toEqual([]);
    expect(parseFrameworks(undefined as unknown as string)).toEqual([]);
  });

  it("falls back to comma-split for legacy non-JSON payloads", () => {
    expect(parseFrameworks("react, vite,  tailwindcss")).toEqual([
      "react", "vite", "tailwindcss",
    ]);
  });

  it("ignores non-string entries inside the JSON array", () => {
    expect(parseFrameworks(`["react", 42, null, "vite"]`)).toEqual([
      "react", "vite",
    ]);
  });
});
