// ui/src/routes/_authed.index.tsx
import { createFileRoute } from "@tanstack/react-router";
import { PostureCards } from "@/components/fleet/posture-cards";
import { SeverityTrend } from "@/components/fleet/severity-trend";
import { TopComponents } from "@/components/fleet/top-components";
import { NeedsAttention } from "@/components/fleet/needs-attention";
import { InventoryBreakdown } from "@/components/fleet/inventory-breakdown";
import { RecentActivity } from "@/components/fleet/recent-activity";

export const Route = createFileRoute("/_authed/")({ component: FleetDashboard });

function FleetDashboard() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Fleet</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Open findings, posture, and inventory across every repository wolf manages.
        </p>
      </div>
      <PostureCards />
      <SeverityTrend />
      <div className="grid grid-cols-2 gap-6">
        <TopComponents />
        <NeedsAttention />
      </div>
      <div className="grid grid-cols-2 gap-6">
        <InventoryBreakdown />
        <RecentActivity />
      </div>
    </div>
  );
}
