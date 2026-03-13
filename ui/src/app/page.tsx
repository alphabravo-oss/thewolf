"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { TrendChart } from "@/components/trend-chart";
import { StatusBadge } from "@/components/status-badge";
import { SeverityBadge } from "@/components/severity-badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import api from "@/lib/api";
import type { Collection, Scan, Finding, TrendEntry, Severity } from "@/lib/types";

export default function DashboardPage() {
  const { data: cols = [], isLoading: colsLoading } = useQuery({
    queryKey: ["collections"],
    queryFn: () => api.get<Collection[]>("/collections").then((r) => r.data ?? []),
    refetchInterval: 10_000,
  });

  const { data: scans = [], isLoading: scansLoading } = useQuery({
    queryKey: ["scans"],
    queryFn: () => api.get<Scan[]>("/scans").then((r) => r.data ?? []),
    refetchInterval: 10_000,
  });

  const { data: findings = [], isLoading: findingsLoading } = useQuery({
    queryKey: ["findings"],
    queryFn: () => api.get<Finding[]>("/findings").then((r) => r.data ?? []),
    refetchInterval: 10_000,
  });

  const { data: trendData = [] } = useQuery({
    queryKey: ["findings-trends"],
    queryFn: () => api.get<TrendEntry[]>("/findings/trends").then((r) => r.data ?? []),
    refetchInterval: 30_000,
  });

  const loading = colsLoading || scansLoading || findingsLoading;
  if (loading) return <LoadingSpinner />;

  const repoCount = cols.reduce((sum: number, c: Collection) => sum + (c.repo_count ?? 0), 0);
  const fixed = findings.filter((f: Finding) => f.status === "fixed").length;
  const bySev: Record<string, number> = { critical: 0, high: 0, medium: 0, low: 0, info: 0 };
  for (const f of findings) {
    if (f.severity in bySev) bySev[f.severity]++;
  }

  const fixPct = findings.length > 0
    ? Math.round((fixed / findings.length) * 100)
    : 0;
  const severities: Severity[] = ["critical", "high", "medium", "low", "info"];
  const recentScans = scans.slice(0, 5);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold">Dashboard</h1>
        <p className="text-muted-foreground">
          System overview and scan activity
        </p>
      </div>

      {/* Summary cards */}
      <div className="grid gap-4 md:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Collections</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-3xl font-bold">{cols.length}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Repositories</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-3xl font-bold">{repoCount}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Total Scans</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-3xl font-bold">{scans.length}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Total Findings</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-3xl font-bold">{findings.length}</p>
          </CardContent>
        </Card>
      </div>

      {/* Severity breakdown + Fix rate */}
      <div className="grid gap-4 md:grid-cols-3">
        <Card className="md:col-span-2">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-medium">Findings by Severity</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex gap-6">
              {severities.map((sev) => (
                <div key={sev} className="flex flex-col items-center gap-1">
                  <SeverityBadge severity={sev} />
                  <span className="text-2xl font-bold">{bySev[sev]}</span>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-medium">Fix Rate</CardTitle>
          </CardHeader>
          <CardContent>
            {findings.length > 0 ? (
              <div className="flex flex-col items-center gap-2 py-2">
                <span className="text-4xl font-bold">{fixPct}%</span>
                <span className="text-sm text-muted-foreground">
                  {fixed} of {findings.length} fixed
                </span>
                <div className="w-full bg-muted rounded-full h-2.5 mt-1">
                  <div
                    className="bg-primary rounded-full h-2.5 transition-all"
                    style={{ width: `${fixPct}%` }}
                  />
                </div>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground py-4 text-center">
                No findings data yet.
              </p>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Trend chart */}
      {trendData.length > 0 && (
        <TrendChart
          data={trendData}
          title="Findings Over Time"
          height={260}
        />
      )}

      {/* Recent scans + Collections */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base font-medium">Recent Scans</CardTitle>
          </CardHeader>
          <CardContent>
            {recentScans.length === 0 ? (
              <p className="text-sm text-muted-foreground">No scans yet.</p>
            ) : (
              <div className="space-y-2">
                {recentScans.map((scan) => (
                  <Link
                    key={scan.id}
                    href={scan.status === "running" ? `/scans/${scan.id}/live` : `/scans/${scan.id}`}
                    className="flex items-center justify-between p-2 rounded-md border hover:bg-muted/50 transition-colors"
                  >
                    <div className="flex items-center gap-2">
                      <StatusBadge status={scan.status} />
                      <span className="text-sm font-mono">{scan.id.slice(0, 8)}</span>
                    </div>
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                      <span>{scan.finding_count} findings</span>
                      <span>{new Date(scan.created_at).toLocaleDateString()}</span>
                    </div>
                  </Link>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-base font-medium">Collections</CardTitle>
          </CardHeader>
          <CardContent>
            {cols.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No collections yet. <Link href="/collections" className="underline">Create one</Link>.
              </p>
            ) : (
              <div className="space-y-2">
                {cols.map((col) => (
                  <Link
                    key={col.id}
                    href={`/collections/${col.id}`}
                    className="flex items-center justify-between p-2 rounded-md border hover:bg-muted/50 transition-colors"
                  >
                    <span className="font-medium text-sm">{col.name}</span>
                    <span className="text-sm text-muted-foreground">
                      {col.repo_count ?? 0} repos
                    </span>
                  </Link>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
