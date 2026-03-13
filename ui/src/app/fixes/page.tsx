"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/data-table";
import { StatusBadge } from "@/components/status-badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import api from "@/lib/api";
import type { Fix } from "@/lib/types";
import type { ColumnDef } from "@tanstack/react-table";

const columns: ColumnDef<Fix>[] = [
  {
    accessorKey: "status",
    header: "Status",
    cell: ({ row }) => <StatusBadge status={row.getValue("status")} />,
  },
  {
    accessorKey: "branch_name",
    header: "Branch",
    cell: ({ row }) => (
      <span className="font-mono text-sm">{row.getValue("branch_name")}</span>
    ),
  },
  {
    accessorKey: "findings_fixed",
    header: "Fixed",
  },
  {
    accessorKey: "findings_failed",
    header: "Failed",
  },
  {
    accessorKey: "findings_attempted",
    header: "Attempted",
  },
  {
    accessorKey: "created_at",
    header: "Created",
    cell: ({ row }) => new Date(row.getValue("created_at")).toLocaleString(),
  },
  {
    id: "actions",
    cell: ({ row }) => (
      <div className="flex gap-2">
        <Button size="sm" variant="ghost" asChild>
          <Link href={`/fixes/${row.original.id}`}>Details</Link>
        </Button>
        {row.original.status === "running" && (
          <Button size="sm" variant="ghost" asChild>
            <Link href={`/fixes/${row.original.id}/live`}>Live</Link>
          </Button>
        )}
      </div>
    ),
  },
];

export default function FixesPage() {
  const { data: fixes = [], isLoading } = useQuery({
    queryKey: ["fixes"],
    queryFn: () => api.get<Fix[]>("/fixes").then((r) => r.data ?? []),
    refetchInterval: 5_000,
  });

  if (isLoading) return <LoadingSpinner />;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold">Fixes</h1>
        <p className="text-muted-foreground">AI-powered fix history</p>
      </div>

      {fixes.length === 0 ? (
        <EmptyState
          title="No fixes yet"
          description="AI-generated fixes will appear here after scanning."
        />
      ) : (
        <DataTable columns={columns} data={fixes} />
      )}
    </div>
  );
}
