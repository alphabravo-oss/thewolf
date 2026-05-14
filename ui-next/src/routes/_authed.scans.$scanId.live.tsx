// Live-scan page. Subscribes to the SSE event stream and renders the
// per-tool card grid. Once the scan completes, redirects to the regular
// scan detail page so the static results view takes over.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { ArrowLeftIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Scan } from "@/lib/types";
import { LiveScan } from "@/components/live-scan";
import { CardSkeleton } from "@/components/skeleton";
import { ScanStatusPill } from "@/components/scan-status-pill";

export const Route = createFileRoute("/_authed/scans/$scanId/live")({
  component: LiveScanPage,
});

function LiveScanPage() {
  const { scanId } = Route.useParams();
  const navigate = useNavigate();

  const scanQ = useQuery({
    queryKey: ["scan", scanId],
    queryFn: async () => {
      const r = await api.get<Scan>(`/scans/${scanId}`);
      return r.data;
    },
    refetchInterval: 3000,
  });

  // When the scan exits a running state, jump to the static detail view.
  useEffect(() => {
    const s = scanQ.data?.status;
    if (s === "completed" || s === "failed" || s === "cancelled") {
      const t = setTimeout(
        () => navigate({ to: "/scans/$scanId", params: { scanId } }),
        1500,
      );
      return () => clearTimeout(t);
    }
  }, [scanQ.data?.status, scanId, navigate]);

  if (scanQ.isLoading || !scanQ.data) {
    return (
      <div className="p-6 space-y-3">
        <CardSkeleton />
        <CardSkeleton />
      </div>
    );
  }
  const scan = scanQ.data;

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center gap-3">
        <Link
          to="/scans"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div className="flex-1 min-w-0">
          <h1 className="text-xl font-semibold truncate">
            {scan.repo?.name ?? scan.id.slice(0, 8)}{" "}
            <span className="text-muted-foreground font-normal">
              · {scan.branch}
            </span>
          </h1>
          <p className="text-xs text-muted-foreground">
            {scan.tools_completed.length}/{scan.tools_selected.length} tools
            completed
          </p>
        </div>
        <ScanStatusPill status={scan.status} />
      </div>

      <LiveScan
        scanId={scan.id}
        initialTools={scan.tools_selected}
        scanStatus={scan.status}
      />
    </div>
  );
}
