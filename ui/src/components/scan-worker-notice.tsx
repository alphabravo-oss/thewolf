import { useQuery } from "@tanstack/react-query";
import { runtimeCapabilitiesQuery } from "@/lib/runtime-capabilities";

export function ScanWorkerNotice() {
  const health = useQuery({ ...runtimeCapabilitiesQuery, staleTime: 0, refetchInterval: 10_000 });
  const worker = health.data?.worker_readiness;
  if (health.isPending || (!health.isError && health.data?.queue_execution === false)) return null;
  if (!health.isError && worker?.status === "ready") return null;
  const message = health.isError || !worker
    ? "Scan worker health is unavailable. Queued scans may be waiting for a worker."
    : worker.status === "busy"
      ? "All active scan workers are busy. Queued scans will wait for capacity."
      : `No active ${health.data?.scan_runtime ?? "compatible"} scan worker has checked in. Scans will stay queued until an administrator starts a worker.`;
  return <div role="status" className="rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-sm text-status-warning">
    <p>{message}</p>
    {worker?.last_active_heartbeat && <p className="mt-1 text-xs">Last active heartbeat: <time dateTime={worker.last_active_heartbeat}>{new Date(worker.last_active_heartbeat).toLocaleString()}</time></p>}
    <button type="button" className="mt-1 underline" onClick={() => void health.refetch()}>Refresh worker status</button>
  </div>;
}
