"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Pencil, Trash2 } from "lucide-react";
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
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { SeverityBadge } from "@/components/severity-badge";
import { StatusBadge } from "@/components/status-badge";
import { TrendSparkline } from "@/components/trend-sparkline";
import { EmptyState } from "@/components/empty-state";
import { LoadingSpinner } from "@/components/loading-spinner";
import { ConfirmDeleteDialog } from "@/components/confirm-delete-dialog";
import api from "@/lib/api";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Collection, Repo, Severity, SourceType } from "@/lib/types";

type DialogStep = "collection" | "repo";

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

export default function CollectionsHome() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const { data: collections = [], isLoading: loading } = useQuery({
    queryKey: ["collections"],
    queryFn: () => api.get<Collection[]>("/collections").then((r) => r.data ?? []),
    refetchInterval: 10_000,
  });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [dialogStep, setDialogStep] = useState<DialogStep>("collection");
  const [createdCollectionId, setCreatedCollectionId] = useState<string | null>(null);

  // Step 1: collection fields
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  // Step 2: repo fields
  const [repoSourceType, setRepoSourceType] = useState<SourceType>("local");
  const [repoPath, setRepoPath] = useState("");
  const [repoName, setRepoName] = useState("");
  const [repoBranch, setRepoBranch] = useState("main");
  const [addingRepo, setAddingRepo] = useState(false);

  // File browser state
  const [browsing, setBrowsing] = useState(false);
  const [browseEntries, setBrowseEntries] = useState<BrowseEntry[]>([]);
  const [browseCurrent, setBrowseCurrent] = useState("");
  const [browseParent, setBrowseParent] = useState("");
  const [browseLoading, setBrowseLoading] = useState(false);

  // Edit collection state
  const [editOpen, setEditOpen] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editDescription, setEditDescription] = useState("");
  const [editSaving, setEditSaving] = useState(false);

  // Delete collection state
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [collectionToDelete, setCollectionToDelete] = useState<Collection | null>(null);
  const [deleting, setDeleting] = useState(false);

  const openDelete = (col: Collection, e: React.MouseEvent) => {
    e.stopPropagation();
    setCollectionToDelete(col);
    setDeleteOpen(true);
  };

  const handleDeleteCollection = async () => {
    if (!collectionToDelete) return;
    setDeleting(true);
    try {
      await api.delete(`/collections/${collectionToDelete.id}`);
      setDeleteOpen(false);
      setCollectionToDelete(null);
      queryClient.invalidateQueries({ queryKey: ["collections"] });
    } catch {
      // error handled by api layer
    } finally {
      setDeleting(false);
    }
  };

  const openEdit = (col: Collection, e: React.MouseEvent) => {
    e.stopPropagation();
    setEditId(col.id);
    setEditName(col.name);
    setEditDescription(col.description || "");
    setEditOpen(true);
  };

  const handleEditSave = async () => {
    if (!editId || !editName.trim()) return;
    setEditSaving(true);
    try {
      const col = collections.find((c) => c.id === editId);
      await api.put(`/collections/${editId}`, {
        name: editName.trim(),
        description: editDescription.trim(),
        scan_config: typeof col?.scan_config === "string"
          ? col.scan_config
          : JSON.stringify(col?.scan_config || {}),
      });
      setEditOpen(false);
      queryClient.invalidateQueries({ queryKey: ["collections"] });
    } catch {
      // error handled by api layer
    } finally {
      setEditSaving(false);
    }
  };

  const resetDialog = () => {
    setDialogStep("collection");
    setCreatedCollectionId(null);
    setName("");
    setDescription("");
    setRepoSourceType("local");
    setRepoPath("");
    setRepoName("");
    setRepoBranch("main");
    setAddingRepo(false);
    setBrowsing(false);
    setBrowseEntries([]);
  };

  const handleDialogOpenChange = (open: boolean) => {
    if (!open) resetDialog();
    setDialogOpen(open);
  };

  const handleCreateCollection = async () => {
    try {
      const res = await api.post<Collection>("/collections", {
        name,
        description,
      });
      queryClient.invalidateQueries({ queryKey: ["collections"] });
      setCreatedCollectionId(res.data.id);
      setDialogStep("repo");
    } catch {
      // error handled by api layer
    }
  };

  const handleAddRepo = async () => {
    if (!createdCollectionId) return;
    setAddingRepo(true);
    try {
      // 1. Create the repo
      const repoRes = await api.post<Repo>("/repos", {
        name: repoName || repoPath.split("/").pop() || "repo",
        source_type: repoSourceType,
        source_path: repoPath,
        default_branch: repoBranch,
      });
      // 2. Link it to the collection
      await api.post(`/collections/${createdCollectionId}/repos`, {
        repo_id: repoRes.data.id,
      });
      // Navigate to collection detail
      setDialogOpen(false);
      resetDialog();
      router.push(`/collections/${createdCollectionId}`);
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
      // fallback: stay on manual input
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

  const handleSkipRepo = () => {
    setDialogOpen(false);
    if (createdCollectionId) {
      router.push(`/collections/${createdCollectionId}`);
    }
    resetDialog();
  };

  if (loading) return <LoadingSpinner />;

  const severities: Severity[] = ["critical", "high", "medium", "low", "info"];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold">Collections</h1>
          <p className="text-muted-foreground">
            Organize repositories and track code quality
          </p>
        </div>
        <Dialog open={dialogOpen} onOpenChange={handleDialogOpenChange}>
          <DialogTrigger asChild>
            <Button>Create Collection</Button>
          </DialogTrigger>
          <DialogContent>
            {dialogStep === "collection" ? (
              <>
                <DialogHeader>
                  <DialogTitle>Create Collection</DialogTitle>
                  <DialogDescription>
                    Organize repositories into a collection for grouped scanning.
                  </DialogDescription>
                </DialogHeader>
                <div className="space-y-4 pt-4">
                  <div className="space-y-2">
                    <Label htmlFor="name">Name</Label>
                    <Input
                      id="name"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      placeholder="my-services"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="description">Description</Label>
                    <Textarea
                      id="description"
                      value={description}
                      onChange={(e) => setDescription(e.target.value)}
                      placeholder="Backend microservices..."
                    />
                  </div>
                  <Button onClick={handleCreateCollection} disabled={!name.trim()} className="w-full">
                    Next: Add Repository
                  </Button>
                </div>
              </>
            ) : (
              <>
                <DialogHeader>
                  <DialogTitle>Add Repository</DialogTitle>
                  <DialogDescription>
                    Add a local or remote repository to &ldquo;{name}&rdquo;.
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
                    <Label htmlFor="repoPath">
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
                        id="repoPath"
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
                      <Label htmlFor="repoName">Name (optional)</Label>
                      <Input
                        id="repoName"
                        value={repoName}
                        onChange={(e) => setRepoName(e.target.value)}
                        placeholder={repoPath.split("/").pop() || "my-app"}
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="repoBranch">Branch</Label>
                      <Input
                        id="repoBranch"
                        value={repoBranch}
                        onChange={(e) => setRepoBranch(e.target.value)}
                      />
                    </div>
                  </div>
                  <div className="flex gap-2">
                    <Button
                      onClick={handleAddRepo}
                      disabled={!repoPath.trim() || addingRepo}
                      className="flex-1"
                    >
                      {addingRepo ? "Adding..." : "Add Repository"}
                    </Button>
                    <Button variant="ghost" onClick={handleSkipRepo}>
                      Skip
                    </Button>
                  </div>
                </div>
              </>
            )}
          </DialogContent>
        </Dialog>
      </div>

      {collections.length === 0 ? (
        <EmptyState
          title="No collections yet"
          description="Create a collection to organize your repositories and start scanning."
          action={
            <Button onClick={() => setDialogOpen(true)}>
              Create Collection
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {collections.map((collection) => (
            <div
              key={collection.id}
              role="link"
              tabIndex={0}
              className="cursor-pointer"
              onClick={() => router.push(`/collections/${collection.id}`)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  router.push(`/collections/${collection.id}`);
                }
              }}
            >
              <Card className="hover:border-primary/50 transition-colors">
                <CardHeader>
                  <div className="flex items-center justify-between">
                    <CardTitle>{collection.name}</CardTitle>
                    <div className="flex items-center gap-0.5 shrink-0">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-muted-foreground h-7 w-7 p-0"
                        onClick={(e) => openEdit(collection, e)}
                      >
                        <Pencil className="w-3.5 h-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive hover:text-destructive hover:bg-destructive/10 h-7 w-7 p-0"
                        onClick={(e) => openDelete(collection, e)}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </div>
                  <CardDescription>
                    {collection.repo_count ?? 0} repos
                    {collection.description && ` — ${collection.description}`}
                  </CardDescription>
                </CardHeader>
                <CardContent className="space-y-3">
                  {collection.latest_scan && (
                    <StatusBadge status={collection.latest_scan.status} />
                  )}
                  {collection.finding_counts && (
                    <div className="flex flex-wrap gap-1.5">
                      {severities.map(
                        (sev) =>
                          (collection.finding_counts?.[sev] ?? 0) > 0 && (
                            <div key={sev} className="flex items-center gap-1">
                              <SeverityBadge severity={sev} />
                              <span className="text-xs">
                                {collection.finding_counts?.[sev]}
                              </span>
                            </div>
                          ),
                      )}
                    </div>
                  )}
                  {collection.trend && collection.trend.length > 1 && (
                    <TrendSparkline data={collection.trend} />
                  )}
                  <div className="flex gap-2 pt-2">
                    <Button size="sm" variant="outline" asChild>
                      <Link
                        href={`/collections/${collection.id}`}
                        onClick={(e) => e.stopPropagation()}
                      >
                        Open
                      </Link>
                    </Button>
                    <Button size="sm" variant="outline" asChild>
                      <Link
                        href={`/findings?collection=${collection.id}`}
                        onClick={(e) => e.stopPropagation()}
                      >
                        Findings
                      </Link>
                    </Button>
                  </div>
                </CardContent>
              </Card>
            </div>
          ))}
        </div>
      )}

      {/* Delete Collection Confirmation */}
      {collectionToDelete && (
        <ConfirmDeleteDialog
          open={deleteOpen}
          onOpenChange={(open) => {
            setDeleteOpen(open);
            if (!open) setCollectionToDelete(null);
          }}
          title={`Delete "${collectionToDelete.name}"?`}
          confirmName={collectionToDelete.name}
          entityType="collection"
          warnings={[
            "All scans, findings, and artifacts for this collection will be permanently deleted.",
            `${collectionToDelete.repo_count ?? 0} repo(s) linked to this collection will be removed from Wolf.`,
            "Repositories themselves will NOT be deleted from disk.",
          ]}
          onConfirm={handleDeleteCollection}
          deleting={deleting}
        />
      )}

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
              <Label htmlFor="editColName">Name</Label>
              <Input
                id="editColName"
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                placeholder="Collection name"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="editColDesc">Description</Label>
              <Textarea
                id="editColDesc"
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
    </div>
  );
}
