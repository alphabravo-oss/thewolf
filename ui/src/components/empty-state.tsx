// Full-width workspace empty state. List pages render one of these when
// there is nothing to show — a surface that spans the page, not a narrow
// centered card.
import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

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
    <section className={cn("glass-card empty-state", className)}>
      {Icon ? (
        <div className="empty-icon" aria-hidden="true">
          <Icon />
        </div>
      ) : null}
      <h3>{title}</h3>
      {description ? <p>{description}</p> : null}
      {cta ? (
        <div className="empty-actions">
          {"to" in cta ? (
            <Button asChild>
              <Link to={cta.to}>{cta.label}</Link>
            </Button>
          ) : (
            <Button type="button" onClick={cta.onClick}>
              {cta.label}
            </Button>
          )}
        </div>
      ) : null}
    </section>
  );
}
