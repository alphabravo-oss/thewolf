"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { SeverityBadge } from "@/components/severity-badge";
import { StatusBadge } from "@/components/status-badge";
import { TrendChart } from "@/components/trend-chart";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import { ScanConfirmDialog, getLangColor } from "@/components/scan-confirm-dialog";
import { ConfirmDeleteDialog } from "@/components/confirm-delete-dialog";
import api from "@/lib/api";
import type { Collection, Repo, Scan, ScanConfig, Severity, SourceType, CollectionMetrics } from "@/lib/types";

interface BrowseEntry {
  name: string;
  path: string;
  is_dir: boolean;
  is_git: boolean;
}

interface BrowseResult {
  current: string;
  parent: string;
  entries: BrowseEntry[];
}

interface CollectionDetail extends Collection {
  repos: Repo[];
  scans: Scan[];
  recent_scans: Scan[]; // backwards compat
}

function formatDuration(start?: string, end?: string): string {
  if (!start) return "\u2014";
  const s = new Date(start).getTime();
  const e = end ? new Date(end).getTime() : Date.now();
  const sec = Math.round((e - s) / 1000);
  if (sec < 60) return sec + "s";
  return Math.floor(sec / 60) + "m " + (sec % 60) + "s";
}

function parseToolArray(val: string[] | string | undefined | null): string[] {
  if (!val) return [];
  if (Array.isArray(val)) return val;
  try { const parsed = JSON.parse(val); return Array.isArray(parsed) ? parsed : []; } catch { return []; }
}

export default function CollectionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [scanConfirmOpen, setScanConfirmOpen] = useState(false);
  const [configOnlyMode, setConfigOnlyMode] = useState(false);
  const [scanConfig, setScanConfig] = useState<ScanConfig>({});

  // Scan state
  const [scanning, setScanning] = useState(false);
  const [scanProgress, setScanProgress] = useState<string[]>([]);

  // Add repo dialog state
  const [addRepoOpen, setAddRepoOpen] = useState(false);
  const [repoSourceType, setRepoSourceType] = useState<SourceType>("local");
  const [repoPath, setRepoPath] = useState("");
  const [repoName, setRepoName] = useState("");
  const [repoBranch, setRepoBranch] = useState("main");
  const [addingRepo, setAddingRepo] = useState(false);

  // (trendData and fixRate are fetched via useQuery below)

  // Branch filter
  const [selectedBranch, setSelectedBranch] = useState<string>("all");
  const handleBranchChange = (branch: string) => {
    setSelectedBranch(branch);
    setScanPage(1);
  };

  // Scan pagination
  const [scanPage, setScanPage] = useState(1);
  const scansPerPage = 10;

  // Edit collection dialog state
  const [editOpen, setEditOpen] = useState(false);
  const [editName, setEditName] = useState("");
  const [editDescription, setEditDescription] = useState("");
  const [editSaving, setEditSaving] = useState(false);

  // Delete collection dialog state
  const [deleteCollectionOpen, setDeleteCollectionOpen] = useState(false);
  const [deletingCollection, setDeletingCollection] = useState(false);

  // Delete repo dialog state
  const [deleteRepoOpen, setDeleteRepoOpen] = useState(false);
  const [deletingRepo, setDeletingRepo] = useState(false);
  const [repoToDelete, setRepoToDelete] = useState<Repo | null>(null);

  // File browser state
  const [browsing, setBrowsing] = useState(false);
  const [browseEntries, setBrowseEntries] = useState<BrowseEntry[]>([]);
  const [browseCurrent, setBrowseCurrent] = useState("");
  const [browseParent, setBrowseParent] = useState("");
  const [browseLoading, setBrowseLoading] = useState(false);

  // Fetch collection data with TanStack Query
  const { data: collection, isLoading: loading } = useQuery({
    queryKey: ["collection", id],
    queryFn: async () => {
      const res = await api.get<CollectionDetail>(`/collections/${id}`);
      const raw = res.data as unknown as Record<string, unknown>;
      if (raw.collection) {
        const col = raw.collection as CollectionDetail;
        col.repos = (raw.repos as Repo[]) ?? [];
        col.scans = (raw.scans as Scan[]) ?? (raw.recent_scans as Scan[]) ?? [];
        col.recent_scans = col.scans;
        return col;
      }
      return res.data;
    },
    refetchInterval: (query) => {
      const col = query.state.data;
      const scans = col?.scans || col?.recent_scans || [];
      const hasRunning = scans.some(
        (s: Scan) => s.status === "running" || s.status === "pending"
      );
      return hasRunning ? 3_000 : 10_000;
    },
  });

  const { data: metrics = null } = useQuery({
    queryKey: ["collection-metrics", id, selectedBranch],
    queryFn: async () => {
      const params = selectedBranch && selectedBranch !== "all" ? `?branch=${encodeURIComponent(selectedBranch)}` : "";
      const res = await api.get<CollectionMetrics>(`/collections/${id}/metrics${params}`);
      return res.data ?? null;
    },
    refetchInterval: 15_000,
  });

  const loadCollection = () => {
    queryClient.invalidateQueries({ queryKey: ["collection", id] });
  };

  useEffect(() => {
    if (collection?.scan_config) {
      const cfg = typeof collection.scan_config === "string"
        ? JSON.parse(collection.scan_config || "{}")
        : collection.scan_config;
      setScanConfig(cfg);
    }
  }, [collection?.scan_config]);

  const handleScanAll = async (configOverride?: ScanConfig) => {
    if (!collection?.repos?.length) return;
    const cfg = configOverride ?? scanConfig;
    setScanning(true);
    setScanProgress([]);
    const scanIds: string[] = [];

    for (const repo of collection.repos) {
      setScanProgress((prev) => [...prev, `Starting scan for ${repo.name}...`]);
      try {
        const res = await api.post<Scan>("/scans", {
          repo_id: repo.id,
          collection_id: id,
          branch: cfg.branch_overrides?.[repo.id] || undefined,
          disabled_tools: cfg.disabled_tools?.length ? cfg.disabled_tools : undefined,
          ai_enabled: cfg.ai_enabled || false,
          ai_engine: cfg.ai_engine || undefined,
          ai_model: cfg.ai_model || undefined,
        });
        scanIds.push(res.data.id);
        setScanProgress((prev) => [
          ...prev.slice(0, -1),
          `${repo.name}: scan created (${res.data.id.slice(0, 8)})`,
        ]);
      } catch {
        setScanProgress((prev) => [
          ...prev.slice(0, -1),
          `${repo.name}: failed to start scan`,
        ]);
      }
    }

    setScanProgress((prev) => [...prev, "All scans started."]);
    if (collection.repos.length === 1 && scanIds.length === 1) {
      router.push(`/scans/${scanIds[0]}/live`);
    } else {
      setTimeout(() => {
        loadCollection();
        setScanning(false);
      }, 1500);
    }
  };

  const handleAddRepo = async () => {
    if (!id) return;
    setAddingRepo(true);
    try {
      const repoRes = await api.post<Repo>("/repos", {
        name: repoName || repoPath.split("/").pop() || "repo",
        source_type: repoSourceType,
        source_path: repoPath,
        default_branch: repoBranch,
      });
      await api.post(`/collections/${id}/repos`, {
        repo_id: repoRes.data.id,
      });
      setAddRepoOpen(false);
      setRepoPath("");
      setRepoName("");
      setRepoBranch("main");
      setRepoSourceType("local");
      setBrowsing(false);
      loadCollection();
    } catch {
      // error handled by api layer
    } finally {
      setAddingRepo(false);
    }
  };

  const browseDir = async (path?: string) => {
    setBrowseLoading(true);
    try {
      const params = path ? `?path=${encodeURIComponent(path)}` : "";
      const res = await api.get<BrowseResult>(`/browse${params}`);
      setBrowseEntries(res.data.entries ?? []);
      setBrowseCurrent(res.data.current);
      setBrowseParent(res.data.parent);
      setBrowsing(true);
    } catch {
      // stay on manual input
    } finally {
      setBrowseLoading(false);
    }
  };

  const selectBrowseEntry = (entry: BrowseEntry) => {
    if (entry.is_git) {
      setRepoPath(entry.path);
      if (!repoName) setRepoName(entry.name);
      setBrowsing(false);
    } else {
      browseDir(entry.path);
    }
  };

  const openEditDialog = () => {
    if (!collection) return;
    setEditName(collection.name);
    setEditDescription(collection.description || "");
    setEditOpen(true);
  };

  const handleEditSave = async () => {
    if (!editName.trim()) return;
    setEditSaving(true);
    try {
      await api.put(`/collections/${id}`, {
        name: editName.trim(),
        description: editDescription.trim(),
        scan_config: typeof collection?.scan_config === "string"
          ? collection.scan_config
          : JSON.stringify(collection?.scan_config || {}),
      });
      setEditOpen(false);
      loadCollection();
    } catch {
      // error handled by api layer
    } finally {
      setEditSaving(false);
    }
  };

  const handleDeleteCollection = async () => {
    setDeletingCollection(true);
    try {
      await api.delete(`/collections/${id}`);
      router.push("/");
    } catch {
      // error handled by api layer
    } finally {
      setDeletingCollection(false);
    }
  };

  const handleDeleteRepo = async () => {
    if (!repoToDelete) return;
    setDeletingRepo(true);
    try {
      await api.delete(`/repos/${repoToDelete.id}`);
      setDeleteRepoOpen(false);
      setRepoToDelete(null);
      loadCollection();
    } catch {
      // error handled by api layer
    } finally {
      setDeletingRepo(false);
    }
  };

  if (loading) return <LoadingSpinner />;
  if (!collection) return <EmptyState title="Collection not found" />;

  const severities: Severity[] = ["critical", "high", "medium", "low", "info"];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-3xl font-bold">{collection.name}</h1>
            <Button variant="ghost" size="sm" onClick={openEditDialog} className="text-muted-foreground h-8 px-2">
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
              </svg>
            </Button>
          </div>
          <p className="text-muted-foreground">{collection.description || <span className="italic">No description</span>}</p>
        </div>
        <div className="flex gap-2">
          <Button
            onClick={() => {
              setConfigOnlyMode(false);
              setScanConfirmOpen(true);
            }}
            disabled={scanning || !collection.repos?.length}
          >
            {scanning ? "Scanning..." : "Scan All"}
          </Button>
          <Button variant="outline" onClick={() => {
            setConfigOnlyMode(true);
            setScanConfirmOpen(true);
          }}>
            Configure
          </Button>
          <Button variant="outline" onClick={() => setAddRepoOpen(true)}>
            Add Repo
          </Button>
          <Button variant="outline" asChild>
            <Link href={`/findings?collection=${id}${selectedBranch !== "all" ? `&branch=${encodeURIComponent(selectedBranch)}` : ""}`}>View Findings</Link>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive hover:bg-destructive/10 h-8 w-8 p-0"
            onClick={() => setDeleteCollectionOpen(true)}
          >
            <Trash2 className="w-4 h-4" />
          </Button>
        </div>
      </div>

      {/* Branch selector */}
      {metrics && metrics.branches && metrics.branches.length > 0 && (
        <div className="flex items-center gap-3">
          <Label className="text-sm font-medium text-muted-foreground whitespace-nowrap">Branch</Label>
          <Select value={selectedBranch} onValueChange={handleBranchChange}>
            <SelectTrigger className="w-[200px]">
              <SelectValue placeholder="All branches" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All branches</SelectItem>
              {metrics.branches.map((branch) => (
                <SelectItem key={branch} value={branch}>
                  {branch}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {selectedBranch !== "all" && (
            <span className="text-xs text-muted-foreground">
              Showing scans and metrics for <span className="font-mono font-medium">{selectedBranch}</span> only
            </span>
          )}
        </div>
      )}

      {/* Scan progress */}
      {scanProgress.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Scan Progress</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-1 text-sm font-mono">
              {scanProgress.map((msg, i) => (
                <div key={i} className="text-muted-foreground">{msg}</div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Current Snapshot */}
      {metrics && metrics.snapshot.total_findings > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle>Current Snapshot</CardTitle>
            <CardDescription>
              Based on latest scan per repository and branch ({metrics.snapshot.repos_scanned} repo{metrics.snapshot.repos_scanned !== 1 ? "s" : ""}, {metrics.snapshot.branches_scanned} branch{metrics.snapshot.branches_scanned !== 1 ? "es" : ""})
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="flex items-center gap-6">
              <div className="text-center">
                <span className="text-3xl font-bold">{metrics.snapshot.by_status?.open ?? metrics.snapshot.total_findings}</span>
                <p className="text-xs text-muted-foreground mt-1">Open</p>
              </div>
              <div className="h-10 border-l" />
              <div className="flex gap-3">
                {severities.map((sev) => (
                  <div key={sev} className="flex items-center gap-1.5">
                    <SeverityBadge severity={sev} />
                    <span className="text-sm font-semibold tabular-nums">
                      {metrics.snapshot.by_severity?.[sev] ?? 0}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Trend chart + Resolution rate */}
      {metrics && (metrics.trends.length > 0 || metrics.resolution_rate.total_unique_fingerprints > 0) && (
        <div className="grid gap-4 md:grid-cols-3">
          <div className="md:col-span-2">
            {metrics.trends.length > 0 && (
              <TrendChart
                data={metrics.trends}
                title="Collection Trends"
                height={220}
              />
            )}
          </div>
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base font-medium">Resolution Rate</CardTitle>
              <CardDescription className="text-xs">
                Based on unique finding fingerprints
              </CardDescription>
            </CardHeader>
            <CardContent>
              {metrics.resolution_rate.total_unique_fingerprints > 0 ? (
                <div className="flex flex-col items-center justify-center gap-2 py-4">
                  <span className="text-4xl font-bold">
                    {Math.round(metrics.resolution_rate.rate * 100)}%
                  </span>
                  <span className="text-sm text-muted-foreground text-center">
                    {metrics.resolution_rate.resolved} resolved of {metrics.resolution_rate.total_unique_fingerprints} unique
                  </span>
                  <div className="w-full bg-muted rounded-full h-2.5 mt-2">
                    <div
                      className="bg-primary rounded-full h-2.5 transition-all"
                      style={{
                        width: `${Math.round(metrics.resolution_rate.rate * 100)}%`,
                      }}
                    />
                  </div>
                  <div className="flex gap-3 text-xs text-muted-foreground mt-1">
                    <span>{metrics.resolution_rate.open} open</span>
                    <span>{metrics.resolution_rate.triaged} triaged</span>
                    <span>{metrics.resolution_rate.suppressed} suppressed</span>
                  </div>
                </div>
              ) : (
                <p className="text-sm text-muted-foreground text-center py-8">
                  No findings data yet.
                </p>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* Per-repo health grid */}
      {metrics && metrics.snapshot.latest_scans.length > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base">Repository Health</CardTitle>
            <CardDescription className="text-xs">Latest scan per repository and branch</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <table className="w-full">
              <thead>
                <tr className="border-b bg-muted/40">
                  <th className="text-left text-xs font-medium text-muted-foreground px-4 py-2">Repository</th>
                  <th className="text-left text-xs font-medium text-muted-foreground px-4 py-2">Branch</th>
                  <th className="text-right text-xs font-medium text-muted-foreground px-4 py-2">Findings</th>
                  <th className="text-right text-xs font-medium text-muted-foreground px-4 py-2">Severity</th>
                  <th className="text-right text-xs font-medium text-muted-foreground px-4 py-2">Scanned</th>
                </tr>
              </thead>
              <tbody>
                {metrics.snapshot.latest_scans.map((ls) => (
                  <tr
                    key={`${ls.repo_id}-${ls.branch}`}
                    className="border-b last:border-0 hover:bg-muted/30 transition-colors cursor-pointer"
                    onClick={() => router.push(`/scans/${ls.scan_id}`)}
                  >
                    <td className="px-4 py-2.5 text-sm font-medium">{ls.repo_name}</td>
                    <td className="px-4 py-2.5 text-sm font-mono text-muted-foreground">{ls.branch}</td>
                    <td className="px-4 py-2.5 text-right text-sm font-semibold tabular-nums">{ls.finding_count}</td>
                    <td className="px-4 py-2.5 text-right">
                      <div className="flex justify-end gap-1">
                        {severities.map((sev) => {
                          const count = ls.by_severity?.[sev] ?? 0;
                          if (count === 0) return null;
                          return (
                            <span key={sev} className="flex items-center gap-0.5">
                              <SeverityBadge severity={sev} />
                              <span className="text-xs tabular-nums">{count}</span>
                            </span>
                          );
                        })}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs text-muted-foreground whitespace-nowrap">
                      {ls.completed_at
                        ? new Date(ls.completed_at).toLocaleDateString(undefined, {
                            month: "short",
                            day: "numeric",
                            hour: "2-digit",
                            minute: "2-digit",
                          })
                        : "\u2014"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </CardContent>
        </Card>
      )}

      <div>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-semibold">Repositories</h2>
          {collection.repos?.length > 0 && (
            <span className="text-sm text-muted-foreground">
              {collection.repos.length} repo{collection.repos.length !== 1 ? "s" : ""}
            </span>
          )}
        </div>
        {(!collection.repos || collection.repos.length === 0) ? (
          <EmptyState
            title="No repositories"
            description="Add repositories to this collection to start scanning."
            action={
              <Button onClick={() => setAddRepoOpen(true)}>
                Add Repository
              </Button>
            }
          />
        ) : (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {collection.repos.map((repo) => {
              const detectedLangs: Record<string, number> = (() => {
                try { return repo.detected_languages ? JSON.parse(repo.detected_languages) : {}; }
                catch { return {}; }
              })();
              const detectedFrameworks: string[] = (() => {
                try { const v = repo.detected_frameworks ? JSON.parse(repo.detected_frameworks) : []; return Array.isArray(v) ? v : []; }
                catch { return []; }
              })();
              const hasDetection = !!repo.detected_at;

              return (
                <Card key={repo.id}>
                  <CardHeader>
                    <CardTitle className="text-base">{repo.name}</CardTitle>
                    <CardDescription>
                      <StatusBadge status={repo.source_type} /> — {repo.default_branch}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-2">
                    <p className="text-xs text-muted-foreground truncate">
                      {repo.source_path}
                    </p>
                    {!hasDetection ? (
                      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                        <span className="inline-block h-3 w-3 rounded-full border-2 border-muted-foreground border-t-transparent animate-spin" />
                        Detecting...
                      </div>
                    ) : (
                      <>
                        {Object.keys(detectedLangs).length > 0 && (
                          <div className="flex flex-wrap gap-1">
                            {Object.entries(detectedLangs)
                              .sort(([, a], [, b]) => b - a)
                              .map(([lang, count]) => (
                                <span
                                  key={lang}
                                  className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] font-medium ${getLangColor(lang)}`}
                                >
                                  {lang}
                                  <span className="opacity-60">{count}</span>
                                </span>
                              ))}
                          </div>
                        )}
                        {detectedFrameworks.length > 0 && (
                          <div className="flex flex-wrap gap-1">
                            {detectedFrameworks.map((fw) => (
                              <span
                                key={fw}
                                className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-muted text-muted-foreground"
                              >
                                {fw}
                              </span>
                            ))}
                          </div>
                        )}
                      </>
                    )}
                    <div className="pt-2 flex justify-end">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive hover:text-destructive hover:bg-destructive/10 h-7 w-7 p-0"
                        onClick={() => {
                          setRepoToDelete(repo);
                          setDeleteRepoOpen(true);
                        }}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        )}
      </div>

      {/* Scans */}
      {(() => {
        const rawScans = collection.scans || collection.recent_scans || [];
        const allScans = selectedBranch === "all" ? rawScans : rawScans.filter((s: Scan) => s.branch === selectedBranch);
        const totalPages = Math.max(1, Math.ceil(allScans.length / scansPerPage));
        const paged = allScans.slice((scanPage - 1) * scansPerPage, scanPage * scansPerPage);
        return (
          <div>
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-xl font-semibold">Scans</h2>
              <span className="text-sm text-muted-foreground">
                {allScans.length} scan{allScans.length !== 1 ? "s" : ""}
              </span>
            </div>
            {allScans.length === 0 ? (
              <Card>
                <CardContent className="py-8 text-center text-sm text-muted-foreground">
                  No scans yet. Run a scan to see results here.
                </CardContent>
              </Card>
            ) : (
              <Card>
                <CardContent className="p-0">
                  <table className="w-full">
                    <thead>
                      <tr className="border-b bg-muted/40">
                        <th className="text-left text-xs font-medium text-muted-foreground px-4 py-3">Status</th>
                        <th className="text-left text-xs font-medium text-muted-foreground px-4 py-3">Repository</th>
                        <th className="text-left text-xs font-medium text-muted-foreground px-4 py-3">Branch</th>
                        <th className="text-right text-xs font-medium text-muted-foreground px-4 py-3">Tools</th>
                        <th className="text-right text-xs font-medium text-muted-foreground px-4 py-3">Findings</th>
                        <th className="text-right text-xs font-medium text-muted-foreground px-4 py-3">Duration</th>
                        <th className="text-right text-xs font-medium text-muted-foreground px-4 py-3">Date</th>
                      </tr>
                    </thead>
                    <tbody>
                      {paged.map((scan: Scan) => {
                        const selected = parseToolArray(scan.tools_selected);
                        const completed = parseToolArray(scan.tools_completed);
                        const failed = parseToolArray(scan.tools_failed);
                        return (
                          <tr
                            key={scan.id}
                            className="border-b last:border-0 hover:bg-muted/30 transition-colors cursor-pointer"
                            onClick={() =>
                              router.push(
                                scan.status === "running"
                                  ? `/scans/${scan.id}/live`
                                  : `/scans/${scan.id}`
                              )
                            }
                          >
                            <td className="px-4 py-3.5">
                              <StatusBadge status={scan.status} />
                            </td>
                            <td className="px-4 py-3.5">
                              <span className="text-sm font-medium">
                                {scan.repo?.name || scan.id.slice(0, 8)}
                              </span>
                            </td>
                            <td className="px-4 py-3.5">
                              <span className="text-sm font-mono text-muted-foreground">
                                {scan.branch}
                              </span>
                            </td>
                            <td className="px-4 py-3.5 text-right text-sm tabular-nums">
                              {selected.length > 0 ? (
                                <>
                                  {completed.length}/{selected.length}
                                  {failed.length > 0 && (
                                    <span className="text-destructive text-xs ml-1">
                                      ({failed.length} failed)
                                    </span>
                                  )}
                                </>
                              ) : (
                                <span className="text-muted-foreground">&mdash;</span>
                              )}
                            </td>
                            <td className="px-4 py-3.5 text-right text-sm font-semibold tabular-nums">
                              {scan.finding_count}
                            </td>
                            <td className="px-4 py-3.5 text-right text-sm font-mono text-muted-foreground tabular-nums">
                              {formatDuration(scan.started_at, scan.completed_at)}
                            </td>
                            <td className="px-4 py-3.5 text-right text-sm text-muted-foreground whitespace-nowrap">
                              {new Date(scan.created_at).toLocaleDateString(undefined, {
                                month: "short",
                                day: "numeric",
                                hour: "2-digit",
                                minute: "2-digit",
                              })}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </CardContent>
                {totalPages > 1 && (
                  <div className="flex items-center justify-between px-4 py-3 border-t">
                    <span className="text-sm text-muted-foreground">
                      Page {scanPage} of {totalPages}
                    </span>
                    <div className="flex items-center gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={scanPage <= 1}
                        onClick={() => setScanPage((p) => p - 1)}
                      >
                        Previous
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={scanPage >= totalPages}
                        onClick={() => setScanPage((p) => p + 1)}
                      >
                        Next
                      </Button>
                    </div>
                  </div>
                )}
              </Card>
            )}
          </div>
        );
      })()}

      {/* Scan Confirm / Config Dialog */}
      <ScanConfirmDialog
        collectionId={id}
        open={scanConfirmOpen}
        onOpenChange={setScanConfirmOpen}
        scanConfig={scanConfig}
        configOnly={configOnlyMode}
        onConfirm={async (updatedConfig) => {
          setScanConfig(updatedConfig);
          // Save config to collection
          try {
            await api.put(`/collections/${id}`, {
              name: collection!.name,
              description: collection!.description,
              scan_config: JSON.stringify(updatedConfig),
            });
          } catch {
            // error handled by api layer
          }
          setScanConfirmOpen(false);

          if (configOnlyMode) {
            loadCollection();
            return;
          }

          // Start scans with the confirmed config
          handleScanAll(updatedConfig);
        }}
      />

      {/* Edit Collection Dialog */}
      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit Collection</DialogTitle>
            <DialogDescription>
              Update the name and description for this collection.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 pt-4">
            <div className="space-y-2">
              <Label htmlFor="editName">Name</Label>
              <Input
                id="editName"
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                placeholder="Collection name"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="editDescription">Description</Label>
              <Textarea
                id="editDescription"
                value={editDescription}
                onChange={(e) => setEditDescription(e.target.value)}
                placeholder="What is this collection for?"
                rows={3}
              />
            </div>
            <div className="flex gap-2 pt-2">
              <Button
                onClick={handleEditSave}
                disabled={!editName.trim() || editSaving}
                className="flex-1"
              >
                {editSaving ? "Saving..." : "Save Changes"}
              </Button>
              <Button variant="outline" onClick={() => setEditOpen(false)}>
                Cancel
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Add Repo Dialog */}
      <Dialog open={addRepoOpen} onOpenChange={setAddRepoOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add Repository</DialogTitle>
            <DialogDescription>
              Add a local or remote repository to &ldquo;{collection.name}&rdquo;.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 pt-4">
            <div className="space-y-2">
              <Label>Source Type</Label>
              <Select
                value={repoSourceType}
                onValueChange={(v) => {
                  setRepoSourceType(v as SourceType);
                  setBrowsing(false);
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="local">Local Path</SelectItem>
                  <SelectItem value="github">GitHub</SelectItem>
                  <SelectItem value="gitlab">GitLab</SelectItem>
                  <SelectItem value="git">Git URL</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="addRepoPath">
                {repoSourceType === "local"
                  ? "Local Path"
                  : repoSourceType === "github"
                    ? "GitHub Repository (owner/repo)"
                    : repoSourceType === "gitlab"
                      ? "GitLab Repository (owner/repo)"
                      : "Git Clone URL"}
              </Label>
              <div className="flex gap-2">
                <Input
                  id="addRepoPath"
                  value={repoPath}
                  onChange={(e) => setRepoPath(e.target.value)}
                  placeholder={
                    repoSourceType === "local"
                      ? "/home/user/projects/my-app"
                      : repoSourceType === "github"
                        ? "octocat/hello-world"
                        : repoSourceType === "gitlab"
                          ? "group/project"
                          : "https://github.com/user/repo.git"
                  }
                  className="flex-1"
                />
                {repoSourceType === "local" && (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => browseDir(repoPath || undefined)}
                    disabled={browseLoading}
                    className="shrink-0"
                  >
                    {browseLoading ? "..." : "Browse"}
                  </Button>
                )}
              </div>
            </div>

            {/* Local file browser */}
            {browsing && repoSourceType === "local" && (
              <div className="border rounded-md max-h-60 overflow-y-auto">
                <div className="sticky top-0 bg-background border-b px-3 py-2 flex items-center gap-2">
                  <span className="text-xs text-muted-foreground truncate flex-1">
                    {browseCurrent}
                  </span>
                  {browseParent && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 px-2 text-xs shrink-0"
                      onClick={() => browseDir(browseParent)}
                    >
                      Up
                    </Button>
                  )}
                </div>
                {browseEntries.length === 0 ? (
                  <div className="px-3 py-4 text-sm text-muted-foreground text-center">
                    No subdirectories found
                  </div>
                ) : (
                  <div className="divide-y">
                    {browseEntries.map((entry) => (
                      <button
                        key={entry.path}
                        className="w-full text-left px-3 py-2 hover:bg-muted/50 flex items-center gap-2 text-sm"
                        onClick={() => selectBrowseEntry(entry)}
                      >
                        <span className="shrink-0">
                          {entry.is_git ? "\u{1F4C1}" : "\u{1F4C2}"}
                        </span>
                        <span className="truncate flex-1">{entry.name}</span>
                        {entry.is_git && (
                          <span className="text-xs bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300 px-1.5 py-0.5 rounded shrink-0">
                            git repo
                          </span>
                        )}
                      </button>
                    ))}
                  </div>
                )}
                <div className="sticky bottom-0 bg-background border-t px-3 py-2">
                  <Button
                    variant="outline"
                    size="sm"
                    className="w-full text-xs"
                    onClick={() => {
                      setRepoPath(browseCurrent);
                      if (!repoName) setRepoName(browseCurrent.split("/").pop() || "");
                      setBrowsing(false);
                    }}
                  >
                    Use current: {browseCurrent.split("/").pop()}
                  </Button>
                </div>
              </div>
            )}

            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label htmlFor="addRepoName">Name (optional)</Label>
                <Input
                  id="addRepoName"
                  value={repoName}
                  onChange={(e) => setRepoName(e.target.value)}
                  placeholder={repoPath.split("/").pop() || "my-app"}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="addRepoBranch">Branch</Label>
                <Input
                  id="addRepoBranch"
                  value={repoBranch}
                  onChange={(e) => setRepoBranch(e.target.value)}
                />
              </div>
            </div>
            <Button
              onClick={handleAddRepo}
              disabled={!repoPath.trim() || addingRepo}
              className="w-full"
            >
              {addingRepo ? "Adding..." : "Add Repository"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* Delete Collection Confirmation */}
      <ConfirmDeleteDialog
        open={deleteCollectionOpen}
        onOpenChange={setDeleteCollectionOpen}
        title={`Delete "${collection.name}"?`}
        confirmName={collection.name}
        entityType="collection"
        warnings={[
          "All scans, findings, and artifacts for this collection will be permanently deleted.",
          `${collection.repos?.length || 0} repo(s) linked to this collection will be removed from Wolf.`,
          "Repositories themselves will NOT be deleted from disk.",
        ]}
        onConfirm={handleDeleteCollection}
        deleting={deletingCollection}
      />

      {/* Delete Repo Confirmation */}
      {repoToDelete && (
        <ConfirmDeleteDialog
          open={deleteRepoOpen}
          onOpenChange={(open) => {
            setDeleteRepoOpen(open);
            if (!open) setRepoToDelete(null);
          }}
          title={`Delete "${repoToDelete.name}"?`}
          confirmName={repoToDelete.name}
          entityType="repository"
          warnings={[
            "All scans, findings, and artifacts for this repository will be permanently deleted from Wolf.",
            "The actual source code on disk will NOT be modified or deleted.",
            "This repository will be removed from all collections.",
          ]}
          onConfirm={handleDeleteRepo}
          deleting={deletingRepo}
        />
      )}
    </div>
  );
}
