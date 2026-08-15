import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  api: {
    get: mocks.get,
  },
}));

import { BranchSelect } from "./branch-select";

function renderSelect() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function Wrapper({ children }: PropsWithChildren) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return render(
    <BranchSelect
      repoId="repo-1"
      value="main"
      onChange={() => {}}
      defaultBranch="main"
    />,
    { wrapper: Wrapper },
  );
}

describe("BranchSelect", () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders live remote branches", async () => {
    mocks.get.mockResolvedValueOnce({
      data: {
        branches: ["main", "dev"],
        default_branch: "main",
        current_branch: "main",
      },
    });
    renderSelect();
    await waitFor(() => {
      expect(screen.getByRole("option", { name: "dev" })).toBeInTheDocument();
    });
    expect(screen.getByRole("option", { name: "main (default)" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
  });

  it("shows an error and retry instead of hiding other branches", async () => {
    mocks.get.mockRejectedValueOnce(new Error("failed to list GitHub branches"));
    renderSelect();
    await waitFor(() => {
      expect(
        screen.getByText(/Could not load live branches: failed to list GitHub branches/),
      ).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveValue("main");
  });
});
