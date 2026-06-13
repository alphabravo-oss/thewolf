// ui/src/components/fleet/recent-activity.tsx
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

type AuditEntry = {
  id: string;
  user_id: string;
  action: string;
  method: string;
  path: string;
  resource_id?: string;
  status_code: number;
  created_at: string;
};

function useRecentAudit() {
  return useQuery({
    queryKey: ["audit-log", "recent"],
    queryFn: async () => {
      const { data } = await api.get<AuditEntry[]>("/audit-log?limit=5");
      return data ?? [];
    },
    staleTime: 30_000,
  });
}

export function RecentActivity() {
  const q = useRecentAudit();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Recent activity</CardTitle>
      </CardHeader>
      <CardContent>
        {q.isLoading ? (
          <Skeleton className="h-48 w-full" />
        ) : !q.data || q.data.length === 0 ? (
          <div className="text-sm text-muted-foreground">No recent activity.</div>
        ) : (
          <ul className="space-y-2">
            {q.data.map((e) => (
              <li key={e.id} className="text-sm flex items-baseline gap-3">
                <span className="font-mono text-xs text-muted-foreground w-12 shrink-0">
                  {e.method}
                </span>
                <span className="flex-1 truncate font-mono text-xs">{e.path}</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {timeAgo(e.created_at)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  const now = Date.now();
  const sec = Math.max(1, Math.round((now - then) / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h`;
  const day = Math.round(hr / 24);
  return `${day}d`;
}
