// The one Enterprise-only treatment: children render when the running edition
// grants the feature, otherwise a locked card says what it does and links to the
// editions page. Pass `capability` for an entitlement key, or omit it to gate on
// the Enterprise edition itself.
import type { ReactNode } from "react";
import { LockIcon } from "lucide-react";
import { entitled, ENTERPRISE_INFO_URL, isEnterprise, useEdition } from "@/lib/edition";
import { cn } from "@/lib/utils";

export function EditionGate({
  feature,
  description,
  capability,
  className,
  children,
}: {
  /** Short feature name, used as "<feature> is a Wolf Enterprise feature". */
  feature: string;
  /** What it does and what stays available in Community. */
  description?: ReactNode;
  capability?: string;
  className?: string;
  children?: ReactNode;
}) {
  const edition = useEdition();
  if (edition.isPending) return <p role="status" className="text-sm text-muted-foreground">Loading edition…</p>;
  if (edition.isError) {
    return (
      <p role="alert" className="text-sm text-muted-foreground">
        Could not verify this edition. Reload to check which Enterprise modules are available.
      </p>
    );
  }
  const granted = capability ? entitled(edition.data, capability) : isEnterprise(edition.data);
  if (granted) return <>{children}</>;
  return (
    <div className={cn("flex items-start gap-3 rounded-lg border border-border bg-card p-4", className)}>
      <LockIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 space-y-1">
        <p className="text-sm font-medium text-foreground">{feature} is a Wolf Enterprise feature</p>
        {description ? <p className="max-w-3xl text-sm text-muted-foreground">{description}</p> : null}
        <a
          href={ENTERPRISE_INFO_URL}
          target="_blank"
          rel="noreferrer"
          className="inline-block text-sm font-medium text-primary underline underline-offset-4 hover:no-underline"
        >
          Contact us about Enterprise
        </a>
      </div>
    </div>
  );
}
