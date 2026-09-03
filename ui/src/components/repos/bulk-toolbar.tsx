// Fixed-bottom-center bulk-action toolbar for /repos. Renders only when
// at least one repo is selected. Each action opens a small confirmation
// or picker dialog. Scan dispatches N parallel POST /scans calls — the
// server gains a true bulk endpoint in a follow-up.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  FolderPlusIcon,
  PlayIcon,
  ShieldIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { COMMUNITY_LIMIT_COPY, isCommunityLimit } from "@/lib/safe-display";
import type { Collection, Repo, Scan } from "@/lib/types";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

interface BulkToolbarProps {
  selectedIds: Set<string>;
  repos: Repo[];
  onClear: () => void;
}

type DialogKind = null | "scan" | "collection" | "policy" | "delete";

export function BulkToolbar({ selectedIds, repos, onClear }: BulkToolbarProps) {
  const [dialog, setDialog] = useState<DialogKind>(null);
  if (selectedIds.size === 0) return null;

  const selectedRepos = repos.filter((r) => selectedIds.has(r.id));

  return (
    <>
      <div className="pointer-events-none fixed inset-x-0 bottom-6 z-40 flex justify-center px-4">
        <div className="pointer-events-auto inline-flex items-center gap-1 rounded-full border bg-popover px-2 py-1.5 text-sm text-popover-foreground shadow-lg">
          <span className="px-2 text-xs font-medium">
            {selectedIds.size} selected
          </span>
          <span className="mx-0.5 h-5 w-px bg-border" aria-hidden />
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 text-xs"
            onClick={() => setDialog("scan")}
          >
            <PlayIcon className="size-3.5" /> Scan
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 text-xs"
            onClick={() => setDialog("collection")}
          >
            <FolderPlusIcon className="size-3.5" /> Add to collection
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 text-xs"
            onClick={() => setDialog("policy")}
          >
            <ShieldIcon className="size-3.5" /> Apply policy
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 text-xs text-destructive hover:text-destructive"
            onClick={() => setDialog("delete")}
          >
            <Trash2Icon className="size-3.5" /> Delete
          </Button>
          <span className="mx-0.5 h-5 w-px bg-border" aria-hidden />
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 text-xs text-muted-foreground"
            onClick={onClear}
          >
            <XIcon className="size-3.5" /> Clear
          </Button>
        </div>
      </div>

      {dialog === "scan" && (
        <ScanDialog
          repos={selectedRepos}
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            onClear();
          }}
        />
      )}
      {dialog === "collection" && (
        <CollectionDialog
          repos={selectedRepos}
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            onClear();
          }}
        />
      )}
      {dialog === "policy" && (
        <PolicyDialog onClose={() => setDialog(null)} />
      )}
      {dialog === "delete" && (
        <DeleteDialog
          repos={selectedRepos}
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            onClear();
          }}
        />
      )}
    </>
  );
}

function ScanDialog({
  repos,
  onClose,
  onDone,
}: {
  repos: Repo[];
  onClose: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const run = useMutation({
    mutationFn: async () => {
      // Fire-and-forget parallel POSTs. Server gets a true bulk endpoint
      // later; for now this matches what the toolbar plan calls out.
      const results = await Promise.allSettled(
        repos.map((r) =>
          api.post<Scan>("/scans", {
            repo_id: r.id,
            branch: r.default_branch || "main",
          }),
        ),
      );
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const failed = results.length - ok;
      const limit = results.some((r) => r.status === "rejected" && isCommunityLimit(r.reason));
      return { ok, failed, limit };
    },
    onSuccess: ({ ok, failed, limit }) => {
      qc.invalidateQueries({ queryKey: ["scans"] });
      if (limit) {
        toast.error(COMMUNITY_LIMIT_COPY);
      }
      if (failed === 0) {
        toast.success(`Started ${ok} scan${ok === 1 ? "" : "s"}`);
      } else if (!limit) {
        toast.warning(`Started ${ok} scan${ok === 1 ? "" : "s"}; ${failed} failed`);
      }
      onDone();
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to start scans");
    },
  });

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Scan {repos.length} repo{repos.length === 1 ? "" : "s"}</DialogTitle>
          <DialogDescription>
            One scan per repo will be queued on each repo's default branch.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={run.isPending}>
            Cancel
          </Button>
          <Button onClick={() => run.mutate()} disabled={run.isPending}>
            {run.isPending ? "Starting…" : "Start scans"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CollectionDialog({
  repos,
  onClose,
  onDone,
}: {
  repos: Repo[];
  onClose: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const collectionsQ = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      const r = await api.get<Collection[]>("/collections");
      return r.data ?? [];
    },
  });
  const [selected, setSelected] = useState<string>("");

  const run = useMutation({
    mutationFn: async (collectionId: string) => {
      // The server exposes POST /collections/{id}/repos which adds a
      // single repo. Fan out N calls; idempotent server-side so already
      // present repos are harmless.
      const results = await Promise.allSettled(
        repos.map((r) =>
          api.post(`/collections/${collectionId}/repos`, { repo_id: r.id }),
        ),
      );
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const failed = results.length - ok;
      return { ok, failed };
    },
    onSuccess: ({ ok, failed }) => {
      qc.invalidateQueries({ queryKey: ["collections"] });
      if (failed === 0) {
        toast.success(`Added ${ok} repo${ok === 1 ? "" : "s"} to collection`);
      } else {
        toast.warning(`Added ${ok}; ${failed} failed`);
      }
      onDone();
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to update collection");
    },
  });

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add to collection</DialogTitle>
          <DialogDescription>
            Pick a collection — {repos.length} repo{repos.length === 1 ? "" : "s"} will be added.
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-64 overflow-y-auto space-y-1 py-2">
          {collectionsQ.isLoading ? (
            <div className="text-sm text-muted-foreground">Loading…</div>
          ) : (collectionsQ.data ?? []).length === 0 ? (
            <div className="text-sm text-muted-foreground">No collections yet.</div>
          ) : (
            (collectionsQ.data ?? []).map((c) => {
              const active = selected === c.id;
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
                  onClick={() => setSelected(c.id)}
                >
                  <div className="font-medium">{c.name}</div>
                  {c.description && (
                    <div className="text-xs text-muted-foreground truncate">
                      {c.description}
                    </div>
                  )}
                </button>
              );
            })
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={run.isPending}>
            Cancel
          </Button>
          <Button
            onClick={() => selected && run.mutate(selected)}
            disabled={!selected || run.isPending}
          >
            {run.isPending ? "Saving…" : "Add"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PolicyDialog({ onClose }: { onClose: () => void }) {
  // Policy application is not yet wired to a fleet endpoint. The toolbar
  // surfaces the action so the affordance is discoverable; the actual
  // batch-policy API lands with the policy-as-code milestone.
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Apply policy</DialogTitle>
          <DialogDescription>
            Batch policy application ships with the upcoming policy-as-code
            milestone. For now, edit policies on each repo's detail page.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button onClick={onClose}>Got it</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteDialog({
  repos,
  onClose,
  onDone,
}: {
  repos: Repo[];
  onClose: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [purge, setPurge] = useState(false);
  const run = useMutation({
    mutationFn: async () => {
      const q = purge ? "?purge=true" : "";
      const results = await Promise.allSettled(
        repos.map((r) => api.delete(`/repos/${r.id}${q}`)),
      );
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const failed = results.length - ok;
      return { ok, failed };
    },
    onSuccess: ({ ok, failed }) => {
      qc.invalidateQueries({ queryKey: ["repos"] });
      if (failed === 0) {
        toast.success(`Deleted ${ok} repo${ok === 1 ? "" : "s"}`);
      } else {
        toast.warning(`Deleted ${ok}; ${failed} failed`);
      }
      onDone();
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to delete");
    },
  });

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            Delete {repos.length} repo{repos.length === 1 ? "" : "s"}?
          </DialogTitle>
          <DialogDescription>
            Repos that still have scans cannot be removed unless you also
            delete their findings. Source code on disk or in git is not
            touched.
          </DialogDescription>
        </DialogHeader>
        <label className="flex items-start gap-3 rounded-md border border-border bg-muted/20 p-3 text-sm">
          <input
            type="checkbox"
            checked={purge}
            onChange={(e) => setPurge(e.target.checked)}
            className="mt-0.5"
          />
          <span>
            <span className="font-medium">Also remove records</span>
            <span className="mt-0.5 block text-xs text-muted-foreground">
              Permanently delete scan history, findings, and artifacts for the
              selected repos. Required if any selected repo still has scans.
            </span>
          </span>
        </label>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={run.isPending}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={() => run.mutate()}
            disabled={run.isPending || !purge}
          >
            {run.isPending
              ? "Deleting…"
              : purge
                ? "Delete and remove records"
                : "Delete"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
