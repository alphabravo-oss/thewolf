// Dashboard. KPIs at the top, recent scans + collections below.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  PackageIcon,
  GaugeIcon,
  BugIcon,
  ActivityIcon,
} from "lucide-react";
import { api } from "@/lib/api";
import type { Collection, Scan } from "@/lib/types";
import { parseToolList } from "@/lib/types";
import { CardSkeleton, ListSkeleton } from "@/components/skeleton";
import { EmptyState } from "@/components/empty-state";
import { ScanStatusPill } from "@/components/scan-status-pill";

export const Route = createFileRoute("/_authed/")({
  component: DashboardPage,
});

function DashboardPage() {
  const collections = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      const r = await api.get<Collection[]>("/collections");
      return r.data ?? [];
    },
  });
  const scans = useQuery({
    queryKey: ["scans", "recent"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?limit=10");
      return r.data ?? [];
    },
    refetchInterval: 15_000,
  });

  const running = scans.data?.filter((s) => s.status === "running").length ?? 0;
  const findings =
    scans.data?.reduce((sum, s) => sum + (s.finding_count ?? 0), 0) ?? 0;

  return (
    <div className="p-6 space-y-6 max-w-7xl">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">
          <span className="text-gradient">Dashboard</span>
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Snapshot of your scans, collections, and findings.
        </p>
      </header>

      <section className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <KpiCard
          icon={PackageIcon}
          label="Collections"
          value={collections.data?.length ?? "—"}
          loading={collections.isLoading}
        />
        <KpiCard
          icon={GaugeIcon}
          label="Recent scans"
          value={scans.data?.length ?? "—"}
          loading={scans.isLoading}
        />
        <KpiCard
          icon={BugIcon}
          label="Findings (recent)"
          value={findings || "—"}
          loading={scans.isLoading}
        />
        <KpiCard
          icon={ActivityIcon}
          label="Running"
          value={running || "—"}
          loading={scans.isLoading}
          glow={running > 0 ? "info" : undefined}
        />
      </section>

      <section className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-base font-semibold">Recent scans</h2>
            <Link
              to="/scans"
              className="text-xs text-muted-foreground hover:text-foreground"
            >
              View all →
            </Link>
          </div>
          {scans.isLoading ? (
            <ListSkeleton rows={5} />
          ) : !scans.data || scans.data.length === 0 ? (
            <EmptyState
              icon={GaugeIcon}
              title="No scans yet"
              description="Kick off your first scan from the Scans page."
              cta={{ label: "New scan", to: "/scans" }}
            />
          ) : (
            <ul className="space-y-2">
              {scans.data.slice(0, 6).map((s) => (
                <li key={s.id}>
                  <Link
                    to="/scans/$scanId"
                    params={{ scanId: s.id }}
                    className="glass-card px-4 py-3 flex items-center gap-3 hover:bg-muted/30 transition"
                  >
                    <ScanStatusPill status={s.status} />
                    <div className="flex-1 min-w-0">
                      <div className="text-sm font-medium truncate">
                        {s.repo?.name ?? s.id.slice(0, 8)}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {s.branch} · {parseToolList(s.tools_completed).length}/
                        {parseToolList(s.tools_selected).length} tools
                      </div>
                    </div>
                    <div className="text-sm font-mono tabular-nums">
                      {s.finding_count}
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-base font-semibold">Collections</h2>
            <Link
              to="/collections"
              className="text-xs text-muted-foreground hover:text-foreground"
            >
              View all →
            </Link>
          </div>
          {collections.isLoading ? (
            <ListSkeleton rows={5} />
          ) : !collections.data || collections.data.length === 0 ? (
            <EmptyState
              icon={PackageIcon}
              title="No collections yet"
              description="Group your repos into collections to scan them as a batch."
              cta={{ label: "Create collection", to: "/collections" }}
            />
          ) : (
            <ul className="space-y-2">
              {collections.data.slice(0, 6).map((c) => (
                <li key={c.id}>
                  <Link
                    to="/collections/$collectionId"
                    params={{ collectionId: c.id }}
                    className="glass-card px-4 py-3 flex items-center gap-3 hover:bg-muted/30 transition"
                  >
                    <div className="size-8 rounded-md bg-muted/40 grid place-items-center">
                      <PackageIcon className="size-4 text-muted-foreground" />
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="text-sm font-medium truncate">
                        {c.name}
                      </div>
                      <div className="text-xs text-muted-foreground truncate">
                        {c.repo_count ?? 0} repos · {c.description || "—"}
                      </div>
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </div>
  );
}

function KpiCard({
  icon: Icon,
  label,
  value,
  loading,
  glow,
}: {
  icon: typeof PackageIcon;
  label: string;
  value: number | string;
  loading?: boolean;
  glow?: "success" | "warning" | "error" | "info" | "pending";
}) {
  return (
    <div
      className={`glass-card p-5 ${glow ? `glow-${glow}` : ""}`}
    >
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Icon className="size-4" />
        <span>{label}</span>
      </div>
      {loading ? (
        <CardSkeleton />
      ) : (
        <div className="text-3xl font-semibold mt-2 tabular-nums">{value}</div>
      )}
    </div>
  );
}
