// Repo detail page. Shows metadata, detected languages, scan history,
// trend chart of findings over time, and lets the user trigger a new
// scan or delete the repo.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  PlayIcon,
  Trash2Icon,
  GitBranchIcon,
  HardDriveIcon,
  ServerIcon,
} from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { Repo, Scan } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";
import { BranchSelect } from "@/components/branch-select";
import { FrameworksChips } from "@/components/frameworks-chips";
import { FixableBadge } from "@/components/fixes/fixable-badge";
import { useScanWithPreflight } from "@/components/scan-preflight";
import { useMe, canModify } from "@/lib/me";

// One row per past scan from GET /api/scans/trends?repo_id=&branch=
interface TrendPoint {
  scan_id: string;
  branch: string;
  status: string;
  completed_at: string;
  total: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
}

export const Route = createFileRoute("/_authed/repos/$repoId")({
  component: RepoDetailPage,
});

function RepoDetailPage() {
  const { repoId } = Route.useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const repoQ = useQuery({
    queryKey: ["repo", repoId],
    queryFn: async () => {
      const r = await api.get<Repo>(`/repos/${repoId}`);
      return r.data;
    },
  });

  const scansQ = useQuery({
    queryKey: ["scans", "by-repo", repoId],
    queryFn: async () => {
      const r = await api.get<Scan[]>(`/scans?repo_id=${repoId}&limit=50`);
      return r.data ?? [];
    },
  });

  const trendsQ = useQuery({
    queryKey: ["scans-trends", repoId],
    queryFn: async () => {
      const r = await api.get<TrendPoint[]>(
        `/scans/trends?repo_id=${repoId}&limit=30`,
      );
      return r.data ?? [];
    },
    // Refresh when the user kicks off a new scan from this page.
    refetchInterval: 30_000,
  });

  const [scanBranch, setScanBranch] = useState("");

  const me = useMe();

  // Scanning goes through the image preflight: it prompts to pull any selected
  // scanner whose container image isn't present before starting the scan.
  const scan = useScanWithPreflight();
  function startScanNow() {
    scan.launch({
      repo_id: repoId,
      branch: scanBranch.trim() || repoQ.data?.default_branch || "main",
    });
  }

  const del = useMutation({
    mutationFn: async () => {
      await api.delete(`/repos/${repoId}`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["repos"] });
      toast.success("Repo deleted");
      navigate({ to: "/repos" });
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    },
  });

  if (repoQ.isLoading || !repoQ.data) {
    return (
      <div className="page stack page--mid">
        <CardSkeleton />
      </div>
    );
  }
  const r = repoQ.data;
  const langs = parseLanguages(r.detected_languages);

  return (
    <div className="page stack page--mid">
      {scan.dialog}
      <div className="flex items-start gap-3">
        <Link
          to="/repos"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div className="flex-1 min-w-0">
          <h1 className="text-xl font-semibold flex items-center gap-2">
            {r.source_type === "local" ? (
              <HardDriveIcon className="size-5 text-muted-foreground" />
            ) : r.source_type === "ssh" ? (
              <ServerIcon className="size-5 text-muted-foreground" />
            ) : (
              <GitBranchIcon className="size-5 text-muted-foreground" />
            )}
            {r.name}
            <FixableBadge repoId={repoId} />
          </h1>
          <div className="text-sm text-muted-foreground font-mono break-all mt-1">
            {r.source_path}
          </div>
        </div>
        <div className="flex items-center gap-1">
          <BranchSelect
            repoId={repoId}
            value={scanBranch}
            onChange={setScanBranch}
            defaultBranch={r.default_branch}
          />
          <button
            type="button"
            onClick={startScanNow}
            disabled={scan.busy}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
          >
            <PlayIcon className="size-4" />
            {scan.busy ? "Starting…" : "Scan now"}
          </button>
          {canModify(me.data, r.user_id) && (
            <button
              type="button"
              onClick={() => {
                if (
                  window.confirm(
                    `Delete repo "${r.name}"? This removes it from all collections and deletes its scans. Cannot be undone.`,
                  )
                ) {
                  del.mutate();
                }
              }}
              className="size-9 grid place-items-center rounded-md hover:bg-destructive/10 text-destructive"
              aria-label="Delete"
            >
              <Trash2Icon className="size-4" />
            </button>
          )}
        </div>
      </div>

      <section className="glass-card p-5 grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
        <Field label="Source type" value={r.source_type} mono />
        <Field label="Default branch" value={r.default_branch || "main"} />
        {r.source_type === "ssh" && (
          <>
            <Field label="Remote node" value={r.remote_node_id || "—"} mono />
            <Field label="Remote path" value={r.remote_path || r.source_path} mono />
            <Field label="Last commit" value={r.last_commit_sha || "—"} mono />
            <Field label="Dirty state" value={r.last_dirty_state || "unknown"} />
          </>
        )}
        <Field
          label="Languages"
          value={
            langs.length === 0 ? (
              <em className="text-muted-foreground">not yet detected</em>
            ) : (
              langs
                .map(([l, n]) => `${l} (${n})`)
                .join(", ")
            )
          }
        />
        <Field
          label="Frameworks"
          value={<FrameworksChips raw={r.detected_frameworks} />}
        />
        <Field
          label="First seen"
          value={r.created_at ? new Date(r.created_at).toLocaleString() : "—"}
        />
        <Field
          label="Last detection"
          value={
            r.detected_at ? new Date(r.detected_at).toLocaleString() : "—"
          }
        />
      </section>

      <TrendsSection
        trends={trendsQ.data ?? []}
        loading={trendsQ.isLoading}
        defaultBranch={r.default_branch || "main"}
      />

      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">
          Scans ({scansQ.data?.length ?? 0})
        </h2>
        {scansQ.isLoading ? (
          <div className="text-sm text-muted-foreground">Loading…</div>
        ) : !scansQ.data || scansQ.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No scans yet. Click <strong>Scan now</strong> above to run one.
          </p>
        ) : (
          <ul className="divide-y divide-border/30">
            {scansQ.data.slice(0, 20).map((s) => (
              <li key={s.id} className="py-2 text-sm flex justify-between">
                <Link
                  to="/scans/$scanId"
                  params={{ scanId: s.id }}
                  className="flex-1 min-w-0 hover:underline"
                >
                  <span className="font-mono">{s.id.slice(0, 8)}</span>
                  <span className="text-muted-foreground"> · {s.status}</span>
                  {s.branch && (
                    <span className="text-muted-foreground"> · {s.branch}</span>
                  )}
                </Link>
                <span className="text-xs text-muted-foreground">
                  {s.finding_count ?? 0} findings
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`text-sm ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  );
}

// detected_languages is JSON-encoded: {"go": 219, "python": 2, ...}
function parseLanguages(s: string | undefined): Array<[string, number]> {
  if (!s) return [];
  try {
    const obj = JSON.parse(s) as Record<string, number>;
    return Object.entries(obj).sort(([, a], [, b]) => b - a);
  } catch {
    return [];
  }
}

// TrendsSection renders a stacked-bar chart of findings-by-severity
// over the repo's scan history. With a branch filter, it's the
// "trend on this repo/branch over all the runs" view. The x-axis is
// per-scan timestamp (so multiple scans in one day each get a bar).
// Hovering shows the severity breakdown; clicking a bar would be the
// natural follow-up but we keep it static here — the scan list below
// already provides drill-through.
function TrendsSection({
  trends,
  loading,
  defaultBranch,
}: {
  trends: TrendPoint[];
  loading: boolean;
  defaultBranch: string;
}) {
  const [branchFilter, setBranchFilter] = useState<string>(defaultBranch);
  if (loading) {
    return (
      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">Trend</h2>
        <div className="text-sm text-muted-foreground">Loading trend…</div>
      </section>
    );
  }
  if (!trends || trends.length === 0) {
    return (
      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">Trend</h2>
        <p className="text-sm text-muted-foreground">
          Run two or more scans on this repo to see a trend.
        </p>
      </section>
    );
  }
  // Available branches across the trend window — keep "all" as the default
  // top option, then list each branch we've seen.
  const branches = Array.from(new Set(trends.map((p) => p.branch))).filter(Boolean);
  const filtered =
    branchFilter === "__all__"
      ? trends
      : trends.filter((p) => p.branch === branchFilter);
  const data = filtered.map((p) => ({
    // Short timestamp label so the x-axis fits.
    name: new Date(p.completed_at).toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }),
    scan_id: p.scan_id,
    critical: p.critical,
    high: p.high,
    medium: p.medium,
    low: p.low,
    info: p.info,
    total: p.total,
  }));
  return (
    <section className="glass-card p-5">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-medium">
          Trend ({filtered.length} scan{filtered.length === 1 ? "" : "s"})
        </h2>
        {branches.length > 1 && (
          <select
            value={branchFilter}
            onChange={(e) => setBranchFilter(e.target.value)}
            className="text-xs bg-muted/40 border border-border/40 rounded px-2 h-7"
          >
            <option value="__all__">all branches</option>
            {branches.map((b) => (
              <option key={b} value={b}>
                {b}
              </option>
            ))}
          </select>
        )}
      </div>
      <div className="h-64 w-full">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 5, right: 8, left: 0, bottom: 0 }}>
            <CartesianGrid strokeOpacity={0.1} vertical={false} />
            <XAxis
              dataKey="name"
              tick={{ fontSize: 10 }}
              interval="preserveStartEnd"
            />
            <YAxis tick={{ fontSize: 10 }} allowDecimals={false} />
            <Tooltip
              contentStyle={{
                background: "rgba(20,20,20,0.95)",
                border: "1px solid rgba(255,255,255,0.1)",
                fontSize: 12,
              }}
            />
            <Legend wrapperStyle={{ fontSize: 11 }} iconType="square" />
            {/* Stack severities so the bar height = total. Colors match
                the rest of the UI's severity palette. */}
            <Bar dataKey="critical" stackId="s" fill="#dc2626" name="Critical" />
            <Bar dataKey="high" stackId="s" fill="#f97316" name="High" />
            <Bar dataKey="medium" stackId="s" fill="#eab308" name="Medium" />
            <Bar dataKey="low" stackId="s" fill="#3b82f6" name="Low" />
            <Bar dataKey="info" stackId="s" fill="#71717a" name="Info" />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </section>
  );
}
