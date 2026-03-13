"use client";

import { useState } from "react";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  type TooltipProps,
} from "recharts";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import type { TrendEntry } from "@/lib/types";

type TimeRange = "7d" | "30d" | "90d" | "all";

const SEVERITY_COLORS = {
  critical: "#ef4444",
  high: "#f97316",
  medium: "#eab308",
  low: "#3b82f6",
  info: "#6b7280",
} as const;

const SEVERITY_LABELS: Record<string, string> = {
  critical: "Critical",
  high: "High",
  medium: "Medium",
  low: "Low",
  info: "Info",
};

interface TrendChartProps {
  data: TrendEntry[];
  title?: string;
  timeRange?: TimeRange;
  onTimeRangeChange?: (range: TimeRange) => void;
  height?: number;
}

function filterByTimeRange(data: TrendEntry[], range: TimeRange): TrendEntry[] {
  if (range === "all") return data;

  const days = range === "7d" ? 7 : range === "30d" ? 30 : 90;
  const cutoff = new Date();
  cutoff.setDate(cutoff.getDate() - days);
  const cutoffStr = cutoff.toISOString().slice(0, 10);

  return data.filter((entry) => entry.date >= cutoffStr);
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr + "T00:00:00");
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

function CustomTooltip(props: TooltipProps<number, string>) {
  const { active, payload, label } = props as TooltipProps<number, string> & {
    active?: boolean;
    payload?: Array<{ value?: number; dataKey?: string; color?: string; name?: string }>;
    label?: string;
  };
  if (!active || !payload?.length) return null;

  return (
    <div className="bg-popover border rounded-lg shadow-lg p-3 text-sm">
      <p className="font-medium mb-1.5">{formatDate(label as string)}</p>
      <div className="space-y-1">
        {payload
          .filter((p) => (p.value ?? 0) > 0)
          .reverse()
          .map((p) => (
            <div key={p.dataKey} className="flex items-center gap-2">
              <span
                className="w-2.5 h-2.5 rounded-full shrink-0"
                style={{ backgroundColor: p.color }}
              />
              <span className="text-muted-foreground">
                {SEVERITY_LABELS[p.dataKey as string] ?? p.dataKey}
              </span>
              <span className="ml-auto font-medium">{p.value}</span>
            </div>
          ))}
      </div>
    </div>
  );
}

const TIME_RANGE_OPTIONS: { value: TimeRange; label: string }[] = [
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "90d", label: "90d" },
  { value: "all", label: "All" },
];

export function TrendChart({
  data,
  title = "Finding Trends",
  timeRange: controlledRange,
  onTimeRangeChange,
  height = 300,
}: TrendChartProps) {
  const [internalRange, setInternalRange] = useState<TimeRange>("30d");
  const activeRange = controlledRange ?? internalRange;

  const handleRangeChange = (range: TimeRange) => {
    if (onTimeRangeChange) {
      onTimeRangeChange(range);
    } else {
      setInternalRange(range);
    }
  };

  const filtered = filterByTimeRange(data, activeRange);

  const chartData = filtered.map((entry) => ({
    date: entry.date,
    critical: entry.counts.critical,
    high: entry.counts.high,
    medium: entry.counts.medium,
    low: entry.counts.low,
    info: entry.counts.info,
  }));

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-base font-medium">{title}</CardTitle>
        <div className="flex gap-1">
          {TIME_RANGE_OPTIONS.map((opt) => (
            <Button
              key={opt.value}
              variant={activeRange === opt.value ? "default" : "ghost"}
              size="sm"
              className="h-7 px-2.5 text-xs"
              onClick={() => handleRangeChange(opt.value)}
            >
              {opt.label}
            </Button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        {chartData.length === 0 ? (
          <div
            className="flex items-center justify-center text-sm text-muted-foreground"
            style={{ height }}
          >
            No trend data available for this time range.
          </div>
        ) : (
          <div style={{ width: "100%", height }}>
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart
                data={chartData}
                margin={{ top: 4, right: 4, left: -20, bottom: 0 }}
              >
                <CartesianGrid
                  strokeDasharray="3 3"
                  className="stroke-muted"
                />
                <XAxis
                  dataKey="date"
                  tickFormatter={formatDate}
                  tick={{ fontSize: 11 }}
                  className="text-muted-foreground"
                />
                <YAxis
                  allowDecimals={false}
                  tick={{ fontSize: 11 }}
                  className="text-muted-foreground"
                />
                <Tooltip content={<CustomTooltip />} />
                <Area
                  type="monotone"
                  dataKey="info"
                  stackId="1"
                  stroke={SEVERITY_COLORS.info}
                  fill={SEVERITY_COLORS.info}
                  fillOpacity={0.3}
                />
                <Area
                  type="monotone"
                  dataKey="low"
                  stackId="1"
                  stroke={SEVERITY_COLORS.low}
                  fill={SEVERITY_COLORS.low}
                  fillOpacity={0.3}
                />
                <Area
                  type="monotone"
                  dataKey="medium"
                  stackId="1"
                  stroke={SEVERITY_COLORS.medium}
                  fill={SEVERITY_COLORS.medium}
                  fillOpacity={0.3}
                />
                <Area
                  type="monotone"
                  dataKey="high"
                  stackId="1"
                  stroke={SEVERITY_COLORS.high}
                  fill={SEVERITY_COLORS.high}
                  fillOpacity={0.3}
                />
                <Area
                  type="monotone"
                  dataKey="critical"
                  stackId="1"
                  stroke={SEVERITY_COLORS.critical}
                  fill={SEVERITY_COLORS.critical}
                  fillOpacity={0.3}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
