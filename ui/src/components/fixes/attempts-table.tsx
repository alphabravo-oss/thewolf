// Decisions — grouped by scanner, then by real change (one OpenCode turn
// / one file bump), not one row per finding.
import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ChevronDownIcon, ListChecksIcon } from "lucide-react";
import type { FixAttempt } from "@/lib/fixes";
import { decisionInfo, fetchFixDiff, findingLabel } from "@/lib/fixes";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";

const PAGE_SIZE = 20;

export type ToolRollup = {
  tool: string;
  total: number;
  kept: number;
  open: number;
  unfixable: number;
  muted?: number;
  deferred?: number;
  rolled?: number;
  after?: number;
};

type OutcomeFilter = "all" | "kept" | "rolled_back" | "muted" | "unfixable";

export type ChangeCard = {
  key: string;
  tool: string;
  outcome: FixAttempt["outcome"];
  excerpt: string;
  files: string[];
  attempts: FixAttempt[];
};

export function AttemptsTable({
  attempts,
  tools: summaryTools,
  fixId,
}: {
  attempts: FixAttempt[];
  tools?: ToolRollup[];
  fixId: string;
}) {
  const [toolFilter, setToolFilter] = useState<string>("all");
  const [outcomeFilter, setOutcomeFilter] = useState<OutcomeFilter>("all");
  const [page, setPage] = useState(1);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [open, setOpen] = useState<ChangeCard | null>(null);

  const tools = summaryTools && summaryTools.length > 0 ? summaryTools : [];

  const cards = useMemo(
    () => groupChanges(attempts),
    [attempts],
  );

  const byScanner = useMemo(() => {
    const groups = new Map<string, ChangeCard[]>();
    for (const c of cards) {
      if (outcomeFilter !== "all" && c.outcome !== outcomeFilter) continue;
      if (toolFilter !== "all" && c.tool !== toolFilter) continue;
      const list = groups.get(c.tool) ?? [];
      list.push(c);
      groups.set(c.tool, list);
    }
    const order = tools.map((t) => t.tool);
    const names = [
      ...order.filter((t) => groups.has(t)),
      ...[...groups.keys()].filter((t) => !order.includes(t)),
    ];
    return names.map((tool) => ({ tool, cards: groups.get(tool) ?? [] }));
  }, [cards, tools, toolFilter, outcomeFilter]);

  const flat = byScanner.flatMap((g) => g.cards);
  const totalPages = Math.max(1, Math.ceil(flat.length / PAGE_SIZE));
  const pageClamped = Math.min(page, totalPages);
  const start = (pageClamped - 1) * PAGE_SIZE;
  const pageKeys = new Set(flat.slice(start, start + PAGE_SIZE).map((c) => c.key));

  const selectTool = (tool: string) => {
    setToolFilter((cur) => (cur === tool ? "all" : tool));
    setPage(1);
  };

  return (
    <div className="glass-card overflow-hidden">
      <div className="flex items-center gap-2 px-5 py-3 text-sm font-medium border-b border-border/30">
        <ListChecksIcon className="size-4 text-muted-foreground" />
        Decisions
        <span className="text-xs text-muted-foreground">
          ({cards.length} changes · {attempts.length} findings)
        </span>
      </div>

      {tools.length > 0 && (
        <div className="border-b border-border/20">
          <div className="px-5 py-2 text-xs font-medium text-muted-foreground">
            By scanner
            <span className="ml-2 font-normal">
              findings reported vs still open after this agent
            </span>
          </div>
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="text-left px-4 py-2">Scanner</th>
                <th className="text-right px-4 py-2">Before</th>
                <th className="text-right px-4 py-2">Fixed</th>
                <th className="text-right px-4 py-2">Muted</th>
                <th className="text-right px-4 py-2">After</th>
                <th className="text-right px-4 py-2">Rolled</th>
              </tr>
            </thead>
            <tbody>
              {tools.map((t) => {
                const active = toolFilter === t.tool;
                const after = t.after ?? Math.max(0, t.total - t.kept - (t.muted ?? 0));
                return (
                  <tr
                    key={t.tool}
                    className={`border-t border-border/20 cursor-pointer ${
                      active ? "bg-primary/10" : "hover:bg-muted/20"
                    }`}
                    onClick={() => selectTool(t.tool)}
                  >
                    <td className="px-4 py-2 font-mono text-xs">{t.tool}</td>
                    <td className="px-4 py-2 text-right tabular-nums">{t.total}</td>
                    <td className="px-4 py-2 text-right tabular-nums text-emerald-300">
                      {t.kept}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums">{t.muted ?? 0}</td>
                    <td className="px-4 py-2 text-right tabular-nums">{after}</td>
                    <td className="px-4 py-2 text-right tabular-nums text-amber-300">
                      {t.rolled ?? 0}
                    </td>
                  </tr>
                );
              })}
            </tbody>
            <tfoot>
              <tr className="border-t border-border/40 bg-muted/15 font-medium">
                <td className="px-4 py-2 text-xs uppercase tracking-wide text-muted-foreground">
                  Total
                </td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {tools.reduce((n, t) => n + t.total, 0)}
                </td>
                <td className="px-4 py-2 text-right tabular-nums text-emerald-300">
                  {tools.reduce((n, t) => n + t.kept, 0)}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {tools.reduce((n, t) => n + (t.muted ?? 0), 0)}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {tools.reduce(
                    (n, t) =>
                      n + (t.after ?? Math.max(0, t.total - t.kept - (t.muted ?? 0))),
                    0,
                  )}
                </td>
                <td className="px-4 py-2 text-right tabular-nums text-amber-300">
                  {tools.reduce((n, t) => n + (t.rolled ?? 0), 0)}
                </td>
              </tr>
            </tfoot>
          </table>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-1.5 px-5 py-2 border-b border-border/20">
        {(
          [
            ["all", "All"],
            ["kept", "Fixed"],
            ["rolled_back", "Rolled back"],
            ["muted", "Muted"],
            ["unfixable", "Skipped"],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            type="button"
            onClick={() => {
              setOutcomeFilter(id);
              setPage(1);
            }}
            className={
              "h-7 px-2 rounded-md text-[11px] border " +
              (outcomeFilter === id
                ? "bg-primary/15 border-primary/40 text-foreground"
                : "border-border/40 text-muted-foreground hover:bg-muted/30")
            }
          >
            {label}
          </button>
        ))}
        {toolFilter !== "all" && (
          <button
            type="button"
            onClick={() => selectTool(toolFilter)}
            className="h-7 px-2 rounded-md text-[11px] border border-border/40 text-muted-foreground hover:bg-muted/30"
          >
            {toolFilter} ×
          </button>
        )}
      </div>

      {attempts.length === 0 ? (
        <p className="px-5 py-4 text-sm text-muted-foreground">
          No decisions recorded yet.
        </p>
      ) : flat.length === 0 ? (
        <p className="px-5 py-4 text-sm text-muted-foreground">
          No decisions match this filter.
        </p>
      ) : (
        <>
          {byScanner.map((g) => {
            const visible = g.cards.filter((c) => pageKeys.has(c.key));
            if (visible.length === 0) return null;
            const closed = collapsed[g.tool];
            return (
              <div key={g.tool} className="border-t border-border/20">
                <button
                  type="button"
                  onClick={() =>
                    setCollapsed((c) => ({ ...c, [g.tool]: !c[g.tool] }))
                  }
                  className="w-full flex items-center gap-2 px-4 py-2 text-left text-xs font-medium bg-muted/15 hover:bg-muted/25"
                >
                  <ChevronDownIcon
                    className={`size-3.5 text-muted-foreground transition-transform ${
                      closed ? "-rotate-90" : ""
                    }`}
                  />
                  <span className="font-mono">{g.tool}</span>
                  <span className="text-muted-foreground font-normal">
                    {g.cards.length} change{g.cards.length === 1 ? "" : "s"}
                  </span>
                </button>
                {!closed &&
                  visible.map((c) => (
                    <ChangeRow key={c.key} card={c} onOpen={() => setOpen(c)} />
                  ))}
              </div>
            );
          })}
          {totalPages > 1 && (
            <div className="flex items-center justify-between px-4 py-2 text-xs border-t border-border/20">
              <button
                type="button"
                onClick={() => setPage(Math.max(1, pageClamped - 1))}
                disabled={pageClamped <= 1}
                className="h-8 px-3 rounded-md hover:bg-muted/40 disabled:opacity-30"
              >
                ← Prev
              </button>
              <div className="text-muted-foreground tabular-nums">
                {start + 1}–{Math.min(start + PAGE_SIZE, flat.length)} of {flat.length}
              </div>
              <button
                type="button"
                onClick={() => setPage(Math.min(totalPages, pageClamped + 1))}
                disabled={pageClamped >= totalPages}
                className="h-8 px-3 rounded-md hover:bg-muted/40 disabled:opacity-30"
              >
                Next →
              </button>
            </div>
          )}
        </>
      )}

      <ChangePanel fixId={fixId} card={open} onClose={() => setOpen(null)} />
    </div>
  );
}

function ChangeRow({ card, onOpen }: { card: ChangeCard; onOpen: () => void }) {
  const excerpt = card.excerpt;
  const info = decisionInfo(card.outcome, excerpt);
  const n = card.attempts.length;
  const first = card.attempts[0];
  const label =
    n === 1
      ? findingLabel({
          title: first.title,
          file_path: first.file_path,
          line_start: first.line_start,
          severity: first.severity,
          tool_name: first.tool_name,
          id: first.finding_id,
        })
      : `${n} findings · ${card.files.join(", ") || "shared change"}`;
  const why =
    excerpt.startsWith("rolled back:") || excerpt.startsWith("SKIP:") || excerpt.startsWith("MUTE:")
      ? excerpt
      : "";
  return (
    <button
      type="button"
      onClick={onOpen}
      className="w-full text-left px-4 py-2.5 border-t border-border/15 hover:bg-muted/20"
    >
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="text-sm truncate">{label}</div>
          {excerpt && !why ? (
            <div className="mt-0.5 text-[11px] text-muted-foreground truncate">{excerpt}</div>
          ) : null}
          {why ? (
            <div className="mt-0.5 text-[11px] text-amber-200/90 truncate">{why}</div>
          ) : null}
        </div>
        <span
          title={info.hint}
          className={`shrink-0 text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 ${
            info.tone === "ok"
              ? "text-emerald-300 bg-emerald-500/10 border-emerald-500/30"
              : info.tone === "warn"
                ? "text-amber-300 bg-amber-500/10 border-amber-500/30"
                : "text-muted-foreground bg-muted/20 border-border/30"
          }`}
        >
          {info.label}
        </span>
      </div>
    </button>
  );
}

function ChangePanel({
  fixId,
  card,
  onClose,
}: {
  fixId: string;
  card: ChangeCard | null;
  onClose: () => void;
}) {
  const files = card?.files.filter(Boolean) ?? [];
  const diffQ = useQuery({
    queryKey: ["fix-diff-files", fixId, files],
    enabled: !!card && files.length > 0 && card.outcome === "kept",
    queryFn: () => fetchFixDiff(fixId, files),
  });
  const excerpt = card?.excerpt ?? "";
  const info = card ? decisionInfo(card.outcome, excerpt) : null;

  return (
    <Sheet open={!!card} onOpenChange={(v) => !v && onClose()}>
      <SheetContent
        side="right"
        className="sm:max-w-2xl w-full overflow-y-auto bg-background"
      >
        {card && (
          <>
            <SheetHeader>
              <SheetTitle className="text-left">
                {excerpt || `${card.attempts.length} findings`}
              </SheetTitle>
              <SheetDescription className="text-left">
                {card.tool}
                {info ? ` · ${info.label}` : ""}
                {card.files.length ? ` · ${card.files.join(", ")}` : ""}
              </SheetDescription>
            </SheetHeader>
            <ul className="mt-4 space-y-1.5 text-sm">
              {card.attempts.map((a) => (
                <li key={a.id} className="min-w-0">
                  <Link
                    to="/findings/$findingId"
                    params={{ findingId: a.finding_id }}
                    className="text-primary hover:underline break-all"
                  >
                    {findingLabel({
                      title: a.title,
                      file_path: a.file_path,
                      line_start: a.line_start,
                      severity: a.severity,
                      tool_name: a.tool_name,
                      id: a.finding_id,
                    })}
                  </Link>
                </li>
              ))}
            </ul>
            {excerpt.startsWith("rolled back:") && (
              <p className="mt-3 text-xs text-amber-200">{excerpt}</p>
            )}
            {card.outcome === "kept" && (
              <div className="mt-4">
                <h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground mb-2">
                  Diff
                </h3>
                {diffQ.isLoading ? (
                  <p className="text-xs text-muted-foreground">Loading hunks…</p>
                ) : !diffQ.data ? (
                  <p className="text-xs text-muted-foreground">
                    No file hunks on the branch yet (still assembling, or this
                    change was a version bump already committed).
                  </p>
                ) : (
                  <pre className="mono text-[11px] leading-relaxed max-h-[28rem] overflow-auto rounded-md border border-border/40 bg-black/40 p-3 whitespace-pre">
                    {diffQ.data.split("\n").map((line, i) => (
                      <div key={i} className={hunkClass(line)}>
                        {line || " "}
                      </div>
                    ))}
                  </pre>
                )}
              </div>
            )}
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}

function hunkClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) return "text-muted-foreground";
  if (line.startsWith("@@")) return "text-sky-300";
  if (line.startsWith("+")) return "text-emerald-300";
  if (line.startsWith("-")) return "text-rose-300";
  if (line.startsWith("diff ") || line.startsWith("index ")) return "text-muted-foreground/70";
  return "text-foreground/70";
}

export function groupChanges(attempts: FixAttempt[]): ChangeCard[] {
  type Turn = { token: string; items: FixAttempt[] };
  const turns = new Map<string, Turn>();
  for (const a of attempts) {
    const token = `${a.outcome}|${a.engine_used}|${a.input_tokens}|${a.output_tokens}|${a.duration_ms}`;
    const t = turns.get(token) ?? { token, items: [] };
    t.items.push(a);
    turns.set(token, t);
  }
  const cards: ChangeCard[] = [];
  for (const turn of turns.values()) {
    const byFile = new Map<string, FixAttempt[]>();
    for (const a of turn.items) {
      const file = a.file_path || a.files_changed.split(",")[0] || "";
      const list = byFile.get(file) ?? [];
      list.push(a);
      byFile.set(file, list);
    }
    const uniqueFiles = [...byFile.keys()].filter(Boolean);
    const oneManifest =
      uniqueFiles.length <= 1 ||
      uniqueFiles.every((f) => /(?:go\.mod|go\.sum|package-lock\.json|yarn\.lock|pnpm-lock\.yaml|Cargo\.lock)$/.test(f));
    if (oneManifest && turn.items.length > 1) {
      cards.push(makeCard(turn.token, turn.items));
      continue;
    }
    const byExcerpt = new Map<string, FixAttempt[]>();
    for (const a of turn.items) {
      const ex = (a.diff_excerpt || "").trim() || a.file_path || a.finding_id;
      const list = byExcerpt.get(ex) ?? [];
      list.push(a);
      byExcerpt.set(ex, list);
    }
    for (const [, items] of byExcerpt) {
      cards.push(makeCard(turn.token + "|" + items[0].id, items));
    }
  }
  return cards;
}

function makeCard(key: string, items: FixAttempt[]): ChangeCard {
  const files = [
    ...new Set(
      items.flatMap((a) => {
        const own = a.file_path ? [a.file_path] : [];
        return own;
      }),
    ),
  ];
  const excerpts = [...new Set(items.map((a) => (a.diff_excerpt || "").trim()).filter(Boolean))];
  return {
    key,
    tool: items[0].tool_name || "unknown",
    outcome: items[0].outcome,
    excerpt: excerpts.length === 1 ? excerpts[0] : excerpts[0] || "",
    files,
    attempts: items,
  };
}
