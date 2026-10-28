// Enterprise hub: every Enterprise module grouped by what it is for, with its
// entitlement state, a one-line purpose and a primary action. Locked modules
// show the shared EditionGate treatment instead of an action.
import { createFileRoute, Link } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EditionGate } from "@/components/ui/edition-gate";
import { ErrorState, LoadingState } from "@/components/ui/empty-state";
import { MetricCard } from "@/components/ui/metric-card";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { StatusBadge } from "@/components/ui/status-badge";
import { useEnterpriseRecords } from "@/components/enterprise-records";
import { ENTERPRISE_GROUPS, ENTERPRISE_MODULES, type EnterpriseModule } from "@/lib/enterprise-modules";
import { editionProduct, entitled, useEdition, type EditionInfo } from "@/lib/edition";

export const Route = createFileRoute("/_authed/enterprise/")({
  component: EnterpriseHub,
});

const isOpen = (m: EnterpriseModule, edition?: EditionInfo) => m.capability === null || entitled(edition, m.capability);

function ModuleStatus({ module, open }: { module: EnterpriseModule; open: boolean }) {
  const q = useEnterpriseRecords(module.countPath ?? "", open && !!module.countPath);
  if (!open) return null;
  if (!module.countPath) return <p className="text-xs text-muted-foreground">Available</p>;
  if (q.isLoading) return <p className="text-xs text-muted-foreground">Checking…</p>;
  if (q.isError) return <p className="text-xs text-muted-foreground">Not reporting</p>;
  const n = q.data?.length ?? 0;
  return <p className="text-xs text-muted-foreground">{n === 0 ? "Nothing configured yet" : `${n} configured`}</p>;
}

function ModuleCard({ module: m, edition }: { module: EnterpriseModule; edition?: EditionInfo }) {
  const open = isOpen(m, edition);
  const action = (
    <Button asChild size="sm" variant={open ? "default" : "outline"}>
      <Link to={m.to as string} search={m.search as never}>{m.action}</Link>
    </Button>
  );
  return (
    <Card role="article" aria-label={m.title} className="flex flex-col">
      <CardContent className="flex flex-1 flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <span className="rounded-md bg-muted p-2 text-muted-foreground"><m.Icon className="size-4" aria-hidden /></span>
            <h3 className="truncate text-sm font-semibold">{m.title}</h3>
          </div>
          <StatusBadge status={open ? "active" : "neutral"} label={m.capability === null ? "Included" : open ? "Entitled" : "Locked"} size="sm" className="shrink-0" />
        </div>
        <p className="text-sm text-muted-foreground">{m.purpose}</p>
        <ModuleStatus module={m} open={open} />
        <div className="mt-auto pt-1">
          {m.capability === null ? action : (
            <EditionGate feature={m.title} capability={m.capability} className="border-0 bg-transparent p-0">{action}</EditionGate>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function EnterpriseHub() {
  const edition = useEdition();
  if (edition.isPending) return <PageShell><LoadingState title="Loading edition" /></PageShell>;
  if (edition.isError) return <PageShell><ErrorState title="Could not load the edition" onRetry={() => void edition.refetch()} /></PageShell>;
  const gated = ENTERPRISE_MODULES.filter((m) => m.capability !== null);
  const available = gated.filter((m) => isOpen(m, edition.data)).length;
  return (
    <PageShell>
      <PageHeader
        title="Enterprise"
        description="Identity, governance, integrations, data controls and operations for this installation."
        actions={<Button asChild variant="outline" size="sm"><Link to="/settings" search={{ tab: "license" }}>Manage license</Link></Button>}
      />
      <div className="grid gap-3 sm:grid-cols-3">
        <MetricCard title="Edition" value={editionProduct(edition.data)} subtitle={edition.data?.licensed ? "Commercial license active" : "No commercial license"} />
        <MetricCard title="Modules available" value={`${available} of ${gated.length}`} subtitle="Granted by this binary and license" />
        <MetricCard title="Locked" value={gated.length - available} subtitle={gated.length - available ? "Contact us to enable" : "Everything is unlocked"} />
      </div>
      {ENTERPRISE_GROUPS.map((group) => (
        <PageSection key={group} title={group}>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {ENTERPRISE_MODULES.filter((m) => m.group === group).map((m) => (
              <ModuleCard key={m.key} module={m} edition={edition.data} />
            ))}
          </div>
        </PageSection>
      ))}
    </PageShell>
  );
}
