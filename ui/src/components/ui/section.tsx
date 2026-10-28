// Collapsible disclosure for the non-decision-relevant 80% of a screen. Replaces
// every raw <details>: a real button carries aria-expanded/aria-controls, so it
// is keyboard accessible and announced, and the body stays mounted (hidden) so
// child state survives a collapse.
import { useId, useState, type ReactNode } from "react";
import { ChevronRightIcon } from "lucide-react";
import { cn } from "@/lib/utils";

export function Section({
  title,
  summary,
  defaultOpen = false,
  variant = "boxed",
  id,
  className,
  triggerClassName,
  children,
}: {
  title: ReactNode;
  /** One-line hint shown beside the title while collapsed or open. */
  summary?: ReactNode;
  defaultOpen?: boolean;
  /** `boxed` draws the card border; `plain` is a bare disclosure row. */
  variant?: "boxed" | "plain";
  id?: string;
  className?: string;
  triggerClassName?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const bodyId = useId();
  return (
    <div id={id} className={cn(variant === "boxed" && "rounded-lg border border-border", className)}>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={bodyId}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "flex w-full items-center gap-2 rounded-lg text-left text-sm",
          variant === "boxed" ? "px-3 py-2.5 font-medium" : "py-1 text-muted-foreground",
          triggerClassName,
        )}
      >
        <ChevronRightIcon aria-hidden className={cn("size-4 shrink-0 transition-transform", open && "rotate-90")} />
        <span className="min-w-0">{title}</span>
        {summary ? <span className="ml-auto shrink-0 text-xs font-normal text-muted-foreground">{summary}</span> : null}
      </button>
      <div id={bodyId} hidden={!open} className={variant === "boxed" ? "px-3 pb-3" : "pl-6 pt-1"}>
        {children}
      </div>
    </div>
  );
}
