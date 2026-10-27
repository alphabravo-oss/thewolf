// Create/edit form in a Dialog (short forms) or Sheet (long forms): title,
// description, body, Cancel / primary footer, a pending state that locks the
// form, an inline form-level error, and a dirty-guard so Esc, overlay click, the
// X and Cancel ask before discarding typed input. Radix handles focus trap and
// return. Put per-field errors next to inputs with <FieldError>.
import { useState, type FormEvent, type ReactNode } from "react";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";

export function FieldError({ id, children }: { id?: string; children?: ReactNode }) {
  if (!children) return null;
  return (
    <p id={id} role="alert" className="mt-1 text-xs text-destructive-text">
      {children}
    </p>
  );
}

export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  onSubmit,
  submitLabel = "Save",
  cancelLabel = "Cancel",
  pending = false,
  dirty = false,
  submitDisabled = false,
  error,
  presentation = "dialog",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  children: ReactNode;
  onSubmit: () => void | Promise<void>;
  submitLabel?: string;
  cancelLabel?: string;
  /** Locks the fields and both buttons, and blocks closing, while saving. */
  pending?: boolean;
  /** When true, closing asks to discard first. */
  dirty?: boolean;
  submitDisabled?: boolean;
  /** Form-level error (for example the API message); per-field errors go in the body. */
  error?: ReactNode;
  presentation?: "dialog" | "sheet";
}) {
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  const requestOpenChange = (next: boolean) => {
    if (next) return onOpenChange(true);
    if (pending) return;
    if (dirty) return setConfirmDiscard(true);
    onOpenChange(false);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!pending) void onSubmit();
  };

  // Radix warns when a dialog has no Description; opt out explicitly.
  const noDescription = description ? {} : { "aria-describedby": undefined };
  const Title = presentation === "sheet" ? SheetTitle : DialogTitle;
  const Description = presentation === "sheet" ? SheetDescription : DialogDescription;
  const Header = presentation === "sheet" ? SheetHeader : DialogHeader;
  const Footer = presentation === "sheet" ? SheetFooter : DialogFooter;

  const form = (
    <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col gap-4">
      <Header>
        <Title>{title}</Title>
        {description ? <Description>{description}</Description> : null}
      </Header>
      <fieldset disabled={pending} className="m-0 min-w-0 flex-1 space-y-4 overflow-y-auto border-0 p-0">
        {error ? (
          <p role="alert" className="rounded-md border border-destructive/50 p-3 text-sm text-destructive-text">
            {error}
          </p>
        ) : null}
        {children}
      </fieldset>
      <Footer className="gap-2 sm:gap-2 sm:space-x-0">
        <Button type="button" variant="ghost" disabled={pending} onClick={() => requestOpenChange(false)}>
          {cancelLabel}
        </Button>
        <Button type="submit" disabled={pending || submitDisabled}>
          {pending ? "Saving…" : submitLabel}
        </Button>
      </Footer>
    </form>
  );

  return (
    <>
      {presentation === "sheet" ? (
        <Sheet open={open} onOpenChange={requestOpenChange}>
          <SheetContent className="flex w-full flex-col sm:max-w-xl" {...noDescription}>
            {form}
          </SheetContent>
        </Sheet>
      ) : (
        <Dialog open={open} onOpenChange={requestOpenChange}>
          <DialogContent className="sm:max-w-lg" {...noDescription}>{form}</DialogContent>
        </Dialog>
      )}
      <ConfirmDialog
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        title="Discard changes?"
        description="You have unsaved input. Closing now will lose it."
        confirmLabel="Discard"
        cancelLabel="Keep editing"
        onConfirm={() => {
          setConfirmDiscard(false);
          onOpenChange(false);
        }}
      />
    </>
  );
}
