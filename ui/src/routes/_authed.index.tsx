// ui/src/routes/_authed.index.tsx
import { createFileRoute } from "@tanstack/react-router";
import { PostureCards } from "@/components/fleet/posture-cards";
import { SeverityTrend } from "@/components/fleet/severity-trend";
import { TopComponents } from "@/components/fleet/top-components";
import { NeedsAttention } from "@/components/fleet/needs-attention";
import { InventoryBreakdown } from "@/components/fleet/inventory-breakdown";
import { RecentActivity } from "@/components/fleet/recent-activity";

import { PageHeader, PageShell } from "@/components/ui/page";

export const Route = createFileRoute("/_authed/")({ component: FleetDashboard });

function FleetDashboard() {
  return (
    <PageShell>
      <div className="reveal reveal-1">
        <PageHeader
          title="Fleet"
          description="Open findings, posture, and inventory across every repository wolf manages."
        />
      </div>
      <div className="reveal reveal-2">
        <PostureCards />
      </div>
      <div className="reveal reveal-3">
        <SeverityTrend />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 reveal reveal-4">
        <TopComponents />
        <NeedsAttention />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 reveal reveal-5">
        <InventoryBreakdown />
        <RecentActivity />
      </div>
    </PageShell>
  );
}
