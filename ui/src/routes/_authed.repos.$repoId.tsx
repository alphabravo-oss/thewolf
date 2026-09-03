// Repo detail page. Shows metadata, detected languages, scan history,
// trend chart of findings over time, and lets the user trigger a new
// scan or delete the repo.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  CopyIcon,
  DownloadIcon,
  PlayIcon,
  RefreshCwIcon,
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
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, isNotFound } from "@/lib/api";
import type { Finding, Repo, Scan, ScanSchedule } from "@/lib/types";
import { SeverityBadge } from "@/components/severity-badge";
import { CardSkeleton } from "@/components/skeleton";
import { BranchSelect } from "@/components/branch-select";
import { FrameworksChips } from "@/components/frameworks-chips";
import { FixableBadge } from "@/components/fixes/fixable-badge";
import { useScanWithPreflight } from "@/components/scan-preflight";
import { DeleteWithRecordsDialog } from "@/components/delete-with-records-dialog";
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
  const [profile, setProfile] = useState<"fast" | "standard" | "release">(
    "standard",
  );
  const [deleteOpen, setDeleteOpen] = useState(false);
  const latestCompleted = scansQ.data?.find((s) => s.status === "completed");
  const gateQ = useQuery({
    queryKey: ["scan", latestCompleted?.id, "gate"],
    queryFn: async () => {
      const res = await api.get<ScanGate>(`/scans/${latestCompleted!.id}/gate`);
      return res.data;
    },
    enabled: !!latestCompleted?.id,
  });

  const me = useMe();

  // Scanning goes through the image preflight: it prompts to pull any selected
  // scanner whose container image isn't present before starting the scan.
  const scan = useScanWithPreflight();
  function startScanNow() {
    scan.launch({
      repo_id: repoId,
      branch: scanBranch.trim() || repoQ.data?.default_branch || "main",
      profile,
    });
  }

  const sync = useMutation({
    mutationFn: async () => {
      const branch =
        scanBranch.trim() || repoQ.data?.default_branch || "main";
      const r = await api.post<SyncRepoResult>(
        `/repos/${repoId}/sync?branch=${encodeURIComponent(branch)}`,
      );
      return r.data;
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["repo", repoId] });
      const sha = data.last_commit_sha
        ? data.last_commit_sha.slice(0, 12)
        : "unknown";
      toast.success(
        data.changed
          ? `Synced ${data.branch} to ${sha}`
          : `${data.branch} already at ${sha}`,
      );
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Sync failed");
    },
  });

  const del = useMutation({
    mutationFn: async (purge: boolean) => {
      await api.delete(`/repos/${repoId}${purge ? "?purge=true" : ""}`);
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
      <DeleteWithRecordsDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        kind="repo"
        name={r.name}
        recordCount={scansQ.data?.length}
        pending={del.isPending}
        onConfirm={(purge) => del.mutate(purge)}
      />
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
          <p className="text-xs text-muted-foreground mt-1">
            {latestCompleted ? (
              <GateLine gate={gateQ.data} />
            ) : (
              "No scan yet"
            )}
          </p>
        </div>
        <div className="flex items-center gap-1 flex-wrap justify-end">
          <BranchSelect
            repoId={repoId}
            value={scanBranch}
            onChange={setScanBranch}
            defaultBranch={r.default_branch}
          />
          <select
            aria-label="Scan profile"
            value={profile}
            onChange={(e) =>
              setProfile(e.target.value as "fast" | "standard" | "release")
            }
            className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          >
            <option value="fast">Fast</option>
            <option value="standard">Standard</option>
            <option value="release">Release</option>
          </select>
          {canModify(me.data, r.user_id) && (
            <button
              type="button"
              onClick={() => sync.mutate()}
              disabled={sync.isPending || scan.busy}
              title="Pull latest commits without starting a scan"
              className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm font-medium hover:bg-muted/50 disabled:opacity-50"
            >
              <RefreshCwIcon
                className={`size-4 ${sync.isPending ? "animate-spin" : ""}`}
              />
              {sync.isPending ? "Syncing…" : "Sync"}
            </button>
          )}
          <button
            type="button"
            onClick={startScanNow}
            disabled={scan.busy}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
          >
            <PlayIcon className="size-4" />
            {scan.busy ? "Starting…" : "Scan this"}
          </button>
          <button
            type="button"
            onClick={() => {
              void navigator.clipboard?.writeText(ciSnippet(repoId));
              toast.success("CI snippet copied");
            }}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm font-medium hover:bg-muted/50"
          >
            <CopyIcon className="size-4" />
            Copy CI snippet
          </button>
          {latestCompleted ? (
            <a
              href={`/api/v1/scans/${latestCompleted.id}/sarif`}
              className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm font-medium hover:bg-muted/50"
            >
              <DownloadIcon className="size-4" />
              SARIF
            </a>
          ) : null}
          {canModify(me.data, r.user_id) && (
            <button
              type="button"
              onClick={() => setDeleteOpen(true)}
              className="size-9 grid place-items-center rounded-md hover:bg-destructive/10 text-destructive"
              aria-label="Delete"
            >
              <Trash2Icon className="size-4" />
            </button>
          )}
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Point GitHub Settings → Webhooks at{" "}
        <code className="font-mono">
          {`${typeof window !== "undefined" ? window.location.origin : ""}/api/v1/webhooks/github`}
        </code>{" "}
        (push + pull_request). Secret is Settings → Scanners (enable Advanced for webhook secret).
      </p>

      <section className="glass-card p-5 grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
        <Field label="Source type" value={r.source_type} mono />
        <Field label="Default branch" value={r.default_branch || "main"} />
        {r.source_type === "ssh" && (
          <>
            <Field label="Remote node" value={r.remote_node_id || "—"} mono />
            <Field label="Remote path" value={r.remote_path || r.source_path} mono />
          </>
        )}
        <Field label="Last commit" value={r.last_commit_sha || "—"} mono />
        <Field
          label="Dirty state"
          value={r.last_dirty_state || (r.last_commit_sha ? "unknown" : "—")}
        />
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

      <ScheduleCard
        repoId={repoId}
        defaultBranch={r.default_branch || "main"}
      />

      <CurrentFindings
        repoId={repoId}
        latestScanId={latestCompleted?.id}
      />

      <RepoSuppressions repoId={repoId} />

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
            No scans yet. Click <strong>Scan this</strong> above to run one.
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

const INTERVALS: { minutes: number; label: string }[] = [
  { minutes: 15, label: "15m" },
  { minutes: 60, label: "1h" },
  { minutes: 360, label: "6h" },
  { minutes: 1440, label: "daily" },
];

function ScheduleCard({
  repoId,
  defaultBranch,
}: {
  repoId: string;
  defaultBranch: string;
}) {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["schedules"],
    queryFn: async () => {
      try {
        const r = await api.get<ScanSchedule[] | { items: ScanSchedule[] }>(
          "/schedules",
        );
        if (Array.isArray(r.data)) return r.data;
        return r.data?.items ?? [];
      } catch (e) {
        if (isNotFound(e)) return null;
        throw e;
      }
    },
  });
  const existing = q.data?.find((s) => s.repo_id === repoId);
  const [interval, setInterval] = useState(60);
  const [profile, setProfile] = useState<"fast" | "standard" | "release">(
    "standard",
  );
  const [quietStart, setQuietStart] = useState("");
  const [quietEnd, setQuietEnd] = useState("");
  const [enabled, setEnabled] = useState(true);

  useEffect(() => {
    if (!existing) return;
    setInterval(existing.interval_minutes || 60);
    if (
      existing.profile === "fast" ||
      existing.profile === "standard" ||
      existing.profile === "release"
    ) {
      setProfile(existing.profile);
    }
    setQuietStart(existing.quiet_start || "");
    setQuietEnd(existing.quiet_end || "");
    setEnabled(existing.enabled);
  }, [existing]);

  const save = useMutation({
    mutationFn: async () => {
      const body: Partial<ScanSchedule> = {
        repo_id: repoId,
        interval_minutes: interval,
        branch: defaultBranch,
        profile,
        quiet_start: quietStart || undefined,
        quiet_end: quietEnd || undefined,
        enabled,
      };
      if (existing?.id) {
        return (
          await api.put<ScanSchedule>(`/schedules/${existing.id}`, {
            ...body,
            id: existing.id,
          })
        ).data;
      }
      return (await api.post<ScanSchedule>("/schedules", body)).data;
    },
    onSuccess: () => {
      toast.success("Schedule saved");
      qc.invalidateQueries({ queryKey: ["schedules"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Schedule save failed"),
  });

  if (q.data === null) return null;

  return (
    <section className="glass-card p-5 space-y-3">
      <h2 className="text-sm font-medium">Schedule</h2>
      <div className="flex flex-wrap items-end gap-3 text-sm">
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Interval</span>
          <select
            value={interval}
            onChange={(e) => setInterval(Number(e.target.value))}
            className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          >
            {INTERVALS.map((opt) => (
              <option key={opt.minutes} value={opt.minutes}>
                {opt.label}
              </option>
            ))}
          </select>
        </label>
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Profile</span>
          <select
            value={profile}
            onChange={(e) =>
              setProfile(e.target.value as "fast" | "standard" | "release")
            }
            className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          >
            <option value="fast">Fast</option>
            <option value="standard">Standard</option>
            <option value="release">Release</option>
          </select>
        </label>
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Quiet start</span>
          <input
            type="time"
            value={quietStart}
            onChange={(e) => setQuietStart(e.target.value)}
            className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </label>
        <label className="space-y-1">
          <span className="block text-xs text-muted-foreground">Quiet end</span>
          <input
            type="time"
            value={quietEnd}
            onChange={(e) => setQuietEnd(e.target.value)}
            className="h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </label>
        <label className="inline-flex items-center gap-2 h-9 text-sm">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          Enabled
        </label>
        <button
          type="button"
          onClick={() => save.mutate()}
          disabled={save.isPending || q.isLoading}
          className="inline-flex items-center h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
      </div>
    </section>
  );
}

function CurrentFindings({
  repoId,
  latestScanId,
}: {
  repoId: string;
  latestScanId?: string;
}) {
  const q = useQuery({
    queryKey: ["findings", "repo", repoId],
    queryFn: async () => {
      const r = await api.get<Finding[]>(
        `/findings?repo_id=${repoId}&per_page=50&sort=severity&order=desc`,
      );
      return { rows: r.data ?? [], total: r.meta?.total ?? r.data?.length ?? 0 };
    },
  });
  const rows = q.data?.rows ?? [];
  const total = q.data?.total ?? 0;
  return (
    <section className="glass-card overflow-hidden">
      <div className="px-5 py-3 border-b border-border flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-medium">Current findings</h2>
          <p className="text-[11px] text-muted-foreground">
            Open on the latest scan of each branch. Full detail lives on the
            scan page.
          </p>
        </div>
        {latestScanId ? (
          <Link
            to="/scans/$scanId"
            params={{ scanId: latestScanId }}
            className="text-xs hover:underline shrink-0"
          >
            Open latest scan
          </Link>
        ) : null}
      </div>
      {q.isLoading ? (
        <p className="px-5 py-3 text-sm text-muted-foreground">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="px-5 py-3 text-sm text-muted-foreground">
          No open findings on the current scans.
        </p>
      ) : (
        <>
          <ul className="divide-y divide-border/20 text-sm">
            {rows.map((f) => (
              <li key={f.id}>
                <Link
                  to="/findings/$findingId"
                  params={{ findingId: f.id }}
                  className="px-5 py-2 flex items-start gap-3 hover:bg-muted/20"
                >
                  <SeverityBadge severity={f.severity} />
                  <div className="min-w-0 flex-1">
                    <div className="truncate">{f.title}</div>
                    <div className="text-[11px] text-muted-foreground font-mono truncate">
                      {f.tool_name}
                      {f.file_path ? ` · ${f.file_path}` : ""}
                    </div>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          {total > rows.length ? (
            <p className="px-5 py-2 text-[11px] text-muted-foreground border-t border-border">
              Showing {rows.length} of {total.toLocaleString()}.{" "}
              {latestScanId ? "Open the latest scan for the full list." : null}
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}

interface RepoSuppression {
  id: string;
  scope_type: string;
  scope_value: string;
  reason: string;
  status: string;
  created_at: string;
}

function RepoSuppressions({ repoId }: { repoId: string }) {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["suppressions", repoId],
    queryFn: async () => {
      const r = await api.get<RepoSuppression[]>(
        `/suppressions?repo_id=${repoId}`,
      );
      return r.data ?? [];
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/suppressions/${id}`),
    onSuccess: () => {
      toast.success("Suppression revoked — next scan can show it again");
      qc.invalidateQueries({ queryKey: ["suppressions", repoId] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Revoke failed"),
  });
  const rows = q.data ?? [];
  return (
    <section className="glass-card overflow-hidden">
      <div className="px-5 py-3 border-b border-border">
        <h2 className="text-sm font-medium">Repo suppressions</h2>
        <p className="text-[11px] text-muted-foreground">
          These hide matching findings on every future scan of this repo. Agent
          “muted this run” rows are not listed here unless a durable
          suppression was written. Revoke anything you still want to see.
        </p>
      </div>
      {q.isLoading ? (
        <p className="px-5 py-3 text-sm text-muted-foreground">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="px-5 py-3 text-sm text-muted-foreground">
          No durable suppressions.
        </p>
      ) : (
        <ul className="divide-y divide-border/20 text-sm">
          {rows.map((s) => (
            <li
              key={s.id}
              className="px-5 py-2 flex items-start justify-between gap-3"
            >
              <div className="min-w-0">
                <div className="font-mono text-xs truncate">
                  {s.scope_type}: {s.scope_value}
                </div>
                <p className="mt-0.5 text-[11px] text-muted-foreground truncate">
                  {s.reason}
                </p>
              </div>
              <button
                type="button"
                onClick={() => revoke.mutate(s.id)}
                disabled={revoke.isPending}
                className="shrink-0 text-xs text-destructive hover:underline disabled:opacity-50"
              >
                Revoke
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
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

interface SyncRepoResult extends Repo {
  branch: string;
  previous_commit_sha?: string;
  changed: boolean;
}

type ScanGate = {
  evaluation?: { status?: string };
  result?: { status?: string };
  finding_count?: number;
};

function GateLine({ gate }: { gate?: ScanGate }) {
  const status = gate?.evaluation?.status ?? gate?.result?.status;
  if (!status) return <>Latest scan completed</>;
  const tone =
    status === "fail" || status === "failed"
      ? "text-status-error"
      : status === "warn" || status === "warning"
        ? "text-status-warning"
        : "text-status-success";
  return (
    <>
      Gate <span className={`${tone} font-medium`}>{status}</span>
      {gate?.finding_count != null
        ? ` · ${gate.finding_count.toLocaleString()} findings`
        : ""}
    </>
  );
}

function ciSnippet(repoId: string) {
  return `name: wolf
on: [push, pull_request]
jobs:
  scan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: |
          curl -fsS -X POST "$WOLF_URL/api/v1/scans" \\
            -H "Authorization: Bearer $WOLF_TOKEN" \\
            -H "Content-Type: application/json" \\
            -d '{"repo_id":"${repoId}","profile":"fast"}'
          wolf scan gate --fail-exit-code
# wolf scan gate --fail-exit-code fails the job on new findings and secrets.`;
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
            className="text-xs bg-muted/40 border border-border rounded px-2 h-7"
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
