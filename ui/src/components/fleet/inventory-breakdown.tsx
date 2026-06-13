// ui/src/components/fleet/inventory-breakdown.tsx
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useFleetInventory } from "@/lib/fleet";

export function InventoryBreakdown() {
  const q = useFleetInventory();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Inventory</CardTitle>
      </CardHeader>
      <CardContent>
        {q.isLoading ? (
          <Skeleton className="h-48 w-full" />
        ) : !q.data ? (
          <div className="text-sm text-muted-foreground">No inventory yet.</div>
        ) : (
          <div className="grid grid-cols-3 gap-6">
            <BreakdownColumn label="Source" data={q.data.by_source_type} />
            <BreakdownColumn label="Collection" data={q.data.by_collection} />
            <BreakdownColumn label="Language" data={q.data.by_language} />
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function BreakdownColumn({ label, data }: { label: string; data: Record<string, number> }) {
  const entries = Object.entries(data).sort((a, b) => b[1] - a[1]);
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">{label}</div>
      {entries.length === 0 ? (
        <div className="text-xs text-muted-foreground">—</div>
      ) : (
        <ul className="space-y-1.5">
          {entries.map(([key, value]) => (
            <li key={key} className="flex items-center justify-between text-sm">
              <span className="truncate">{key || "—"}</span>
              <span className="tabular-nums text-muted-foreground">{value}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
