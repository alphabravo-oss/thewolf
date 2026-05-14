// Toolbar above the findings table: severity filter pills, search, status
// filter, bulk-action menu for selected rows. Saved-view persistence lives
// in store-views.ts (localStorage).
import {
  CheckCircle2Icon,
  CircleSlashIcon,
  AlertOctagonIcon,
  SearchIcon,
  BookmarkIcon,
} from "lucide-react";
import { useFindingsView } from "@/lib/store-views";
import { cn } from "@/lib/cn";
import type { FindingStatus, Severity } from "@/lib/types";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { toast } from "sonner";
import { useState } from "react";

const severities: Severity[] = ["critical", "high", "medium", "low", "info"];
const statuses: FindingStatus[] = [
  "open",
  "fixed",
  "wont_fix",
  "false_positive",
];

interface Props {
  selectedIds: string[];
  onClearSelection: () => void;
}

export function FindingsToolbar({ selectedIds, onClearSelection }: Props) {
  const view = useFindingsView();
  const [saveName, setSaveName] = useState("");
  const qc = useQueryClient();

  const bulkUpdate = useMutation({
    mutationFn: async (status: FindingStatus) => {
      await Promise.all(
        selectedIds.map((id) => api.put(`/findings/${id}`, { status })),
      );
    },
    onSuccess: (_d, status) => {
      qc.invalidateQueries({ queryKey: ["findings"] });
      toast.success(
        `Marked ${selectedIds.length} finding${selectedIds.length === 1 ? "" : "s"} as ${status.replace("_", " ")}`,
      );
      onClearSelection();
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Bulk update failed"),
  });

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        {/* Search */}
        <div className="relative">
          <SearchIcon className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search title or file…"
            value={view.search}
            onChange={(e) => view.set({ search: e.target.value })}
            className="h-9 pl-8 pr-3 w-64 rounded-md bg-muted/30 border border-border focus:border-ring outline-none text-sm"
          />
        </div>

        {/* Severity pills */}
        <div className="flex gap-1 flex-wrap">
          {severities.map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => view.toggleSeverity(s)}
              className={cn(
                "badge transition",
                view.severities.has(s)
                  ? `badge-${s}`
                  : "badge-info opacity-50 hover:opacity-80",
              )}
            >
              {s}
            </button>
          ))}
        </div>

        {/* Status filter */}
        <select
          value={view.status ?? ""}
          onChange={(e) =>
            view.set({
              status: e.target.value === "" ? null : (e.target.value as FindingStatus),
            })
          }
          className="h-9 px-3 rounded-md bg-muted/30 border border-border text-sm"
        >
          <option value="">All statuses</option>
          {statuses.map((s) => (
            <option key={s} value={s}>
              {s.replace("_", " ")}
            </option>
          ))}
        </select>

        <div className="flex-1" />

        {/* Saved views */}
        <div className="flex items-center gap-2">
          <select
            value={view.activeViewId ?? ""}
            onChange={(e) =>
              view.applyView(e.target.value === "" ? null : e.target.value)
            }
            className="h-9 px-3 rounded-md bg-muted/30 border border-border text-sm max-w-[12rem]"
          >
            <option value="">— views —</option>
            {view.savedViews.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name}
              </option>
            ))}
          </select>
          <div className="flex items-center gap-1">
            <input
              type="text"
              placeholder="Save as…"
              value={saveName}
              onChange={(e) => setSaveName(e.target.value)}
              className="h-9 px-2 w-32 rounded-md bg-muted/30 border border-border text-xs"
            />
            <button
              type="button"
              disabled={!saveName.trim()}
              onClick={() => {
                view.saveCurrent(saveName.trim());
                setSaveName("");
                toast.success(`Saved view "${saveName}"`);
              }}
              className="size-9 grid place-items-center rounded-md bg-muted/30 hover:bg-muted/50 disabled:opacity-50"
              title="Save current filters as a view"
            >
              <BookmarkIcon className="size-4" />
            </button>
          </div>
        </div>
      </div>

      {/* Bulk-action bar — only visible with rows selected. */}
      {selectedIds.length > 0 && (
        <div className="glass-card px-3 py-2 flex items-center gap-3 text-sm animate-fade-in glow-info">
          <span className="font-medium">
            {selectedIds.length} selected
          </span>
          <span className="text-muted-foreground text-xs">
            Mark as:
          </span>
          <button
            type="button"
            onClick={() => bulkUpdate.mutate("fixed")}
            className="badge badge-low hover:opacity-90 flex items-center gap-1"
          >
            <CheckCircle2Icon className="size-3" /> Fixed
          </button>
          <button
            type="button"
            onClick={() => bulkUpdate.mutate("wont_fix")}
            className="badge badge-info hover:opacity-90 flex items-center gap-1"
          >
            <CircleSlashIcon className="size-3" /> Won't fix
          </button>
          <button
            type="button"
            onClick={() => bulkUpdate.mutate("false_positive")}
            className="badge badge-info hover:opacity-90 flex items-center gap-1"
          >
            <AlertOctagonIcon className="size-3" /> False positive
          </button>
          <button
            type="button"
            onClick={onClearSelection}
            className="ml-auto text-xs text-muted-foreground hover:text-foreground"
          >
            Clear selection
          </button>
        </div>
      )}
    </div>
  );
}
