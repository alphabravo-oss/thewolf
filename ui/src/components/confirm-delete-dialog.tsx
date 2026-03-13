"use client";

import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface ConfirmDeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /** The name the user must type to confirm deletion. */
  confirmName: string;
  /** What kind of thing is being deleted (e.g. "collection", "repository"). */
  entityType: string;
  /** Warning lines shown in red before the input. */
  warnings: string[];
  /** Called when the user confirms. Return a promise if async. */
  onConfirm: () => void | Promise<void>;
  /** Whether the delete is currently in progress. */
  deleting?: boolean;
}

export function ConfirmDeleteDialog({
  open,
  onOpenChange,
  title,
  confirmName,
  entityType,
  warnings,
  onConfirm,
  deleting = false,
}: ConfirmDeleteDialogProps) {
  const [typed, setTyped] = useState("");

  useEffect(() => {
    if (!open) setTyped("");
  }, [open]);

  const matches = typed === confirmName;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="text-destructive">{title}</DialogTitle>
          <DialogDescription>
            This action cannot be undone. This will permanently delete the{" "}
            <span className="font-semibold">{entityType}</span> and all associated data.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 pt-2">
          {warnings.length > 0 && (
            <div className="rounded-md border border-red-200 dark:border-red-800 bg-red-50/50 dark:bg-red-950/20 p-3 space-y-1">
              {warnings.map((w, i) => (
                <p key={i} className="text-sm text-red-700 dark:text-red-400">
                  {w}
                </p>
              ))}
            </div>
          )}
          <div className="space-y-2">
            <Label htmlFor="confirmDeleteInput">
              Type <span className="font-mono font-semibold">{confirmName}</span> to confirm
            </Label>
            <Input
              id="confirmDeleteInput"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              placeholder={confirmName}
              autoComplete="off"
              autoFocus
            />
          </div>
          <div className="flex gap-2 pt-1">
            <Button
              variant="destructive"
              onClick={onConfirm}
              disabled={!matches || deleting}
              className="flex-1"
            >
              {deleting ? "Deleting..." : `Delete ${entityType}`}
            </Button>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={deleting}>
              Cancel
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
