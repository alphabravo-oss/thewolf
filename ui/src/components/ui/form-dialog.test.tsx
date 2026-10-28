import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FieldError, FormDialog } from "./form-dialog";

function setup(props: Partial<React.ComponentProps<typeof FormDialog>> = {}) {
  const onOpenChange = vi.fn();
  const onSubmit = vi.fn();
  render(
    <FormDialog open onOpenChange={onOpenChange} onSubmit={onSubmit} title="Add secret" description="Stored encrypted." submitLabel="Add" {...props}>
      <label>
        Name
        <input name="name" />
      </label>
      <FieldError id="name-err">Name is required</FieldError>
    </FormDialog>,
  );
  return { onOpenChange, onSubmit };
}

describe("FormDialog", () => {
  it("renders title, description and inline field error, and submits via the primary button", () => {
    const { onSubmit } = setup();
    expect(screen.getByRole("dialog", { name: "Add secret" })).toBeInTheDocument();
    expect(screen.getByText("Stored encrypted.")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Name is required");
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(onSubmit).toHaveBeenCalledOnce();
  });

  it("closes straight away when clean", () => {
    const { onOpenChange } = setup();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("asks before discarding when dirty, and Esc takes the same path", () => {
    const { onOpenChange } = setup({ dirty: true });
    fireEvent.keyDown(screen.getByRole("dialog", { name: "Add secret" }), { key: "Escape" });
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog", { name: "Discard changes?" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Keep editing" }));
    expect(onOpenChange).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("locks the form and blocks closing while pending", () => {
    const { onOpenChange } = setup({ pending: true, dirty: true });
    expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(screen.getByRole("textbox", { name: "Name" })).toBeDisabled();
    fireEvent.keyDown(screen.getByRole("dialog", { name: "Add secret" }), { key: "Escape" });
    expect(onOpenChange).not.toHaveBeenCalled();
  });
});
