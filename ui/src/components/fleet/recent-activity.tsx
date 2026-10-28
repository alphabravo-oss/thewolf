// ui/src/components/fleet/recent-activity.tsx
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { describeAudit } from "@/lib/audit";
import { formatRelativeTime } from "@/lib/utils";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Section } from "@/components/ui/section";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";

type AuditEntry = {
  id: string;
  user_id: string;
  action: string;
  method: string;
  path: string;
  event_type?: string;
  resource_id?: string;
  status_code: number;
  created_at: string;
};

function useRecentAudit() {
  return useQuery({
    queryKey: ["audit-log", "recent"],
    queryFn: async () => {
      const { data } = await api.get<AuditEntry[]>("/audit-log?limit=6");
      return data ?? [];
    },
    staleTime: 30_000,
  });
}

export function RecentActivity() {
  const q = useRecentAudit();
  const rows = q.data ?? [];

  return (
    <Card className="flex h-full flex-col">
      <CardHeader>
        <CardTitle className="text-base">Recent activity</CardTitle>
        <Link to="/audit" className="text-xs font-medium text-primary underline-offset-4 hover:underline">Audit log →</Link>
      </CardHeader>
      <CardContent className="min-h-48 flex-1">
        {q.isLoading ? (
          <Skeleton className="h-40 w-full" />
        ) : q.isError ? (
          <div role="alert" className="text-sm text-muted-foreground">Recent activity could not be loaded.</div>
        ) : rows.length === 0 ? (
          <div className="text-sm text-muted-foreground">No recent activity.</div>
        ) : (
          <>
            <ul className="divide-y divide-border">
              {rows.map((e) => {
                const { text, failed } = describeAudit(e);
                return (
                  <li key={e.id} className="flex items-center gap-3 py-2 text-sm">
                    <span className="min-w-0 flex-1 truncate">{text}</span>
                    {failed && <StatusBadge status="failed" label={e.status_code === 401 || e.status_code === 403 ? "Denied" : "Failed"} size="sm" showDot={false} />}
                    <span className="shrink-0 text-xs text-muted-foreground tabular-nums" title={new Date(e.created_at).toLocaleString()}>
                      {formatRelativeTime(e.created_at)}
                    </span>
                  </li>
                );
              })}
            </ul>
            <Section variant="plain" className="mt-2 text-xs" title="Raw requests">
              <ul className="space-y-1 font-mono text-xs text-muted-foreground">
                {rows.map((e) => (
                  <li key={e.id} className="break-all">{e.method} {e.path} · {e.status_code}{e.resource_id ? ` · ${e.resource_id}` : ""}</li>
                ))}
              </ul>
            </Section>
          </>
        )}
      </CardContent>
    </Card>
  );
}
