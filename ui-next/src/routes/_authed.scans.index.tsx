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

// New-scan form: pick a repo, override branch if needed, POST /api/scans,
// navigate to the live scan view.
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
  const [repoId, setRepoId] = useState("");
  const [branch, setBranch] = useState("");

  const m = useMutation({
    mutationFn: async () => {
      const repo = repos.data?.find((r) => r.id === repoId);
      const r = await api.post<Scan>("/scans", {
        repo_id: repoId,
        branch: branch.trim() || repo?.default_branch || "main",
      });
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
          disabled={!repoId || m.isPending}
          className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50 inline-flex items-center gap-1.5"
        >
          <PlayIcon className="size-3.5" />
          {m.isPending ? "Starting…" : "Start scan"}
        </button>
      </div>
    </form>
  );
}
