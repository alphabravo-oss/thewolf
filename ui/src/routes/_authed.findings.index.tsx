// Fleet findings inbox. Two views:
//   - By repo: current-open counts per product (the working default)
//   - By rule: the same CVE/rule across many products
// Deep work stays on the scan page / repo current-findings list.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { BugIcon, ChevronDownIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { TableSkeleton } from "@/components/skeleton";
import { SeverityBadge } from "@/components/severity-badge";
import {
  useFindingsByRepo,
  useTopVulnerableRules,
  type AggregateRow,
} from "@/lib/fleet";
import type { Severity } from "@/lib/types";

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
  const [search, setSearch] = useState(q ?? "");
  const needle = search.trim().toLowerCase();

  return (
    <div className="page stack">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Findings</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Current open issues across the fleet. Work a product from its repo
            or scan page — this list is the inbox, not a merge of every scan.
          </p>
        </div>
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
                "h-8 px-3 rounded-md text-xs border " +
                (view === id
                  ? "bg-primary/15 border-primary/40 text-foreground"
                  : "border-border/40 text-muted-foreground hover:bg-muted/30")
              }
            >
              {label}
            </button>
          ))}
        </div>
      </header>

      <input
        type="search"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder={
          view === "repo" ? "Filter repos…" : "Filter rules, CVEs, titles…"
        }
        className="h-9 max-w-md px-3 rounded-md bg-muted/40 border border-border/40 text-sm"
      />

      {view === "repo" ? (
        <ByRepo needle={needle} />
      ) : (
        <ByRule needle={needle} />
      )}
    </div>
  );
}

function ByRepo({ needle }: { needle: string }) {
  const q = useFindingsByRepo();
  const rows = useMemo(() => {
    const src = q.data ?? [];
    if (!needle) return src;
    return src.filter((r) => r.name.toLowerCase().includes(needle));
  }, [q.data, needle]);
  const totals = useMemo(() => {
    return rows.reduce(
      (a, r) => ({
        repos: a.repos + 1,
        total: a.total + r.total,
        critical: a.critical + r.critical,
        high: a.high + r.high,
      }),
      { repos: 0, total: 0, critical: 0, high: 0 },
    );
  }, [rows]);

  if (q.isLoading) return <TableSkeleton rows={8} />;
  if (rows.length === 0) {
    return (
      <EmptyState
        icon={BugIcon}
        title={q.data && q.data.length > 0 ? "No repos match" : "No open findings"}
        description={
          q.data && q.data.length > 0
            ? "Clear the filter."
            : "Run a scan and current-open findings land here, grouped by product."
        }
        cta={
          q.data && q.data.length > 0
            ? undefined
            : { label: "Go to repos", to: "/repos" }
        }
      />
    );
  }

  return (
    <div className="glass-card overflow-hidden">
      <div className="px-4 py-2 text-xs text-muted-foreground border-b border-border/20">
        {totals.repos} repo{totals.repos === 1 ? "" : "s"} ·{" "}
        {totals.total.toLocaleString()} open · {totals.critical} critical ·{" "}
        {totals.high} high
      </div>
      <table className="w-full text-sm">
        <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
          <tr>
            <th className="text-left px-4 py-2">Repo</th>
            <th className="text-right px-4 py-2">Crit</th>
            <th className="text-right px-4 py-2">High</th>
            <th className="text-right px-4 py-2">Med</th>
            <th className="text-right px-4 py-2">Low</th>
            <th className="text-right px-4 py-2">Info</th>
            <th className="text-right px-4 py-2">Open</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.repo_id} className="border-t border-border/20 hover:bg-muted/20">
              <td className="px-4 py-2">
                <Link
                  to="/repos/$repoId"
                  params={{ repoId: r.repo_id }}
                  className="hover:underline font-medium"
                >
                  {r.name}
                </Link>
              </td>
              <SevCell n={r.critical} tone="crit" />
              <SevCell n={r.high} tone="high" />
              <SevCell n={r.medium} />
              <SevCell n={r.low} />
              <SevCell n={r.info} />
              <td className="px-4 py-2 text-right tabular-nums font-medium">
                {r.total.toLocaleString()}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ByRule({ needle }: { needle: string }) {
  const q = useTopVulnerableRules(100);
  const [open, setOpen] = useState<string | null>(null);
  const rows = useMemo(() => {
    const src = q.data ?? [];
    if (!needle) return src;
    return src.filter((r) => {
      const blob = `${r.key} ${r.title ?? ""} ${r.tool ?? ""} ${(r.repo_names ?? []).join(" ")}`.toLowerCase();
      return blob.includes(needle);
    });
  }, [q.data, needle]);

  if (q.isLoading) return <TableSkeleton rows={8} />;
  if (rows.length === 0) {
    return (
      <EmptyState
        icon={BugIcon}
        title={q.data && q.data.length > 0 ? "No rules match" : "No shared rules yet"}
        description="Rules that show up in more than one product appear here first."
      />
    );
  }

  return (
    <div className="glass-card overflow-hidden">
      <div className="px-4 py-2 text-xs text-muted-foreground border-b border-border/20">
        Same rule or CVE across products. Sorted by how many repos have it.
      </div>
      <table className="w-full text-sm">
        <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
          <tr>
            <th className="text-left px-4 py-2">Rule</th>
            <th className="text-left px-4 py-2">Tool</th>
            <th className="text-left px-4 py-2">Severity</th>
            <th className="text-right px-4 py-2">Repos</th>
            <th className="text-right px-4 py-2">Open</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <RuleRows
              key={r.key}
              row={r}
              expanded={open === r.key}
              onToggle={() => setOpen((cur) => (cur === r.key ? null : r.key))}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RuleRows({
  row,
  expanded,
  onToggle,
}: {
  row: AggregateRow;
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <>
      <tr
        className="border-t border-border/20 hover:bg-muted/20 cursor-pointer"
        onClick={onToggle}
      >
        <td className="px-4 py-2">
          <div className="flex items-start gap-2">
            <ChevronDownIcon
              className={`size-3.5 mt-0.5 text-muted-foreground shrink-0 transition-transform ${
                expanded ? "" : "-rotate-90"
              }`}
            />
            <div className="min-w-0">
              <div className="font-mono text-xs truncate">{row.key}</div>
              {row.title && row.title !== row.key ? (
                <div className="text-[11px] text-muted-foreground truncate">
                  {row.title}
                </div>
              ) : null}
            </div>
          </div>
        </td>
        <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
          {row.tool || "—"}
        </td>
        <td className="px-4 py-2">
          {row.severity ? (
            <SeverityBadge severity={row.severity as Severity} />
          ) : (
            "—"
          )}
        </td>
        <td className="px-4 py-2 text-right tabular-nums">{row.repos}</td>
        <td className="px-4 py-2 text-right tabular-nums">
          {row.findings.toLocaleString()}
        </td>
      </tr>
      {expanded && (
        <tr className="border-t border-border/10 bg-muted/10">
          <td colSpan={5} className="px-10 py-2 text-xs">
            <div className="flex flex-wrap gap-2">
              {(row.repo_ids ?? []).map((id, i) => (
                <Link
                  key={id}
                  to="/repos/$repoId"
                  params={{ repoId: id }}
                  className="hover:underline font-medium"
                  onClick={(e) => e.stopPropagation()}
                >
                  {row.repo_names?.[i] || id.slice(0, 8)}
                </Link>
              ))}
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

function SevCell({ n, tone }: { n: number; tone?: "crit" | "high" }) {
  const cls =
    n === 0
      ? "text-muted-foreground/40"
      : tone === "crit"
        ? "text-rose-300"
        : tone === "high"
          ? "text-amber-300"
          : "";
  return (
    <td className={`px-4 py-2 text-right tabular-nums ${cls}`}>
      {n || "—"}
    </td>
  );
}
