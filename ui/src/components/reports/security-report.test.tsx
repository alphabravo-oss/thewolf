import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { SecurityReport } from "@/lib/reports";

// The report is static: no router or API, so the chart helpers' hooks are unused.
vi.mock("@tanstack/react-router", () => ({ Link: () => null, useNavigate: () => () => {} }));

import { OBSERVED_NOTE, SecurityReportView } from "./security-report";

const day = (date: string, critical: number, high: number) => ({ date, counts: { critical, high, medium: 0, low: 0, info: 0, total: critical + high } });
const report: SecurityReport = {
  generated_at: "2026-10-28T09:00:00Z",
  collection: { id: "c1", name: "Backend" },
  open_by_severity: { critical: 2, high: 5 },
  trend: [day("2026-10-01", 1, 1), ...Array.from({ length: 7 }, (_, i) => day(`2026-10-0${i + 2}`, 2, 5))],
  changes: { window_days: 7, compared_branches: 3, excluded_branches: 0, new: 4, resurfaced: 1, resolved: 6, suppressed: 0 },
  repo_count: 4,
  coverage: { fresh_days: 7, fresh_repos: 3 },
  gates_failing: 1,
  fixes_pushed: 2,
  aging: [{ days: 30, critical: 1, high: 3 }, { days: 60, critical: 0, high: 2 }, { days: 90, critical: 0, high: 1 }],
  top_repos: [{ repo_id: "r1", name: "payments-api", total: 7, critical: 2, high: 5, medium: 0, low: 0, info: 0 }],
};

describe("SecurityReportView", () => {
  it("renders every figure from the report JSON with the observed note", () => {
    render(<SecurityReportView report={report} title="Weekly posture" />);
    expect(screen.getByRole("heading", { name: "Weekly posture" })).toBeInTheDocument();
    expect(screen.getByText(/^Backend · generated/)).toBeInTheDocument();
    expect(screen.getByText(OBSERVED_NOTE)).toBeInTheDocument();
    expect(screen.getByText("Critical 2 · High 5")).toBeInTheDocument();
    expect(screen.getByText("+5 vs 7 days earlier")).toBeInTheDocument();
    expect(screen.getByText("5 / 6")).toBeInTheDocument();
    expect(screen.getByText("75%")).toBeInTheDocument();
    const aging = screen.getByRole("table", { name: "Open critical and high findings by age" });
    expect(within(aging).getByRole("rowheader", { name: "Over 60 days" }).parentElement).toHaveTextContent("Over 60 days02");
    expect(screen.getByRole("rowheader", { name: "payments-api" })).toBeInTheDocument();
  });
});
