"use client";

import { Suspense } from "react";
import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DataTable } from "@/components/data-table";
import { SeverityBadge } from "@/components/severity-badge";
import { StatusBadge } from "@/components/status-badge";
import { ScoreBar } from "@/components/score-bar";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import { TrendChart } from "@/components/trend-chart";
import api, { getToken } from "@/lib/api";
import { useFindingStore } from "@/lib/store";
import type { Finding, Severity, Category, FindingStatus, TrendEntry } from "@/lib/types";
import type { ColumnDef } from "@tanstack/react-table";

const columns: ColumnDef<Finding>[] = [
  {
    accessorKey: "severity",
    header: "Severity",
    cell: ({ row }) => <SeverityBadge severity={row.getValue("severity")} />,
  },
  {
    accessorKey: "title",
    header: "Title",
    cell: ({ row }) => (
      <Link
        href={`/findings/${row.original.id}`}
        className="font-medium hover:underline"
      >
        {row.getValue("title")}
      </Link>
    ),
  },
  {
    accessorKey: "category",
    header: "Category",
  },
  {
    accessorKey: "tool_name",
    header: "Tool",
  },
  {
    accessorKey: "file_path",
    header: "File",
    cell: ({ row }) => (
      <span className="text-xs font-mono truncate max-w-xs block">
        {row.getValue("file_path")}
      </span>
    ),
  },
  {
    accessorKey: "composite_score",
    header: "Score",
    cell: ({ row }) => (
      <ScoreBar score={row.getValue("composite_score")} className="w-24" />
    ),
  },
  {
    accessorKey: "status",
    header: "Status",
    cell: ({ row }) => <StatusBadge status={row.getValue("status")} />,
  },
];

const severityOptions: Severity[] = ["critical", "high", "medium", "low", "info"];
const categoryOptions: Category[] = [
  "sast", "sca", "secrets", "container", "quality", "docs", "license", "sbom",
];
const statusOptions: FindingStatus[] = ["open", "fixed", "wont_fix", "false_positive"];

export default function FindingsPage() {
  return (
    <Suspense fallback={<LoadingSpinner />}>
      <FindingsContent />
    </Suspense>
  );
}

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api";

function triggerDownload(path: string) {
  const token = getToken();
  const separator = path.includes("?") ? "&" : "?";
  const url = `${API_BASE}${path}${token ? `${separator}token=${encodeURIComponent(token)}` : ""}`;
  const a = document.createElement("a");
  a.href = url;
  a.download = "";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

function FindingsContent() {
  const searchParams = useSearchParams();
  const { filters, setFilters } = useFindingStore();
  const [trendRange, setTrendRange] = useState<"7d" | "30d" | "90d" | "all">("30d");

  // Sync collection_id from URL into filters
  useEffect(() => {
    const collectionId = searchParams.get("collection");
    if (collectionId) setFilters({ collection_id: collectionId });
  }, [searchParams, setFilters]);

  // Build query string from filters
  const filterQS = (() => {
    const params = new URLSearchParams();
    if (filters.severity?.length) params.set("severity", filters.severity.join(","));
    if (filters.category?.length) params.set("category", filters.category.join(","));
    if (filters.status?.length) params.set("status", filters.status.join(","));
    if (filters.collection_id) params.set("collection_id", filters.collection_id);
    return params.toString();
  })();

  const { data: findings = [], isLoading } = useQuery({
    queryKey: ["findings", filterQS],
    queryFn: () =>
      api.get<Finding[]>(`/findings${filterQS ? `?${filterQS}` : ""}`).then((r) => r.data ?? []),
    refetchInterval: 10_000,
  });

  const { data: trendData = [] } = useQuery({
    queryKey: ["findings-trends"],
    queryFn: () => api.get<TrendEntry[]>("/findings/trends").then((r) => r.data ?? []),
    refetchInterval: 30_000,
  });

  // Build filter query string for export URLs.
  const exportParams = new URLSearchParams();
  if (filters.severity?.length) exportParams.set("severity", filters.severity.join(","));
  if (filters.category?.length) exportParams.set("category", filters.category.join(","));
  if (filters.status?.length) exportParams.set("status", filters.status.join(","));
  if (filters.collection_id) exportParams.set("collection_id", filters.collection_id);
  const exportQS = exportParams.toString();

  if (isLoading) return <LoadingSpinner />;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold">Findings</h1>
          <p className="text-muted-foreground">
            Explore findings across all repositories
          </p>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline">Export</Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onClick={() =>
                triggerDownload(`/findings/export?format=csv${exportQS ? `&${exportQS}` : ""}`)
              }
            >
              Export as CSV
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() =>
                triggerDownload(`/findings/export?format=json${exportQS ? `&${exportQS}` : ""}`)
              }
            >
              Export as JSON
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() => triggerDownload("/findings/trends/export?format=csv")}
            >
              Export Trends CSV
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {trendData.length > 0 && (
        <TrendChart
          data={trendData}
          title="Finding Trends"
          timeRange={trendRange}
          onTimeRangeChange={setTrendRange}
        />
      )}

      <div className="flex gap-3 flex-wrap">
        <Select
          value={filters.severity?.[0] ?? "all"}
          onValueChange={(v) =>
            setFilters({ severity: v === "all" ? undefined : [v as Severity] })
          }
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue placeholder="Severity" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Severities</SelectItem>
            {severityOptions.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select
          value={filters.category?.[0] ?? "all"}
          onValueChange={(v) =>
            setFilters({ category: v === "all" ? undefined : [v as Category] })
          }
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue placeholder="Category" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Categories</SelectItem>
            {categoryOptions.map((c) => (
              <SelectItem key={c} value={c}>
                {c}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select
          value={filters.status?.[0] ?? "all"}
          onValueChange={(v) =>
            setFilters({
              status: v === "all" ? undefined : [v as FindingStatus],
            })
          }
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue placeholder="Status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Statuses</SelectItem>
            {statusOptions.map((s) => (
              <SelectItem key={s} value={s}>
                {s.replace(/_/g, " ")}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Button variant="ghost" size="sm" onClick={() => useFindingStore.getState().clearFilters()}>
          Clear Filters
        </Button>
      </div>

      {findings.length === 0 ? (
        <EmptyState
          title="No findings"
          description="Run a scan to discover findings across your repositories."
        />
      ) : (
        <DataTable
          columns={columns}
          data={findings}
          searchKey="title"
          searchPlaceholder="Search findings..."
        />
      )}
    </div>
  );
}
