// Fleet findings inbox. Three views:
//   - By repo: current-open counts per product (the working default)
//   - By rule: the same CVE/rule across many products
//   - List: individual findings with bulk triage (defaults to new-since-baseline)
// Deep work stays on the scan page / repo current-findings list.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BugIcon, ChevronDownIcon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
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
import type { Finding, FindingStatus, Severity } from "@/lib/types";

type RepoRow = FindingsByRepoRow;
type SeverityKey = "critical" | "high" | "medium" | "low" | "info";

// Stable empty arrays — a fresh `[]` fallback each render would invalidate the
// table's data-dependent row models on every render.
const EMPTY_REPO_ROWS: RepoRow[] = [];
const EMPTY_RULE_ROWS: AggregateRow[] = [];
const EMPTY_FINDINGS: Finding[] = [];

type View = "repo" | "rule" | "list";

// view is omitted for the default "by repo" inbox so /findings links
// elsewhere do not have to pass search params.
type Search = { view?: View; q?: string };

export const Route = createFileRoute("/_authed/findings/")({
  validateSearch: (s: Record<string, unknown>): Search => {
    const view: View =
      s.view === "rule" || typeof s.rule_id === "string"
        ? "rule"
        : s.view === "list"
          ? "list"
          : "repo";
    const q =
      typeof s.q === "string"
        ? s.q
        : typeof s.rule_id === "string"
          ? s.rule_id
          : undefined;
    return {
      ...(view !== "repo" ? { view } : {}),
      ...(q ? { q } : {}),
    };
  },
  component: FindingsPage,
});

function FindingsPage() {
  const params = Route.useSearch();
  const view: View =
    params.view === "rule" ? "rule" : params.view === "list" ? "list" : "repo";
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
                ["list", "List"],
              ] as const
            ).map(([id, label]) => (
              <button
                key={id}
                type="button"
                onClick={() =>
                  navigate({
                    search: id === "repo" ? { q } : { view: id, q },
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

      {view === "repo" ? <ByRepo /> : view === "rule" ? <ByRule /> : <FindingsList />}
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

const SEVERITY_RANK: Record<string, number> = {
  critical: 5,
  high: 4,
  medium: 3,
  low: 2,
  info: 1,
};

function FindingsList() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [scope, setScope] = useState<"new" | "all">("new");

  const q = useQuery({
    queryKey: ["findings", "list", scope],
    queryFn: async () => {
      const params = new URLSearchParams({ per_page: "200", status: "open" });
      if (scope === "new") params.set("baseline_state", "new,resurfaced");
      const r = await api.get<Finding[]>(`/findings?${params.toString()}`);
      return r.data ?? [];
    },
  });
  const rows = q.data ?? EMPTY_FINDINGS;

  const bulk = useMutation({
    mutationFn: async (vars: { ids: string[]; status: FindingStatus }) => {
      await api.post("/findings/bulk", vars);
    },
    onSuccess: (_d, vars) => {
      qc.invalidateQueries({ queryKey: ["findings"] });
      toast.success(
        `Marked ${vars.ids.length} finding${vars.ids.length === 1 ? "" : "s"} as ${vars.status.replace("_", " ")}`,
      );
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Bulk update failed"),
  });

  const columns: Column<Finding>[] = [
    {
      key: "severity",
      header: "Severity",
      width: "8rem",
      sortAccessor: (f) => SEVERITY_RANK[f.severity] ?? 0,
      accessor: (f) => <SeverityBadge severity={f.severity} size="sm" />,
    },
    {
      key: "title",
      header: "Title",
      sortAccessor: (f) => f.title,
      accessor: (f) => (
        <Link
          to="/findings/$findingId"
          params={{ findingId: f.id }}
          className="hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          {f.title}
        </Link>
      ),
    },
    {
      key: "repo",
      header: "Repo",
      sortAccessor: (f) => f.repo?.name ?? f.repo_id,
      accessor: (f) =>
        f.repo_id ? (
          <Link
            to="/repos/$repoId"
            params={{ repoId: f.repo_id }}
            className="text-xs hover:underline"
            onClick={(e) => e.stopPropagation()}
          >
            {f.repo?.name || f.repo_id.slice(0, 8)}
          </Link>
        ) : (
          "—"
        ),
    },
    {
      key: "file",
      header: "File",
      sortAccessor: (f) => f.file_path ?? "",
      accessor: (f) => (
        <span className="block max-w-xs truncate font-mono text-xs text-muted-foreground">
          {f.file_path || "—"}
        </span>
      ),
    },
    {
      key: "baseline",
      header: "Baseline",
      sortAccessor: (f) => f.baseline_state ?? "",
      accessor: (f) => (
        <span className="text-xs text-muted-foreground">
          {f.baseline_state || "—"}
        </span>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-1.5">
        {(
          [
            ["new", "New since baseline"],
            ["all", "All open"],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            type="button"
            onClick={() => setScope(id)}
            className={
              "h-8 px-3 rounded-md text-xs border transition-colors " +
              (scope === id
                ? "bg-accent border-border text-foreground"
                : "border-border text-muted-foreground hover:bg-accent hover:text-foreground")
            }
          >
            {label}
          </button>
        ))}
      </div>

      {!q.isLoading && rows.length === 0 ? (
        scope === "new" ? (
          <EmptyState
            icon={BugIcon}
            title="No new findings since baseline"
            description="New and resurfaced issues land here. Switch to all open to triage the backlog."
            cta={{ label: "Show all open", onClick: () => setScope("all") }}
          />
        ) : (
          <EmptyState
            icon={BugIcon}
            title="No open findings"
            description="Run a scan and current-open findings land here."
            cta={{ label: "Go to repos", to: "/repos" }}
          />
        )
      ) : (
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(f) => f.id}
          persistKey="findings-list"
          density="compact"
          selectable
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          searchPlaceholder="Filter findings..."
          emptyMessage="No findings match"
          onRowClick={(f) =>
            navigate({
              to: "/findings/$findingId",
              params: { findingId: f.id },
            })
          }
          bulkActions={(selected) => (
            <>
              <button
                type="button"
                disabled={bulk.isPending}
                onClick={() =>
                  bulk.mutate({
                    ids: selected.map((f) => f.id),
                    status: "wont_fix",
                  })
                }
                className="h-7 px-2 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
              >
                Won't fix
              </button>
              <button
                type="button"
                disabled={bulk.isPending}
                onClick={() =>
                  bulk.mutate({
                    ids: selected.map((f) => f.id),
                    status: "false_positive",
                  })
                }
                className="h-7 px-2 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
              >
                False positive
              </button>
              <button
                type="button"
                onClick={() => downloadFindingsJson(selected)}
                className="h-7 px-2 rounded-md border border-border text-xs hover:bg-muted/40"
              >
                Export JSON
              </button>
            </>
          )}
        />
      )}
    </div>
  );
}

function downloadFindingsJson(rows: Finding[]) {
  const blob = new Blob([JSON.stringify(rows, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "findings.json";
  a.click();
  URL.revokeObjectURL(url);
}
