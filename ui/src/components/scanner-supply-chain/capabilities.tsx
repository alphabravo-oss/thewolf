import { createContext, type ReactNode, useContext, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  scannerSupplyChainApi,
  type ScannerReleaseCapabilities,
} from "@/lib/scanner-supply-chain";
import {
  NO_SCANNER_SUPPLY_CHAIN_PERMISSIONS,
  scannerSupplyChainPermissions,
  type ScannerSupplyChainPermissions,
  useMe,
} from "@/lib/me";

const SAFE_DEFAULT: ScannerReleaseCapabilities = {
  mode: "read_only",
  read: true,
  candidates: false,
  canary: false,
  stable_control: false,
};

type CapabilityState = {
  capabilities: ScannerReleaseCapabilities;
  permissions: ScannerSupplyChainPermissions;
  loading: boolean;
  error: Error | null;
};

const CapabilityContext = createContext<CapabilityState>({
  capabilities: SAFE_DEFAULT,
  permissions: NO_SCANNER_SUPPLY_CHAIN_PERMISSIONS,
  loading: true,
  error: null,
});

export function ScannerReleaseCapabilitiesProvider({
  children,
}: {
  children: ReactNode;
}) {
  const query = useQuery({
    queryKey: ["scanner-supply-chain", "overview"],
    queryFn: scannerSupplyChainApi.overview,
    staleTime: 20_000,
  });
  const me = useMe();
  const permissions = useMemo(
    () => scannerSupplyChainPermissions(me.data),
    [me.data],
  );
  const value = useMemo<CapabilityState>(() => {
    return {
      capabilities: query.data?.capabilities ?? SAFE_DEFAULT,
      permissions,
      loading: query.isPending || me.isPending,
      error:
        query.error instanceof Error
          ? query.error
          : me.error instanceof Error
            ? me.error
            : null,
    };
  }, [
    me.error,
    me.isPending,
    permissions,
    query.data?.capabilities,
    query.error,
    query.isPending,
  ]);
  return (
    <CapabilityContext.Provider value={value}>
      {children}
    </CapabilityContext.Provider>
  );
}

// ScannerReleaseCapabilitiesBoundary supplies an already-resolved capability
// snapshot for embedded surfaces, stories, and deterministic component tests.
export function ScannerReleaseCapabilitiesBoundary({
  capabilities,
  permissions = {
    read: true,
    operate: true,
    approve: true,
    manageRegistries: true,
    administer: true,
    systemAdmin: true,
  },
  children,
}: {
  capabilities: ScannerReleaseCapabilities;
  permissions?: ScannerSupplyChainPermissions;
  children: ReactNode;
}) {
  const value = useMemo<CapabilityState>(
    () => ({
      capabilities,
      permissions,
      loading: false,
      error: null,
    }),
    [capabilities, permissions],
  );
  return (
    <CapabilityContext.Provider value={value}>
      {children}
    </CapabilityContext.Provider>
  );
}

export function useScannerReleaseCapabilities() {
  return useContext(CapabilityContext);
}

export function CapabilityBanner() {
  const { capabilities, permissions, loading, error } =
    useScannerReleaseCapabilities();
  if (loading) {
    return (
      <div
        className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground"
        role="status"
        aria-live="polite"
      >
        Loading scanner release-management capabilities…
      </div>
    );
  }
  if (error) {
    return (
      <div
        role="alert"
        className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive-text"
      >
        Scanner release-management state is unavailable. Administrative actions
        remain disabled until capability checks recover.
      </div>
    );
  }
  if (!permissions.read) {
    return (
      <div
        role="alert"
        className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive-text"
      >
        Your account does not have scanner supply-chain access. Ask an
        administrator to assign an approved scanner persona.
      </div>
    );
  }
  if (
    !permissions.operate &&
    !permissions.approve &&
    !permissions.manageRegistries &&
    !permissions.administer
  ) {
    return (
      <div
        role="status"
        aria-live="polite"
        className="rounded-md border border-blue-500/40 bg-blue-500/10 px-3 py-2 text-sm text-foreground"
      >
        Read-only scanner access — inventory, operational evidence, and audit
        history are available. Mutations require an assigned operational persona
        and remain independently protected by server scopes.
      </div>
    );
  }
  if (capabilities.mode === "stable_control") return null;
  const description =
    capabilities.mode === "read_only"
      ? "Observe-only mode: release data is available, but candidate and rollout changes are disabled."
      : capabilities.mode === "candidate"
        ? "Candidate mode: discovery, policy, registry, and approval operations are enabled; publication and rollout controls remain disabled."
        : capabilities.mode === "canary"
          ? "Canary mode: canary publication and rollout controls are enabled; stable deprecation and revocation remain disabled."
          : "Scanner release management is disabled.";
  return (
    <div
      role="status"
      aria-live="polite"
      className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-foreground"
    >
      <span className="font-medium">
        {capabilities.mode.replaceAll("_", " ")}
      </span>
      {" — "}
      {description}
    </div>
  );
}
