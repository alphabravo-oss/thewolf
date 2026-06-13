import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { AddRepoForm } from "./add-repo-form";

vi.mock("@/lib/api", () => ({
  api: {
    post: vi.fn().mockResolvedValue({ data: { data: { id: "new-repo-1" } } }),
  },
}));

function renderWithClient(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("AddRepoForm", () => {
  it("defaults to Local path mode", () => {
    renderWithClient(<AddRepoForm onDone={() => {}} />);
    expect(screen.getByText("Local path")).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/Users\/me/)).toBeInTheDocument();
  });

  it("switches to GitHub mode and shows the secret hint", () => {
    renderWithClient(<AddRepoForm onDone={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "GitHub" }));
    expect(screen.getByPlaceholderText("alphabravo-oss/thewolf")).toBeInTheDocument();
    expect(screen.getByText(/github_token secret/i)).toBeInTheDocument();
  });

  it("disables submit when GitHub source is malformed", () => {
    renderWithClient(<AddRepoForm onDone={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "GitHub" }));
    fireEvent.change(screen.getByPlaceholderText(/owner\/repo/i), {
      target: { value: "not a github source" },
    });
    fireEvent.change(screen.getByPlaceholderText("my-project"), {
      target: { value: "test" },
    });
    expect(screen.getByRole("button", { name: /Create/i })).toBeDisabled();
  });
});
