// Create or edit a collection (name + description) in a FormDialog. Mounted
// only while open so each opening starts from the supplied initial values.
import { useState } from "react";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";

export type CollectionFormBody = { name: string; description: string };

export function CollectionFormDialog({
  open,
  onOpenChange,
  initial,
  onSubmit,
  pending,
  error,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Present when editing; absent when creating. */
  initial?: CollectionFormBody;
  onSubmit: (body: CollectionFormBody) => void;
  pending: boolean;
  error?: unknown;
}) {
  return open ? (
    <Body onOpenChange={onOpenChange} initial={initial} onSubmit={onSubmit} pending={pending} error={error} />
  ) : null;
}

function Body({ onOpenChange, initial, onSubmit, pending, error }: Omit<Parameters<typeof CollectionFormDialog>[0], "open">) {
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const editing = !!initial;
  return (
    <FormDialog
      open
      onOpenChange={(next) => { if (!next) onOpenChange(false); }}
      title={editing ? "Edit collection" : "New collection"}
      description={editing ? undefined : "Group repositories for batch scanning and cross-repo metrics."}
      submitLabel={editing ? "Save" : "Create"}
      submitDisabled={!name.trim()}
      pending={pending}
      dirty={name !== (initial?.name ?? "") || description !== (initial?.description ?? "")}
      error={error ? (error instanceof Error ? error.message : editing ? "Failed to update collection" : "Failed to create collection") : undefined}
      onSubmit={() => { if (name.trim()) onSubmit({ name: name.trim(), description: description.trim() }); }}
    >
      <label className="block">
        <span className="text-xs text-muted-foreground">Name *</span>
        <Input
          className="mt-1"
          required
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Backend services"
        />
      </label>
      <label className="block">
        <span className="text-xs text-muted-foreground">Description</span>
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Optional — what repos belong here?"
          rows={3}
          className="mt-1 w-full resize-none rounded-md border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
      </label>
    </FormDialog>
  );
}
