import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

export interface RuntimeCapabilities {
  scan_runtime: "docker" | "kubernetes" | string;
  queue_execution: boolean;
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
