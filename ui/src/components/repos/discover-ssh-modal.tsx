// Multi-step SSH discover wizard. Picks a remote node, asks the node to walk
// for .git directories under a base path, lets the operator multi-select from
// the result, then fans out POST /repos calls (source_type=ssh) — optionally
// tagging the new repos into a chosen collection. The modal stays mounted
// until every import resolves so the user can see partial-failure counts.
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Loader2Icon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { Collection, RemoteNode, Repo } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

type Step = "node" | "path" | "select" | "target";

interface DiscoveredRepo {
  name: string;
  path: string;
  branch?: string;
  commit_sha?: string;
}

export function DiscoverSSHModal({
  onClose,
  initialNodeId,
}: {
  onClose: () => void;
  initialNodeId?: string;
}) {
  const qc = useQueryClient();
  const [step, setStep] = useState<Step>(initialNodeId ? "path" : "node");
  const [nodeId, setNodeId] = useState<string>(initialNodeId ?? "");
  const [basePath, setBasePath] = useState<string>("");
  const [repos, setRepos] = useState<DiscoveredRepo[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [collectionId, setCollectionId] = useState<string>("");

  // Step 1: load remote nodes for picking.
  const nodesQ = useQuery({
    queryKey: ["nodes", "all"],
    queryFn: async () => {
      const r = await api.get<RemoteNode[]>("/nodes");
      return r.data ?? [];
    },
  });
  const node = useMemo(
    () => (nodesQ.data ?? []).find((n) => n.id === nodeId),
    [nodesQ.data, nodeId],
  );

  // Pre-fill base path from the node's configured BasePath whenever a node is
  // chosen (or the modal opens against a pre-selected node).
  const effectiveBasePath = useMemo(() => {
    if (basePath.trim()) return basePath.trim();
    return node?.base_path?.trim() ?? "";
  }, [basePath, node]);

  // Step 2: discover repos on the chosen node.
  const discoverMut = useMutation({
    mutationFn: async () => {
      const r = await api.post<DiscoveredRepo[]>(
        `/nodes/${nodeId}/discover-repos`,
        { base_path: effectiveBasePath || undefined },
      );
      return r.data ?? [];
    },
    onSuccess: (data) => {
      setRepos(data);
      setSelected(new Set(data.map((r) => r.path)));
      setStep("select");
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to discover repos");
    },
  });

  // Step 4: collections for the optional target picker.
  const collectionsQ = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      const r = await api.get<Collection[]>("/collections");
      return r.data ?? [];
    },
    enabled: step === "target",
  });

  // Final import: N parallel POST /repos calls.
  const importMut = useMutation({
    mutationFn: async () => {
      const chosen = repos.filter((r) => selected.has(r.path));
      const results = await Promise.allSettled(
        chosen.map(async (r) => {
          const created = await api.post<Repo>("/repos", {
            name: r.name,
            source_type: "ssh",
            source_path: r.path,
            remote_node_id: nodeId,
            remote_path: r.path,
            default_branch: r.branch || "main",
          });
          if (collectionId && created.data?.id) {
            await api.post(`/collections/${collectionId}/repos`, {
              repo_id: created.data.id,
            });
          }
          return created.data;
        }),
      );
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const failed = results.length - ok;
      return { ok, failed };
    },
    onSuccess: ({ ok, failed }) => {
      qc.invalidateQueries({ queryKey: ["repos"] });
      qc.invalidateQueries({ queryKey: ["collections"] });
      if (failed === 0) {
        toast.success(`Imported ${ok} repo${ok === 1 ? "" : "s"}`);
      } else {
        toast.warning(`Imported ${ok}; ${failed} failed`);
      }
      onClose();
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to import repos");
    },
  });

  const toggleRepo = (path: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-3xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Discover SSH repositories</DialogTitle>
          <DialogDescription>
            Pick a remote node, walk its disk for git checkouts, then bulk-import
            the ones you want to track.
          </DialogDescription>
        </DialogHeader>

        {step === "node" && (
          <div className="space-y-3 py-2">
            {nodesQ.isLoading ? (
              <div className="text-sm text-muted-foreground">Loading nodes…</div>
            ) : (nodesQ.data ?? []).length === 0 ? (
              <div className="rounded-md border border-border bg-muted/30 p-4 text-sm">
                No SSH nodes configured.{" "}
                <Link
                  to="/settings"
                  search={{ tab: "nodes" }}
                  className="text-primary underline"
                  onClick={onClose}
                >
                  Add one in Settings → Nodes
                </Link>{" "}
                first.
              </div>
            ) : (
              <div className="space-y-1">
                <Label className="text-xs">Pick a remote node</Label>
                <div className="space-y-1 max-h-72 overflow-y-auto">
                  {(nodesQ.data ?? []).map((n) => {
                    const active = nodeId === n.id;
                    return (
                      <button
                        type="button"
                        key={n.id}
                        className={
                          "block w-full rounded-md border px-3 py-2 text-left text-sm transition " +
                          (active
                            ? "border-primary bg-accent"
                            : "border-border hover:bg-muted/40")
                        }
                        onClick={() => setNodeId(n.id)}
                      >
                        <div className="font-medium">{n.name}</div>
                        <div className="text-xs text-muted-foreground font-mono">
                          {n.username}@{n.host}:{n.port}
                        </div>
                        {n.base_path && (
                          <div className="text-xs text-muted-foreground font-mono">
                            base: {n.base_path}
                          </div>
                        )}
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        )}

        {step === "path" && (
          <div className="space-y-3 py-2">
            <div className="space-y-1">
              <Label htmlFor="ssh-base-path" className="text-xs">
                Base path on{" "}
                <span className="font-mono">
                  {node?.name ?? "node"}
                </span>
              </Label>
              <Input
                id="ssh-base-path"
                value={basePath}
                onChange={(e) => setBasePath(e.target.value)}
                placeholder={node?.base_path ?? "/srv/code"}
                autoFocus
              />
              <p className="text-xs text-muted-foreground">
                We run <code className="font-mono">find &lt;path&gt; -maxdepth 3 -name .git</code>{" "}
                on the remote host. Leave blank to use the node's configured base
                path.
              </p>
            </div>
            {discoverMut.isError && (
              <div className="text-xs text-destructive">
                {discoverMut.error instanceof Error
                  ? discoverMut.error.message
                  : "Failed"}
              </div>
            )}
          </div>
        )}

        {step === "select" && (
          <div className="flex-1 min-h-0 flex flex-col gap-2 py-1">
            <div className="flex flex-wrap items-center gap-3 text-xs">
              <span className="text-muted-foreground">
                Found {repos.length} repo{repos.length === 1 ? "" : "s"} under{" "}
                <span className="font-mono">{effectiveBasePath}</span>
              </span>
              <span className="ml-auto text-muted-foreground">
                {selected.size} of {repos.length} selected
              </span>
            </div>
            <div className="flex-1 min-h-0 overflow-y-auto rounded-md border border-border/40">
              <Table className="data-table">
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-10">
                      <Checkbox
                        aria-label="Select all visible"
                        checked={
                          repos.length > 0 &&
                          repos.every((r) => selected.has(r.path))
                        }
                        onCheckedChange={(v) => {
                          if (v) {
                            setSelected(new Set(repos.map((r) => r.path)));
                          } else {
                            setSelected(new Set());
                          }
                        }}
                      />
                    </TableHead>
                    <TableHead>Path</TableHead>
                    <TableHead>Branch</TableHead>
                    <TableHead>Commit</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {repos.map((r) => (
                    <TableRow key={r.path}>
                      <TableCell>
                        <Checkbox
                          checked={selected.has(r.path)}
                          onCheckedChange={() => toggleRepo(r.path)}
                          aria-label={`Select ${r.path}`}
                        />
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {r.path}
                      </TableCell>
                      <TableCell className="text-xs">{r.branch || "—"}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {r.commit_sha ? r.commit_sha.slice(0, 7) : "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        )}

        {step === "target" && (
          <div className="space-y-3 py-2">
            <div className="space-y-1">
              <Label className="text-xs">Add to collection (optional)</Label>
              <div className="max-h-64 overflow-y-auto space-y-1">
                <button
                  type="button"
                  className={
                    "block w-full rounded-md border px-3 py-2 text-left text-sm transition " +
                    (collectionId === ""
                      ? "border-primary bg-accent"
                      : "border-border hover:bg-muted/40")
                  }
                  onClick={() => setCollectionId("")}
                >
                  <div className="font-medium">No collection</div>
                  <div className="text-xs text-muted-foreground">
                    Import without grouping.
                  </div>
                </button>
                {(collectionsQ.data ?? []).map((c) => {
                  const active = collectionId === c.id;
                  return (
                    <button
                      type="button"
                      key={c.id}
                      className={
                        "block w-full rounded-md border px-3 py-2 text-left text-sm transition " +
                        (active
                          ? "border-primary bg-accent"
                          : "border-border hover:bg-muted/40")
                      }
                      onClick={() => setCollectionId(c.id)}
                    >
                      <div className="font-medium">{c.name}</div>
                      {c.description && (
                        <div className="text-xs text-muted-foreground truncate">
                          {c.description}
                        </div>
                      )}
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        )}

        <DialogFooter>
          {step === "node" && (
            <>
              <Button variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button onClick={() => setStep("path")} disabled={!nodeId}>
                Next
              </Button>
            </>
          )}
          {step === "path" && (
            <>
              <Button
                variant="ghost"
                onClick={() => (initialNodeId ? onClose() : setStep("node"))}
              >
                {initialNodeId ? "Cancel" : "Back"}
              </Button>
              <Button
                onClick={() => discoverMut.mutate()}
                disabled={discoverMut.isPending}
              >
                {discoverMut.isPending ? (
                  <>
                    <Loader2Icon className="size-3.5 animate-spin" /> Walking…
                  </>
                ) : (
                  "Discover"
                )}
              </Button>
            </>
          )}
          {step === "select" && (
            <>
              <Button variant="ghost" onClick={() => setStep("path")}>
                Back
              </Button>
              <Button
                onClick={() => setStep("target")}
                disabled={selected.size === 0}
              >
                Next ({selected.size})
              </Button>
            </>
          )}
          {step === "target" && (
            <>
              <Button variant="ghost" onClick={() => setStep("select")}>
                Back
              </Button>
              <Button
                onClick={() => importMut.mutate()}
                disabled={importMut.isPending || selected.size === 0}
              >
                {importMut.isPending ? (
                  <>
                    <Loader2Icon className="size-3.5 animate-spin" /> Importing…
                  </>
                ) : (
                  `Import ${selected.size} repo${selected.size === 1 ? "" : "s"}`
                )}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
