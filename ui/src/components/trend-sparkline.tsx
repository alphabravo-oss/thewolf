"use client";

import {
  ResponsiveContainer,
  LineChart,
  Line,
} from "recharts";

export function TrendSparkline({
  data,
  color = "hsl(var(--primary))",
  height = 30,
}: {
  data: number[];
  color?: string;
  height?: number;
}) {
  const chartData = data.map((value, index) => ({ index, value }));

  return (
    <div style={{ width: "100%", height }}>
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={chartData}>
          <Line
            type="monotone"
            dataKey="value"
            stroke={color}
            strokeWidth={1.5}
            dot={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
