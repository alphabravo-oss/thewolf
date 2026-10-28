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
});
