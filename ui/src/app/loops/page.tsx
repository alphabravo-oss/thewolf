"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/data-table";
import { StatusBadge } from "@/components/status-badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import api from "@/lib/api";
import type { Loop } from "@/lib/types";
import type { ColumnDef } from "@tanstack/react-table";

const columns: ColumnDef<Loop>[] = [
  {
    accessorKey: "status",
    header: "Status",
    cell: ({ row }) => <StatusBadge status={row.getValue("status")} />,
  },
  {
    id: "progress",
    header: "Progress",
    cell: ({ row }) =>
      `${row.original.current_iteration}/${row.original.max_iterations}`,
  },
  {
    accessorKey: "total_findings_fixed",
    header: "Fixed",
  },
  {
    accessorKey: "total_findings_remaining",
    header: "Remaining",
  },
  {
    accessorKey: "rescan_strategy",
    header: "Strategy",
  },
  {
    accessorKey: "created_at",
    header: "Created",
    cell: ({ row }) => new Date(row.getValue("created_at")).toLocaleString(),
  },
  {
    id: "actions",
    cell: ({ row }) => (
      <Button size="sm" variant="ghost" asChild>
        <Link href={`/loops/${row.original.id}`}>Dashboard</Link>
      </Button>
    ),
  },
];

export default function LoopsPage() {
  const { data: loops = [], isLoading } = useQuery({
    queryKey: ["loops"],
    queryFn: () => api.get<Loop[]>("/loops").then((r) => r.data ?? []),
    refetchInterval: 5_000,
  });

  if (isLoading) return <LoadingSpinner />;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold">Loops</h1>
        <p className="text-muted-foreground">
          Scan → Fix → Re-scan automation cycles
        </p>
      </div>

      {loops.length === 0 ? (
        <EmptyState
          title="No loops yet"
          description="Create a scan-fix-rescan automation loop to iteratively improve code quality."
        />
      ) : (
        <DataTable columns={columns} data={loops} />
      )}
    </div>
  );
}
