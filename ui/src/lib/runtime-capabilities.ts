import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

export interface RuntimeCapabilities {
  scan_runtime: "docker" | "kubernetes" | string;
  queue_execution: boolean;
  worker_readiness?: {
    status: "ready" | "busy" | "unavailable";
    active_workers: number;
    available_slots: number;
    last_active_heartbeat?: string;
  } | null;
  docker_image_management: boolean;
  scanner_jobs: boolean;
  durable_events: boolean;
  tool_cancellation: boolean;
}

export const runtimeCapabilitiesQuery = {
  queryKey: ["runtime-capabilities"] as const,
  queryFn: async () =>
    (await api.get<RuntimeCapabilities>("/scanners/runtime-capabilities")).data,
  staleTime: 60_000,
};

export function useRuntimeCapabilities() {
  return useQuery(runtimeCapabilitiesQuery);
}
