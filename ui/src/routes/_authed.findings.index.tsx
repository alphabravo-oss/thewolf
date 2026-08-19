// Fleet findings inbox. Two views:
//   - By repo: current-open counts per product (the working default)
//   - By rule: the same CVE/rule across many products
// Deep work stays on the scan page / repo current-findings list.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { BugIcon, ChevronDownIcon } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SeverityBadge } from "@/components/severity-badge";
import {
  useFindingsByRepo,
  useTopVulnerableRules,
  type AggregateRow,
  type FindingsByRepoRow,
} from "@/lib/fleet";
import type { Severity } from "@/lib/types";

type RepoRow = FindingsByRepoRow;
type SeverityKey = "critical" | "high" | "medium" | "low" | "info";

// Stable empty arrays — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_REPO_ROWS: RepoRow[] = [];
const EMPTY_RULE_ROWS: AggregateRow[] = [];

type View = "repo" | "rule";

// view is omitted for the default "by repo" inbox so /findings links
// elsewhere do not have to pass search params.
type Search = { view?: View; q?: string };

export const Route = createFileRoute("/_authed/findings/")({
  validateSearch: (s: Record<string, unknown>): Search => {
    const view: View =
      s.view === "rule" || typeof s.rule_id === "string" ? "rule" : "repo";
    const q =
      typeof s.q === "string"
        ? s.q
        : typeof s.rule_id === "string"
          ? s.rule_id
          : undefined;
    return {
      ...(view === "rule" ? { view } : {}),
      ...(q ? { q } : {}),
    };
  },
  component: FindingsPage,
});

function FindingsPage() {
  const params = Route.useSearch();
  const view: View = params.view === "rule" ? "rule" : "repo";
  const q = params.q;
  const navigate = Route.useNavigate();

  return (
    <PageShell>
      <PageHeader
        title="Findings"
        description="Current open issues across the fleet. Work a product from its repo or scan page — this list is the inbox, not a merge of every scan."
        actions={
          <div className="flex items-center gap-1.5">
            {(
              [
                ["repo", "By repo"],
                ["rule", "By rule"],
              ] as const
            ).map(([id, label]) => (
              <button
                key={id}
                type="button"
                onClick={() =>
                  navigate({
                    search: id === "rule" ? { view: "rule", q } : { q },
                  })
                }
                className={
                  "h-8 px-3 rounded-md text-xs border transition-colors " +
                  (view === id
                    ? "bg-accent border-border text-foreground"
                    : "border-border text-muted-foreground hover:bg-accent hover:text-foreground")
                }
              >
                {label}
              </button>
            ))}
          </div>
        }
      />

      {view === "repo" ? <ByRepo /> : <ByRule />}
    </PageShell>
  );
}

// Repo-count inbox. Columns are the severity ladder plus the open total, so
// the heaviest products sort to the top on any single severity.
function ByRepo() {
  const q = useFindingsByRepo();
  const rows = q.data ?? EMPTY_REPO_ROWS;

  const columns: Column<RepoRow>[] = [
    {
      key: "name",
      header: "Repo",
      sortAccessor: (r) => r.name,
      accessor: (r) => (
        <Link
          to="/repos/$repoId"
          params={{ repoId: r.repo_id }}
          className="font-medium hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          {r.name}
        </Link>
      ),
    },
    sevColumn("critical", "Crit"),
    sevColumn("high", "High"),
    sevColumn("medium", "Med"),
    sevColumn("low", "Low"),
    sevColumn("info", "Info"),
    {
      key: "total",
      header: "Open",
      align: "right",
      sortAccessor: (r) => r.total,
      accessor: (r) => (
        <span className="font-medium tabular-nums">{r.total.toLocaleString()}</span>
      ),
    },
  ];

  if (!q.isLoading && rows.length === 0) {
    return (
      <EmptyState
        icon={BugIcon}
        title="No open findings"
        description="Run a scan and current-open findings land here, grouped by product."
        cta={{ label: "Go to repos", to: "/repos" }}
      />
    );
  }

  return (
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={(r) => r.repo_id}
      persistKey="findings-by-repo"
      density="compact"
      loading={q.isLoading}
      isError={q.isError}
      onRetry={() => void q.refetch()}
      searchPlaceholder="Filter repos..."
      emptyMessage="No repos match"
    />
  );
}

// Severity count cell: zero reads as a dash in a muted tone so the eye lands on
// the columns that actually have volume.
function sevColumn(key: SeverityKey, header: string): Column<RepoRow> {
  return {
    key,
    header,
    align: "right",
    sortAccessor: (r) => r[key],
    accessor: (r) => {
      const n = r[key];
      return (
        <span
          className={
            n === 0
              ? "text-muted-foreground/40 tabular-nums"
              : key === "critical"
                ? "text-status-error tabular-nums"
                : key === "high"
                  ? "text-status-warning tabular-nums"
                  : "tabular-nums"
          }
        >
          {n || "—"}
        </span>
      );
    },
  };
}

// Same rule or CVE across products, sorted by blast radius. The affected-repo
// list expands inline rather than opening a detail page.
function ByRule() {
  const q = useTopVulnerableRules(100);
  const rows = q.data ?? EMPTY_RULE_ROWS;
  const [open, setOpen] = useState<string | null>(null);

  const columns: Column<AggregateRow>[] = [
    {
      key: "key",
      header: "Rule",
      sortAccessor: (r) => r.key,
      accessor: (r) => (
        <div className="flex items-start gap-2">
          <ChevronDownIcon
            className={`size-3.5 mt-0.5 shrink-0 text-muted-foreground transition-transform ${
              open === r.key ? "" : "-rotate-90"
            }`}
            aria-hidden="true"
          />
          <div className="min-w-0">
            <div className="truncate font-mono text-xs">{r.key}</div>
            {r.title && r.title !== r.key ? (
              <div className="truncate text-[11px] text-muted-foreground">{r.title}</div>
            ) : null}
            {open === r.key ? (
              <div className="mt-1.5 flex flex-wrap gap-2">
                {(r.repo_ids ?? []).map((id, i) => (
                  <Link
                    key={id}
                    to="/repos/$repoId"
                    params={{ repoId: id }}
                    className="text-xs font-medium hover:underline"
                    onClick={(e) => e.stopPropagation()}
                  >
                    {r.repo_names?.[i] || id.slice(0, 8)}
                  </Link>
                ))}
              </div>
            ) : null}
          </div>
        </div>
      ),
    },
    {
      key: "tool",
      header: "Tool",
      sortAccessor: (r) => r.tool ?? "",
      filter: { label: "Tool" },
      accessor: (r) => (
        <span className="font-mono text-xs text-muted-foreground">{r.tool || "—"}</span>
      ),
    },
    {
      key: "severity",
      header: "Severity",
      sortAccessor: (r) => r.severity ?? "",
      filter: { label: "Severity" },
      accessor: (r) =>
        r.severity ? <SeverityBadge severity={r.severity as Severity} size="sm" /> : "—",
    },
    {
      key: "repos",
      header: "Repos",
      align: "right",
      sortAccessor: (r) => r.repos,
      accessor: (r) => <span className="tabular-nums">{r.repos}</span>,
    },
    {
      key: "findings",
      header: "Open",
      align: "right",
      sortAccessor: (r) => r.findings,
      accessor: (r) => <span className="tabular-nums">{r.findings.toLocaleString()}</span>,
    },
  ];

  if (!q.isLoading && rows.length === 0) {
    return (
      <EmptyState
        icon={BugIcon}
        title="No shared rules yet"
        description="Rules that show up in more than one product appear here first."
      />
    );
  }

  return (
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={(r) => r.key}
      persistKey="findings-by-rule"
      density="compact"
      loading={q.isLoading}
      isError={q.isError}
      onRetry={() => void q.refetch()}
      searchPlaceholder="Filter rules, CVEs, titles..."
      emptyMessage="No rules match"
      onRowClick={(r) => setOpen((cur) => (cur === r.key ? null : r.key))}
    />
  );
}
