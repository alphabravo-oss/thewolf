// Page scaffolding — ported verbatim from
// astronomer/frontend/src/components/ui/page.tsx.
//
// Every Wolf route composes these three primitives instead of hand-rolling its
// own header markup, which is what keeps heading sizes, description widths, and
// action alignment identical between the two consoles.
import type { ReactNode } from "react";
import { Link, type LinkProps } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";
import { cn } from "@/lib/utils";

/** Detail pages pass the trail back to their list: [{ label: "Repositories", to: "/repos" }, { label: name }]. */
export type Breadcrumb = { label: string; to?: LinkProps["to"] };

function Breadcrumbs({ items }: { items: Breadcrumb[] }) {
  return (
    <nav aria-label="Breadcrumb" className="mb-1.5">
      <ol className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
        {items.map((item, i) => (
          <li key={`${i}-${item.label}`} className="flex min-w-0 items-center gap-1">
            {i > 0 ? <ChevronRightIcon className="size-3 shrink-0" aria-hidden /> : null}
            {item.to ? (
              <Link to={item.to} className="hover:text-foreground hover:underline">{item.label}</Link>
            ) : (
              <span aria-current="page" className="truncate text-foreground">{item.label}</span>
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}

export function PageShell({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <div className={cn("space-y-6", className)}>{children}</div>;
}

export function PageHeader({
  title,
  description,
  eyebrow,
  breadcrumbs,
  status,
  actions,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  eyebrow?: ReactNode;
  breadcrumbs?: Breadcrumb[];
  /** A StatusBadge (or similar) shown beside the title on detail pages. */
  status?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("page-header", className)}>
      <div className="min-w-0">
        {breadcrumbs?.length ? <Breadcrumbs items={breadcrumbs} /> : null}
        {eyebrow ? <div className="eyebrow">{eyebrow}</div> : null}
        <div className="flex min-w-0 items-center gap-3">
          <h1 className="truncate text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
          {status ? <div className="shrink-0">{status}</div> : null}
        </div>
        {description ? (
          <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function PageSection({
  children,
  title,
  description,
  actions,
  className,
}: {
  children: ReactNode;
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("space-y-3", className)}>
      {title || description || actions ? (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
          <div className="min-w-0">
            {title ? <h2 className="text-sm font-semibold text-foreground">{title}</h2> : null}
            {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
          </div>
          {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
      ) : null}
      {children}
    </section>
  );
}
