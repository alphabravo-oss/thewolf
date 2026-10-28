import { describe, expect, it } from "vitest";
import { gaugeColor, gaugeTextColor } from "./utils";

describe("gauge colours (higher is better)", () => {
  it("treats full coverage as healthy, partial as warning, none as error", () => {
    expect([100, 60, 0].map(gaugeColor)).toEqual(["bg-status-success", "bg-status-warning", "bg-status-error"]);
    expect([100, 60, 0].map(gaugeTextColor)).toEqual(["text-foreground", "text-status-warning", "text-status-error"]);
  });
});
