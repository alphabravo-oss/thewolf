// Empty-state card. Every list view in wolf renders one of these when
// the underlying collection/scan/finding list is empty — astronomer-style
// "no data yet, here's what to do next".
import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
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
    <Card className={cn("p-12 text-center max-w-xl mx-auto", className)}>
      <CardHeader className="p-0">
        {Icon && (
          <div className="size-12 mx-auto mb-4 rounded-full bg-muted/30 grid place-items-center">
            <Icon className="size-6 text-muted-foreground" />
          </div>
        )}
        <div className="text-base font-semibold">{title}</div>
        {description && (
          <div className="text-sm text-muted-foreground">{description}</div>
        )}
      </CardHeader>
      {cta && (
        <CardContent className="p-0 pt-5">
          {"to" in cta ? (
            <Button asChild>
              <Link to={cta.to}>{cta.label}</Link>
            </Button>
          ) : (
            <Button type="button" onClick={cta.onClick}>
              {cta.label}
            </Button>
          )}
        </CardContent>
      )}
    </Card>
  );
}
