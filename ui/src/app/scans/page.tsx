"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/data-table";
import { StatusBadge } from "@/components/status-badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import api from "@/lib/api";
import type { Scan } from "@/lib/types";
import type { ColumnDef } from "@tanstack/react-table";

function formatDuration(start?: string, end?: string): string {
  if (!start) return "-";
  const s = new Date(start).getTime();
  const e = end ? new Date(end).getTime() : Date.now();
  const sec = Math.round((e - s) / 1000);
  if (sec < 60) return `${sec}s`;
  return `${Math.floor(sec / 60)}m ${sec % 60}s`;
}

const columns: ColumnDef<Scan>[] = [
  {
    accessorKey: "status",
    header: "Status",
    cell: ({ row }) => (
      <Link href={`/scans/${row.original.id}`} className="hover:opacity-80">
        <StatusBadge status={row.getValue("status")} />
      </Link>
    ),
  },
  {
    id: "repo",
    header: "Repo",
    cell: ({ row }) => (
      <span className="font-medium">{row.original.repo?.name ?? "\u2014"}</span>
    ),
  },
  {
    accessorKey: "branch",
    header: "Branch",
  },
  {
    accessorKey: "finding_count",
    header: "Findings",
  },
  {
    id: "duration",
    header: "Duration",
    cell: ({ row }) =>
      formatDuration(row.original.started_at, row.original.completed_at),
  },
  {
    accessorKey: "created_at",
    header: "Started",
    cell: ({ row }) => new Date(row.getValue("created_at")).toLocaleString(),
  },
  {
    id: "actions",
    cell: ({ row }) => (
      <div className="flex gap-2">
        <Button size="sm" variant="ghost" asChild>
          <Link href={`/scans/${row.original.id}`}>Results</Link>
        </Button>
        {row.original.status === "running" && (
          <Button size="sm" variant="ghost" asChild>
            <Link href={`/scans/${row.original.id}/live`}>Live</Link>
          </Button>
        )}
      </div>
    ),
  },
];

export default function ScansPage() {
  const { data: scans = [], isLoading } = useQuery({
    queryKey: ["scans"],
    queryFn: () => api.get<Scan[]>("/scans").then((r) => r.data ?? []),
    refetchInterval: 5_000,
  });

  const handleNewScan = async () => {
    try {
      await api.post("/scans");
    } catch {
      // error handled by api layer
    }
  };

  if (isLoading) return <LoadingSpinner />;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold">Scans</h1>
          <p className="text-muted-foreground">Scan history and results</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" asChild>
            <Link href="/scans/compare">Compare Scans</Link>
          </Button>
          <Button onClick={handleNewScan}>Start New Scan</Button>
        </div>
      </div>

      {scans.length === 0 ? (
        <EmptyState
          title="No scans yet"
          description="Start a scan to analyze your repositories for issues."
          action={<Button onClick={handleNewScan}>Start New Scan</Button>}
        />
      ) : (
        <DataTable columns={columns} data={scans} />
      )}
    </div>
  );
}
