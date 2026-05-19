// Directory-picker modal for the "Add local repo" flow. Walks the
// backend's allow-listed filesystem roots via GET /api/browse and lets the
// user click into folders, then "Select" a directory. Folders that contain
// a .git directory are flagged with a "git" badge so it's obvious where the
// repos live without diving in.
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowUpIcon,
  FolderIcon,
  GitBranchIcon,
  Loader2Icon,
  XIcon,
} from "lucide-react";
import { api } from "@/lib/api";

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

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const q = useQuery({
    queryKey: ["browse", path ?? ""],
    queryFn: async () => {
      const qs = path ? `?path=${encodeURIComponent(path)}` : "";
      const r = await api.get<BrowseResponse>(`/browse${qs}`);
      return r.data;
    },
  });

  return (
    <div
      role="dialog"
      aria-label="Browse for local repository"
      className="fixed inset-0 z-50 grid place-items-center bg-black/50 backdrop-blur-sm animate-fade-in p-4"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-[min(48rem,100%)] max-h-[80vh] glass-card bg-popover/95 shadow-2xl border p-5 flex flex-col"
      >
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-base font-semibold">Pick a local folder</h2>
          <button
            onClick={onClose}
            className="size-7 grid place-items-center rounded-md hover:bg-muted/50"
            aria-label="Close"
          >
            <XIcon className="size-4" />
          </button>
        </div>

        <div className="flex items-center gap-2 mb-3 text-xs">
          <button
            type="button"
            onClick={() => q.data?.parent && setPath(q.data.parent)}
            disabled={!q.data?.parent}
            className="h-7 px-2 rounded-md hover:bg-muted/40 disabled:opacity-30 inline-flex items-center gap-1"
            aria-label="Go up"
          >
            <ArrowUpIcon className="size-3.5" />
            Up
          </button>
          <span className="font-mono text-muted-foreground truncate flex-1 min-w-0">
            {q.data?.current ?? "Loading…"}
          </span>
        </div>

        <div className="flex-1 min-h-0 overflow-y-auto border border-border/40 rounded-md">
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

        <div className="flex justify-end gap-2 mt-3">
          <button
            type="button"
            onClick={onClose}
            className="h-8 px-3 rounded-md text-sm hover:bg-muted/40"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => {
              if (q.data?.current) {
                onSelect(q.data.current);
                onClose();
              }
            }}
            disabled={!q.data?.current}
            className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
          >
            Select this folder
          </button>
        </div>
      </div>
    </div>
  );
}
