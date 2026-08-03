import { useEffect, useId, useState } from "react";
import { Loader2Icon } from "lucide-react";
import { Button, type ButtonProps } from "@/components/ui/button";
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

export function ActionDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pending,
  destructive,
  reasonRequired = true,
  confirmationText,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel: string;
  pending?: boolean;
  destructive?: boolean;
  reasonRequired?: boolean;
  confirmationText?: string;
  onConfirm: (reason: string) => void;
}) {
  const reasonId = useId();
  const confirmationId = useId();
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState("");

  useEffect(() => {
    if (!open) {
      setReason("");
      setConfirmation("");
    }
  }, [open]);

  const validReason = !reasonRequired || reason.trim().length >= 3;
  const validConfirmation =
    !confirmationText || confirmation.trim() === confirmationText;

  return (
    <Dialog open={open} onOpenChange={pending ? undefined : onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {reasonRequired ? (
            <div className="space-y-1.5">
              <Label htmlFor={reasonId}>Reason</Label>
              <textarea
                id={reasonId}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                placeholder="Explain why this action is required"
                rows={3}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
              <p className="text-xs text-muted-foreground">
                This reason is written to the immutable audit history.
              </p>
            </div>
          ) : null}
          {confirmationText ? (
            <div className="space-y-1.5">
              <Label htmlFor={confirmationId}>
                Type <span className="font-mono">{confirmationText}</span> to confirm
              </Label>
              <Input
                id={confirmationId}
                value={confirmation}
                onChange={(event) => setConfirmation(event.target.value)}
                autoComplete="off"
              />
            </div>
          ) : null}
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={pending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant={destructive ? "destructive" : "default"}
            disabled={!validReason || !validConfirmation || pending}
            aria-busy={pending}
            onClick={() => onConfirm(reason.trim())}
          >
            {pending ? <Loader2Icon className="animate-spin" /> : null}
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ActionButton({
  pending,
  children,
  ...props
}: ButtonProps & { pending?: boolean }) {
  return (
    <Button
      {...props}
      disabled={props.disabled || pending}
      aria-busy={pending || undefined}
    >
      {pending ? <Loader2Icon className="animate-spin" aria-hidden="true" /> : null}
      {children}
    </Button>
  );
}
