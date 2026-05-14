// Scans list + scan-trigger form.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { GaugeIcon, PlayIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { Repo, Scan } from "@/lib/types";
import { parseToolList } from "@/lib/types";
import { TableSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { ScanStatusPill } from "@/components/scan-status-pill";

export const Route = createFileRoute("/_authed/scans/")({
  component: ScansPage,
});

function ScansPage() {
  const [showForm, setShowForm] = useState(false);
  const q = useQuery({
    queryKey: ["scans", "list"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?limit=200");
      return r.data ?? [];
    },
    refetchInterval: 10_000,
  });

  return (
    <div className="p-6 space-y-4 max-w-7xl">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Scans</h1>
        {!showForm && (
          <button
            type="button"
            onClick={() => setShowForm(true)}
            className="inline-flex items-center gap-2 px-3 h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90"
          >
            <PlayIcon className="size-4" />
            New scan
          </button>
        )}
      </header>

      {showForm && <NewScanForm onClose={() => setShowForm(false)} />}

      {q.isLoading ? (
        <TableSkeleton rows={10} />
      ) : !q.data || q.data.length === 0 ? (
        !showForm && (
          <EmptyState
            icon={GaugeIcon}
            title="No scans yet"
            description="Click New scan above, or pick a repo from a collection."
            cta={{ label: "New scan", onClick: () => setShowForm(true) }}
          />
        )
      ) : (
        <div className="glass-card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
              <tr>
                <th className="text-left font-medium px-4 py-2">Status</th>
                <th className="text-left font-medium px-4 py-2">Repo</th>
                <th className="text-left font-medium px-4 py-2">Branch</th>
                <th className="text-left font-medium px-4 py-2">Tools</th>
                <th className="text-right font-medium px-4 py-2">Findings</th>
                <th className="text-right font-medium px-4 py-2">Started</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((s) => (
                <tr key={s.id} className="border-t border-border/30 table-row-hover">
                  <td className="px-4 py-2">
                    <ScanStatusPill status={s.status} />
                  </td>
                  <td className="px-4 py-2">
                    <Link
                      to={
                        s.status === "running"
                          ? "/scans/$scanId/live"
                          : "/scans/$scanId"
                      }
                      params={{ scanId: s.id }}
                      className="font-medium hover:underline"
                    >
                      {s.repo?.name ?? s.id.slice(0, 8)}
                    </Link>
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {s.branch}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground tabular-nums">
                    {parseToolList(s.tools_completed).length}/{parseToolList(s.tools_selected).length}
                  </td>
                  <td className="px-4 py-2 text-right font-mono tabular-nums">
                    {s.finding_count}
                  </td>
                  <td className="px-4 py-2 text-right text-muted-foreground tabular-nums">
                    {s.started_at
                      ? new Date(s.started_at).toLocaleString()
                      : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// New-scan form: pick a repo, optionally narrow the tool list, override
// branch, POST /api/scans, navigate to the live scan view.
type ScannerSummary = { name: string; category: string; languages: string[] };

function NewScanForm({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const repos = useQuery({
    queryKey: ["repos", "for-scan"],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos");
      return r.data ?? [];
    },
  });
  const scanners = useQuery({
    queryKey: ["scanners", "list"],
    queryFn: async () => {
      const r = await api.get<ScannerSummary[]>("/scanners/list");
      return r.data ?? [];
    },
  });
  const [repoId, setRepoId] = useState("");
  const [branch, setBranch] = useState("");
  // Tool selection mode:
  //   - "auto"     → empty tools array → backend auto-detects by language
  //   - "explicit" → only the checked tools run
  const [toolMode, setToolMode] = useState<"auto" | "explicit">("auto");
  const [picked, setPicked] = useState<Set<string>>(new Set());

  const m = useMutation({
    mutationFn: async () => {
      const repo = repos.data?.find((r) => r.id === repoId);
      const body: Record<string, unknown> = {
        repo_id: repoId,
        branch: branch.trim() || repo?.default_branch || "main",
      };
      if (toolMode === "explicit" && picked.size > 0) {
        body.tools = Array.from(picked).sort();
      }
      const r = await api.post<Scan>("/scans", body);
      return r.data;
    },
    onSuccess: (scan) => {
      qc.invalidateQueries({ queryKey: ["scans"] });
      toast.success("Scan started");
      navigate({ to: "/scans/$scanId/live", params: { scanId: scan.id } });
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to start scan");
    },
  });

  const selectedRepo = repos.data?.find((r) => r.id === repoId);

  // Group scanners by category for a tidier checkbox list.
  const grouped = (() => {
    const map = new Map<string, ScannerSummary[]>();
    for (const s of scanners.data ?? []) {
      const key = s.category || "other";
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(s);
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  })();

  const togglePick = (name: string) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };
  const pickAll = () => setPicked(new Set((scanners.data ?? []).map((s) => s.name)));
  const pickNone = () => setPicked(new Set());

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!repoId) return;
        m.mutate();
      }}
      className="glass-card p-4 space-y-3 max-w-2xl"
    >
      <div className="flex items-center justify-between">
        <div className="text-sm font-semibold">New scan</div>
        <button
          type="button"
          onClick={onClose}
          className="size-7 grid place-items-center rounded hover:bg-muted/40"
          aria-label="Cancel"
        >
          <XIcon className="size-4" />
        </button>
      </div>

      <label className="block">
        <span className="text-xs text-muted-foreground">Repo *</span>
        {repos.isLoading ? (
          <div className="text-xs text-muted-foreground mt-1">Loading…</div>
        ) : !repos.data || repos.data.length === 0 ? (
          <div className="text-xs text-muted-foreground mt-1">
            No repos. Add one from a{" "}
            <Link to="/collections" className="underline">
              collection
            </Link>{" "}
            first.
          </div>
        ) : (
          <select
            value={repoId}
            onChange={(e) => {
              setRepoId(e.target.value);
              const r = repos.data?.find((x) => x.id === e.target.value);
              if (r) setBranch("");
            }}
            required
            className="mt-1 w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          >
            <option value="">Pick a repo…</option>
            {repos.data.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name} — {r.source_path}
              </option>
            ))}
          </select>
        )}
      </label>

      {selectedRepo && (
        <label className="block">
          <span className="text-xs text-muted-foreground">
            Branch (defaults to {selectedRepo.default_branch || "main"})
          </span>
          <input
            type="text"
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            placeholder={selectedRepo.default_branch || "main"}
            className="mt-1 w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
          />
        </label>
      )}

      {selectedRepo && (
        <div className="space-y-2">
          <div className="text-xs text-muted-foreground">Tools to run</div>
          <div className="flex gap-1 text-xs">
            <button
              type="button"
              onClick={() => setToolMode("auto")}
              className={`h-7 px-2.5 rounded-md transition ${
                toolMode === "auto"
                  ? "bg-primary text-primary-foreground"
                  : "hover:bg-muted/40 text-muted-foreground"
              }`}
            >
              Auto (by detected language)
            </button>
            <button
              type="button"
              onClick={() => setToolMode("explicit")}
              className={`h-7 px-2.5 rounded-md transition ${
                toolMode === "explicit"
                  ? "bg-primary text-primary-foreground"
                  : "hover:bg-muted/40 text-muted-foreground"
              }`}
            >
              Pick tools
            </button>
          </div>

          {toolMode === "explicit" && (
            <div className="border border-border/40 rounded-md p-3 space-y-2 bg-muted/10">
              {scanners.isLoading ? (
                <div className="text-xs text-muted-foreground">Loading scanners…</div>
              ) : (
                <>
                  <div className="flex items-center justify-between text-xs">
                    <span className="text-muted-foreground">
                      {picked.size} of {scanners.data?.length ?? 0} selected
                    </span>
                    <div className="flex gap-2">
                      <button
                        type="button"
                        onClick={pickAll}
                        className="underline hover:text-foreground text-muted-foreground"
                      >
                        All
                      </button>
                      <button
                        type="button"
                        onClick={pickNone}
                        className="underline hover:text-foreground text-muted-foreground"
                      >
                        None
                      </button>
                    </div>
                  </div>
                  <div className="max-h-64 overflow-y-auto space-y-2">
                    {grouped.map(([cat, items]) => (
                      <div key={cat}>
                        <div className="text-[10px] uppercase tracking-wide text-muted-foreground mb-1">
                          {cat}
                        </div>
                        <div className="grid grid-cols-3 gap-1 text-xs">
                          {items.map((s) => (
                            <label
                              key={s.name}
                              className="flex items-center gap-1.5 px-1.5 py-1 rounded hover:bg-muted/30 cursor-pointer"
                            >
                              <input
                                type="checkbox"
                                checked={picked.has(s.name)}
                                onChange={() => togglePick(s.name)}
                                className="accent-primary"
                              />
                              <span className="truncate" title={s.languages.join(", ") || "any"}>
                                {s.name}
                              </span>
                            </label>
                          ))}
                        </div>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>
          )}
        </div>
      )}

      <div className="flex items-center gap-2 justify-end">
        <button
          type="button"
          onClick={onClose}
          className="h-8 px-3 rounded-md text-sm hover:bg-muted/40"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={
            !repoId ||
            m.isPending ||
            (toolMode === "explicit" && picked.size === 0)
          }
          className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50 inline-flex items-center gap-1.5"
        >
          <PlayIcon className="size-3.5" />
          {m.isPending
            ? "Starting…"
            : toolMode === "explicit"
              ? `Start scan (${picked.size})`
              : "Start scan"}
        </button>
      </div>
    </form>
  );
}
