// ui/src/components/fleet/severity-trend.tsx
import { useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api } from "@/lib/api";
import { severityChartColor } from "@/lib/severity";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

type TrendEntry = {
  date: string;
  counts: {
    critical: number;
    high: number;
    medium: number;
    low: number;
    info: number;
    total: number;
  };
};

type ChartRow = {
  date: string;
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
};

function useFindingTrends() {
  return useQuery({
    queryKey: ["findings", "trends"],
    queryFn: async () => {
      const { data } = await api.get<TrendEntry[]>("/findings/trends");
      return data ?? [];
    },
    staleTime: 60_000,
  });
}

export function SeverityTrend() {
  const q = useFindingTrends();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Findings over time</CardTitle>
      </CardHeader>
      <CardContent>
        {q.isLoading ? (
          <Skeleton className="h-56 w-full" />
        ) : !q.data || q.data.length === 0 ? (
          <div className="h-32 grid place-items-center text-sm text-muted-foreground">
            No trend data yet — run scans over time to see the curve.
          </div>
        ) : (
          <div className="h-56">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={toChartRows(q.data)} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" />
                <XAxis dataKey="date" stroke="var(--color-muted-foreground)" fontSize={11} tickLine={false} />
                <YAxis stroke="var(--color-muted-foreground)" fontSize={11} allowDecimals={false} tickLine={false} axisLine={false} width={28} />
                <Tooltip
                  contentStyle={{
                    background: "var(--color-popover)",
                    border: "1px solid var(--color-border)",
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                />
                <Area type="monotone" dataKey="critical" stackId="1" stroke={severityChartColor.critical} fill={severityChartColor.critical} fillOpacity={0.7} />
                <Area type="monotone" dataKey="high" stackId="1" stroke={severityChartColor.high} fill={severityChartColor.high} fillOpacity={0.7} />
                <Area type="monotone" dataKey="medium" stackId="1" stroke={severityChartColor.medium} fill={severityChartColor.medium} fillOpacity={0.7} />
                <Area type="monotone" dataKey="low" stackId="1" stroke={severityChartColor.low} fill={severityChartColor.low} fillOpacity={0.7} />
                <Area type="monotone" dataKey="info" stackId="1" stroke={severityChartColor.info} fill={severityChartColor.info} fillOpacity={0.7} />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function toChartRows(entries: TrendEntry[]): ChartRow[] {
  // Last 90 days.
  const cutoff = new Date();
  cutoff.setDate(cutoff.getDate() - 90);
  const cutoffStr = cutoff.toISOString().slice(0, 10);
  return entries
    .filter((e) => e.date >= cutoffStr)
    .map((e) => ({
      date: e.date,
      critical: e.counts.critical,
      high: e.counts.high,
      medium: e.counts.medium,
      low: e.counts.low,
      info: e.counts.info,
    }));
}
