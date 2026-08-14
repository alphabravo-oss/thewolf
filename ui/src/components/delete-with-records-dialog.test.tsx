import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DeleteWithRecordsDialog } from "./delete-with-records-dialog";

describe("DeleteWithRecordsDialog", () => {
  it("does not delete a repo with scans until records are checked", () => {
    const onConfirm = vi.fn();
    render(
      <DeleteWithRecordsDialog
        open
        onOpenChange={() => undefined}
        kind="repo"
        name="acme"
        recordCount={3}
        onConfirm={onConfirm}
      />,
    );

    expect(screen.getByRole("heading", { name: /Delete repo/ })).toHaveTextContent(
      "acme",
    );
    expect(screen.getAllByText(/Also remove records/).length).toBeGreaterThan(0);
    expect(screen.getByText(/3 scans/)).toBeInTheDocument();

    const confirm = screen.getByRole("button", {
      name: "Delete and remove records",
    });
    expect(confirm).toBeDisabled();
    fireEvent.click(confirm);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("deletes a repo with scans only after the records box is checked", () => {
    const onConfirm = vi.fn();
    render(
      <DeleteWithRecordsDialog
        open
        onOpenChange={() => undefined}
        kind="repo"
        name="acme"
        recordCount={3}
        onConfirm={onConfirm}
      />,
    );

    const boxes = screen.getAllByRole("checkbox");
    fireEvent.click(boxes[boxes.length - 1]);
    fireEvent.click(
      screen.getByRole("button", { name: "Delete and remove records" }),
    );
    expect(onConfirm).toHaveBeenCalledWith(true);
  });

  it("allows deleting a repo with no scans without purging", () => {
    const onConfirm = vi.fn();
    render(
      <DeleteWithRecordsDialog
        open
        onOpenChange={() => undefined}
        kind="repo"
        name="empty"
        recordCount={0}
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledWith(false);
  });

  it("lets a collection be deleted without purging records", () => {
    const onConfirm = vi.fn();
    render(
      <DeleteWithRecordsDialog
        open
        onOpenChange={() => undefined}
        kind="collection"
        name="backend"
        recordCount={4}
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledWith(false);
  });
});
