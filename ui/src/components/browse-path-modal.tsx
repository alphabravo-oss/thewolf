// Directory-picker modal for the "Add local repo" flow. Walks the
// backend's allow-listed filesystem roots via GET /api/browse and lets the
// user click into folders, then "Select" a directory. Folders that contain
// a .git directory are flagged with a "git" badge so it's obvious where the
// repos live without diving in.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowUpIcon,
  FolderIcon,
  GitBranchIcon,
  Loader2Icon,
} from "lucide-react";
import { api } from "@/lib/api";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";

type Entry = {
  name: string;
  path: string;
  is_dir: boolean;
  is_git: boolean;
};

type BrowseResponse = {
  current: string;
  parent: string;
  entries: Entry[];
};

export function BrowsePathModal({
  initialPath,
  onSelect,
  onClose,
}: {
  initialPath?: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  // `undefined` means "use server default (first allow-listed root)" — the
  // initial mount fires the query without a path, the server picks home.
  const [path, setPath] = useState<string | undefined>(
    initialPath?.trim() ? initialPath : undefined,
  );

  const q = useQuery({
    queryKey: ["browse", path ?? ""],
    queryFn: async () => {
      const qs = path ? `?path=${encodeURIComponent(path)}` : "";
      const r = await api.get<BrowseResponse>(`/browse${qs}`);
      return r.data;
    },
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent
        aria-label="Browse for local repository"
        className="max-w-3xl max-h-[80vh] flex flex-col"
      >
        <DialogHeader>
          <DialogTitle>Pick a local folder</DialogTitle>
        </DialogHeader>

        <div className="flex items-center gap-2 text-xs">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => q.data?.parent && setPath(q.data.parent)}
            disabled={!q.data?.parent}
            aria-label="Go up"
            className="h-7 px-2 gap-1"
          >
            <ArrowUpIcon className="size-3.5" />
            Up
          </Button>
          <span className="font-mono text-muted-foreground truncate flex-1 min-w-0">
            {q.data?.current ?? "Loading…"}
          </span>
        </div>

        <div className="flex-1 min-h-0 overflow-y-auto border border-border rounded-md">
          {q.isLoading ? (
            <div className="p-6 grid place-items-center text-muted-foreground">
              <Loader2Icon className="size-4 animate-spin" />
            </div>
          ) : q.isError ? (
            <div className="p-4 text-sm text-destructive">
              {q.error instanceof Error ? q.error.message : "Failed to load"}
            </div>
          ) : !q.data || q.data.entries.length === 0 ? (
            <div className="p-4 text-sm text-muted-foreground italic">
              No subfolders here.
            </div>
          ) : (
            <ul className="divide-y divide-border/30">
              {q.data.entries.map((e) => (
                <li
                  key={e.path}
                  className="px-3 py-2 flex items-center gap-2 hover:bg-muted/30"
                >
                  <button
                    type="button"
                    onClick={() => setPath(e.path)}
                    className="flex items-center gap-2 flex-1 min-w-0 text-left"
                  >
                    <FolderIcon className="size-4 text-muted-foreground shrink-0" />
                    <span className="text-sm truncate">{e.name}</span>
                    {e.is_git && (
                      <span className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-primary/15 text-primary">
                        <GitBranchIcon className="size-3" /> git
                      </span>
                    )}
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      onSelect(e.path);
                      onClose();
                    }}
                    className="h-7 px-2 rounded-md text-xs hover:bg-muted/50 text-primary"
                  >
                    Select
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            onClick={() => {
              if (q.data?.current) {
                onSelect(q.data.current);
                onClose();
              }
            }}
            disabled={!q.data?.current}
          >
            Select this folder
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
