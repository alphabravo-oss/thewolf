import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Section } from "./section";

describe("Section", () => {
  it("starts collapsed and toggles aria-expanded and the body", () => {
    render(<Section title="Advanced" summary="3 options"><p>secret body</p></Section>);
    const trigger = screen.getByRole("button", { name: /Advanced/ });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("secret body")).not.toBeVisible();

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("secret body")).toBeVisible();
    expect(trigger).toHaveAttribute("aria-controls", screen.getByText("secret body").parentElement!.id);

    fireEvent.click(trigger);
    expect(screen.getByText("secret body")).not.toBeVisible();
  });

  it("honours defaultOpen and keeps children mounted while collapsed", () => {
    render(<Section title="Open one" defaultOpen><input aria-label="kept" defaultValue="typed" /></Section>);
    const trigger = screen.getByRole("button", { name: "Open one" });
    expect(screen.getByLabelText("kept")).toBeVisible();
    fireEvent.click(trigger);
    expect(screen.getByLabelText("kept", { selector: "input" })).toHaveValue("typed");
  });

  it("can be controlled and exposes a named heading region", () => {
    const seen: boolean[] = [];
    const { rerender } = render(<Section title="Work" headingLevel={2} open={false} onOpenChange={(o) => seen.push(o)}><p>body</p></Section>);
    expect(screen.getByRole("heading", { name: "Work", level: 2 })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Work" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Work" }));
    expect(seen).toEqual([true]);
    expect(screen.getByText("body")).not.toBeVisible();
    rerender(<Section title="Work" headingLevel={2} open onOpenChange={() => {}}><p>body</p></Section>);
    expect(screen.getByText("body")).toBeVisible();
  });
});
