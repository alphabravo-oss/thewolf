// Empty-state card. Every list view in wolf renders one of these when
// the underlying collection/scan/finding list is empty — astronomer-style
// "no data yet, here's what to do next".
import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { cn } from "@/lib/cn";

export function EmptyState({
  icon: Icon,
  title,
  description,
  cta,
  className,
}: {
  icon?: React.ComponentType<{ className?: string }>;
  title: string;
  description?: ReactNode;
  cta?:
    | { label: string; to: string }
    | { label: string; onClick: () => void };
  className?: string;
}) {
  return (
    <div
      className={cn(
        "glass-card p-12 text-center max-w-xl mx-auto",
        className,
      )}
    >
      {Icon && (
        <div className="size-12 mx-auto mb-4 rounded-full bg-muted/30 grid place-items-center">
          <Icon className="size-6 text-muted-foreground" />
        </div>
      )}
      <div className="text-base font-semibold mb-1">{title}</div>
      {description && (
        <div className="text-sm text-muted-foreground mb-5">{description}</div>
      )}
      {cta && (
        <div>
          {"to" in cta ? (
            <Link
              to={cta.to}
              className="inline-flex items-center px-3 h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition"
            >
              {cta.label}
            </Link>
          ) : (
            <button
              type="button"
              onClick={cta.onClick}
              className="inline-flex items-center px-3 h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition"
            >
              {cta.label}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
