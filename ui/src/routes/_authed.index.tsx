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
    <div className="page stack">
      <header className="page-header reveal reveal-1">
        <div>
          <h1 className="page-title">Fleet</h1>
          <p className="page-subtitle">
            Open findings, posture, and inventory across every repository wolf manages.
          </p>
        </div>
      </header>
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
    </div>
  );
}
