// Confirm delete of a repo or collection, with an explicit choice to
// also purge scan/finding records.
import { useEffect, useId, useState } from "react";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { ConfirmDialog } from "@/components/confirm-dialog";

export function DeleteWithRecordsDialog({
  open,
  onOpenChange,
  kind,
  name,
  recordCount,
  pending,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  kind: "repo" | "collection";
  name: string;
  recordCount?: number;
  pending?: boolean;
  onConfirm: (purge: boolean) => void;
}) {
  const boxId = useId();
  const [purge, setPurge] = useState(false);

  useEffect(() => {
    if (open) setPurge(false);
  }, [open]);

  // A repo with scan history cannot be removed unless records go with it
  // (the API returns 409 otherwise). Treat "count still loading" as
  // must-purge so we never fire a doomed request.
  const hasRecords = kind === "repo" && (recordCount === undefined || recordCount > 0);
  const recordsLabel =
    kind === "repo"
      ? "scan history, findings, and artifacts for this repo"
      : "scan history and findings recorded on this collection";
  const keepHint =
    kind === "repo"
      ? hasRecords
        ? "This repo has scan history. Check “Also remove records” or Fleet will keep showing those findings."
        : "The repo is removed from Wolf. Source code on disk or in git is not touched."
      : "The collection grouping is removed. Repos stay (moved to Default). Scan history stays unless you check the box below.";

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Delete ${kind} “${name}”?`}
      description={keepHint}
      confirmLabel={purge || hasRecords ? "Delete and remove records" : "Delete"}
      pending={pending}
      confirmDisabled={hasRecords && !purge}
      onConfirm={() => {
        if (hasRecords && !purge) return;
        onConfirm(hasRecords ? true : purge);
      }}
    >
      <label
        htmlFor={boxId}
        className={`flex items-start gap-3 rounded-md border p-3 text-sm ${
          hasRecords && !purge
            ? "border-status-warning/50 bg-status-warning/10"
            : "border-border bg-muted/20"
        }`}
      >
        <Checkbox
          id={boxId}
          checked={purge}
          onCheckedChange={(v) => setPurge(v === true)}
          className="mt-0.5"
        />
        <span>
          <Label htmlFor={boxId} className="font-medium">
            Also remove records{hasRecords ? " (required)" : ""}
          </Label>
          <span className="mt-0.5 block text-xs text-muted-foreground">
            Permanently delete {recordsLabel}
            {typeof recordCount === "number"
              ? ` (${recordCount} scan${recordCount === 1 ? "" : "s"})`
              : ""}
            . This cannot be undone.
          </span>
        </span>
      </label>
    </ConfirmDialog>
  );
}
