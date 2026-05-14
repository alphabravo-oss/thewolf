// Findings power-UX page:
//   - Severity + status + search filters with saved views
//   - Row multi-select with x to toggle, Shift+M to bulk-mark, Esc to clear
//   - j/k vim-style navigation between rows
//   - Enter opens the side-panel preview
//   - / focuses the search box
//
// All keyboard handlers are registered on `document` so the user doesn't
// need to focus the table first.
import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { BugIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Finding } from "@/lib/types";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { SeverityBadge } from "@/components/severity-badge";
import { FindingsToolbar } from "@/components/findings-toolbar";
import { FindingPreview } from "@/components/finding-preview";
import { useFindingsView } from "@/lib/store-views";
import { severityRank } from "@/lib/severity";
import { cn } from "@/lib/cn";

export const Route = createFileRoute("/_authed/findings")({
  component: FindingsPage,
});

function FindingsPage() {
  const q = useQuery({
    queryKey: ["findings", "all"],
    queryFn: async () => {
      const r = await api.get<{ findings: Finding[] }>("/findings?limit=2000");
      return r.data.findings ?? [];
    },
  });

  const view = useFindingsView();

  // Apply filters + sort by severity desc.
  const filtered = useMemo(() => {
    const src = q.data ?? [];
    const needle = view.search.trim().toLowerCase();
    return src
      .filter((f) => view.severities.has(f.severity))
      .filter((f) => (view.status ? f.status === view.status : true))
      .filter((f) => {
        if (!needle) return true;
        return (
          f.title.toLowerCase().includes(needle) ||
          f.file_path.toLowerCase().includes(needle) ||
          (f.rule_id?.toLowerCase().includes(needle) ?? false)
        );
      })
      .sort((a, b) => severityRank[b.severity] - severityRank[a.severity]);
  }, [q.data, view.search, view.severities, view.status]);

  // ---- selection state ---------------------------------------------------
  const [selected, setSelected] = useState<Set<string>>(new Set());
  function toggleSelect(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  function clearSelection() {
    setSelected(new Set());
  }

  // ---- cursor + preview --------------------------------------------------
  const [cursor, setCursor] = useState(0);
  const [preview, setPreview] = useState<Finding | null>(null);
  const tableRef = useRef<HTMLTableElement>(null);

  useEffect(() => {
    // Clamp cursor when the filtered set changes.
    if (cursor >= filtered.length) setCursor(Math.max(0, filtered.length - 1));
  }, [filtered.length, cursor]);

  // ---- keyboard handlers -------------------------------------------------
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tgt = e.target as HTMLElement | null;
      const inField =
        tgt &&
        (tgt.tagName === "INPUT" ||
          tgt.tagName === "TEXTAREA" ||
          tgt.tagName === "SELECT" ||
          tgt.isContentEditable);

      // `/` focuses search (only outside an input).
      if (!inField && e.key === "/") {
        e.preventDefault();
        const inp = document.querySelector<HTMLInputElement>(
          "input[placeholder='Search title or file…']",
        );
        inp?.focus();
        return;
      }

      // Esc → close preview / clear selection.
      if (e.key === "Escape") {
        if (preview) {
          setPreview(null);
          return;
        }
        if (selected.size > 0) {
          clearSelection();
          return;
        }
      }

      if (inField) return; // remaining shortcuts only outside fields

      // j / ArrowDown — next row.
      if (e.key === "j" || e.key === "ArrowDown") {
        e.preventDefault();
        setCursor((c) => Math.min(filtered.length - 1, c + 1));
        return;
      }
      // k / ArrowUp — previous.
      if (e.key === "k" || e.key === "ArrowUp") {
        e.preventDefault();
        setCursor((c) => Math.max(0, c - 1));
        return;
      }
      // Enter — open preview for the cursor.
      if (e.key === "Enter") {
        const f = filtered[cursor];
        if (f) {
          e.preventDefault();
          setPreview(f);
        }
        return;
      }
      // x — toggle selection on cursor.
      if (e.key === "x" || e.key === "X") {
        const f = filtered[cursor];
        if (f) {
          e.preventDefault();
          toggleSelect(f.id);
        }
        return;
      }
      // Shift+M — surface the bulk-action bar (already visible if selection
      // is non-empty; this key is a hint for users who don't see it).
      if (e.key === "M" && e.shiftKey && selected.size > 0) {
        e.preventDefault();
        // No-op beyond scrolling the toolbar into view.
        window.scrollTo({ top: 0, behavior: "smooth" });
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [filtered, cursor, preview, selected.size]);

  // Scroll the cursor row into view.
  useEffect(() => {
    const row = tableRef.current?.querySelector<HTMLTableRowElement>(
      `tr[data-row='${cursor}']`,
    );
    row?.scrollIntoView({ block: "nearest" });
  }, [cursor]);

  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Findings</h1>
        <p className="text-sm text-muted-foreground mt-1">
          {filtered.length} of {q.data?.length ?? 0} · use{" "}
          <kbd className="text-2xs px-1 rounded bg-muted/60">j</kbd>/
          <kbd className="text-2xs px-1 rounded bg-muted/60">k</kbd> to
          navigate,{" "}
          <kbd className="text-2xs px-1 rounded bg-muted/60">Enter</kbd> to
          preview,{" "}
          <kbd className="text-2xs px-1 rounded bg-muted/60">x</kbd> to select.
        </p>
      </header>

      <FindingsToolbar
        selectedIds={Array.from(selected)}
        onClearSelection={clearSelection}
      />

      {q.isLoading ? (
        <TableSkeleton rows={12} />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={BugIcon}
          title={q.data && q.data.length > 0 ? "No findings match filters" : "No findings yet"}
          description={
            q.data && q.data.length > 0
              ? "Clear or adjust your filters above."
              : "Run a scan and findings will land here."
          }
          cta={
            q.data && q.data.length > 0
              ? { label: "Clear filters", onClick: () => view.reset() }
              : { label: "Go to scans", to: "/scans" }
          }
        />
      ) : (
        <div className="glass-card overflow-hidden">
          <table ref={tableRef} className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="w-8 px-3 py-2">
                  <input
                    type="checkbox"
                    aria-label="Select all"
                    checked={
                      selected.size > 0 && selected.size === filtered.length
                    }
                    onChange={(e) =>
                      setSelected(
                        e.target.checked
                          ? new Set(filtered.map((f) => f.id))
                          : new Set(),
                      )
                    }
                    className="size-3.5 accent-blue-500"
                  />
                </th>
                <th className="text-left px-4 py-2 font-medium">Severity</th>
                <th className="text-left px-4 py-2 font-medium">Tool</th>
                <th className="text-left px-4 py-2 font-medium">Title</th>
                <th className="text-left px-4 py-2 font-medium">File</th>
                <th className="text-left px-4 py-2 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((f, i) => (
                <tr
                  key={f.id}
                  data-row={i}
                  onClick={() => {
                    setCursor(i);
                    setPreview(f);
                  }}
                  className={cn(
                    "border-t border-l-2 border-border/30 table-row-hover cursor-pointer",
                    selected.has(f.id) && "bg-blue-500/5",
                    cursor === i && "ring-1 ring-inset ring-blue-500/40",
                  )}
                >
                  <td
                    className="px-3 py-2"
                    onClick={(e) => {
                      e.stopPropagation();
                      toggleSelect(f.id);
                    }}
                  >
                    <input
                      type="checkbox"
                      aria-label={`Select ${f.title}`}
                      checked={selected.has(f.id)}
                      onChange={() => toggleSelect(f.id)}
                      className="size-3.5 accent-blue-500"
                    />
                  </td>
                  <td className="px-4 py-2">
                    <SeverityBadge severity={f.severity} />
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {f.tool_name}
                  </td>
                  <td className="px-4 py-2">{f.title}</td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground truncate max-w-xs">
                    {f.file_path}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {f.status}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <FindingPreview finding={preview} onClose={() => setPreview(null)} />
    </div>
  );
}
