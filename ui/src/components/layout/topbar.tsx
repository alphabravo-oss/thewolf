// Topbar — ported from astronomer/frontend/src/components/layout/topbar.tsx.
//
// Astronomer's layout is reproduced exactly: a sticky 14-unit header with a
// blurred translucent background, breadcrumbs on the left, the global search
// centred, then the ⌘K trigger, the three-way theme cycle, a notification bell
// with a count badge and dropdown, and the account menu.
//
// The bell's contents are Wolf's: running scans and recently failed scans stand
// in for Astronomer's firing alerts and Charlie findings.
import { Link, useRouterState, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  AlertCircle,
  Bell,
  ChevronRight,
  Command,
  Monitor,
  Moon,
  Sun,
} from "lucide-react";
import { api, isNotFound } from "@/lib/api";
import type { InboxNotification, Scan } from "@/lib/types";
import { useTheme } from "@/lib/theme";
import { useUIStore } from "@/lib/store-ui";
import { StatusBadge } from "@/components/ui/status-badge";
import { GlobalSearch } from "@/components/layout/global-search";
import { UserMenu } from "@/components/user-menu";
import { formatRelativeTime } from "@/lib/utils";

// --- Breadcrumb generation (Astronomer's `routeLabels` + `generateBreadcrumbs`,
// with Wolf's route vocabulary) ---

const routeLabels: Record<string, string> = {
  collections: "Collections",
  repos: "Repositories",
  scans: "Scans",
  findings: "Findings",
  vulnerabilities: "Vulnerabilities",
  fixes: "Fixes",
  agents: "Agents",
  audit: "Audit Log",
  settings: "Settings",
  account: "Account",
  live: "Live",
  enterprise: "Enterprise",
  identity: "Identity",
  tenancy: "Tenancy",
  catalogs: "Plugin catalogs",
  compliance: "Compliance",
  reports: "Reports",
  support: "Support",
  diagnostics: "Diagnostics",
  integrations: "Integrations",
  siem: "SIEM",
  ticketing: "Ticketing",
  residency: "Residency",
  packaging: "Packaging",
  verifications: "Verification",
  "attack-paths": "Attack paths",
  "custom-roles": "Custom roles",
  "group-mappings": "Group mappings",
  "customer-keys": "Customer keys",
};

function generateBreadcrumbs(pathname: string): { label: string; href: string }[] {
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length === 0) return [{ label: "Home", href: "/" }];

  const crumbs: { label: string; href: string }[] = [{ label: "Home", href: "/" }];
  let path = "";
  for (const segment of segments) {
    path += `/${segment}`;
    // Opaque ids (UUIDs and the like) are truncated rather than title-cased.
    const label = /^[0-9a-f-]{20,}$/i.test(segment)
      ? segment.slice(0, 8)
      : (routeLabels[segment] ??
        decodeURIComponent(segment).charAt(0).toUpperCase() + decodeURIComponent(segment).slice(1));
    crumbs.push({ label, href: path });
  }
  return crumbs;
}

function scanIdFromHref(href?: string): string | undefined {
  if (!href) return undefined;
  const match = href.match(/\/scans\/([^/?#]+)/);
  return match?.[1];
}

function goNotification(
  navigate: ReturnType<typeof useNavigate>,
  href?: string,
) {
  const scanId = scanIdFromHref(href);
  if (scanId) {
    void navigate({ to: "/scans/$scanId", params: { scanId } });
    return;
  }
  if (href?.startsWith("/")) {
    void navigate({ to: href });
  }
}

export function Topbar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const openPalette = useUIStore((s) => s.openPalette);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const notificationRef = useRef<HTMLDivElement>(null);

  // Running scans drive both the live activity pill and the bell. Polls every
  // 5s; cheap, and clearly signals when Wolf is busy.
  const { data: running } = useQuery({
    queryKey: ["scans", "running"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?status=running&limit=10");
      return r.data ?? [];
    },
    refetchInterval: 5000,
    staleTime: 0,
  });

  // Recently failed scans are the closest Wolf analogue to Astronomer's firing
  // alerts — the thing an operator needs to notice without hunting for it.
  const { data: failed } = useQuery({
    queryKey: ["scans", "failed"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?status=failed&limit=5");
      return r.data ?? [];
    },
    refetchInterval: 60_000,
  });

  const { data: inbox } = useQuery({
    queryKey: ["notifications"],
    queryFn: async () => {
      try {
        const r = await api.get<
          { items: InboxNotification[] } | InboxNotification[]
        >("/notifications");
        if (Array.isArray(r.data)) return r.data;
        return r.data?.items ?? [];
      } catch (e) {
        if (isNotFound(e)) return [];
        throw e;
      }
    },
    staleTime: 15_000,
  });

  const { theme, resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  const breadcrumbs = useMemo(() => generateBreadcrumbs(pathname), [pathname]);

  const runningScans = running ?? [];
  const failedScans = failed ?? [];
  const seenScanIds = new Set(
    [...runningScans, ...failedScans].map((scan) => scan.id),
  );
  const extraNotes = (inbox ?? []).filter((item) => {
    const id = scanIdFromHref(item.href);
    return !id || !seenScanIds.has(id);
  });
  const notificationCount =
    runningScans.length + failedScans.length + extraNotes.length;

  // Close the notification dropdown on outside click.
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (notificationRef.current && !notificationRef.current.contains(e.target as Node)) {
        setNotificationOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const cycleTheme = () => {
    if (theme === "light") setTheme("dark");
    else if (theme === "dark") setTheme("system");
    else setTheme("light");
  };

  const visibleTheme = mounted ? theme : "dark";
  const ThemeIcon = !mounted
    ? Moon
    : visibleTheme === "dark"
      ? Moon
      : visibleTheme === "light"
        ? Sun
        : Monitor;

  return (
    <header className="sticky top-0 z-30 flex items-center justify-between h-14 px-6 border-b border-border bg-background/80 backdrop-blur-lg">
      {/* Left: breadcrumbs. Padded on mobile to clear the hamburger. */}
      <nav aria-label="Breadcrumb" className="flex items-center gap-1.5 text-sm min-w-0 pl-10 md:pl-0">
        {breadcrumbs.map((crumb, i) => (
          <div key={crumb.href} className="flex items-center gap-1.5 min-w-0">
            {i > 0 && <ChevronRight className="h-3.5 w-3.5 text-muted-foreground flex-shrink-0" />}
            {i === breadcrumbs.length - 1 ? (
              <span aria-current="page" className="text-foreground font-medium truncate">
                {crumb.label}
              </span>
            ) : (
              <button
                type="button"
                onClick={() => navigate({ to: crumb.href })}
                className="text-muted-foreground hover:text-foreground transition-colors truncate"
              >
                {crumb.label}
              </button>
            )}
          </div>
        ))}
      </nav>

      {/* Centre: global search */}
      <div className="hidden md:flex flex-1 justify-center px-6">
        <GlobalSearch />
      </div>

      {/* Right: actions */}
      <div className="flex items-center gap-2">
        {runningScans.length > 0 && (
          <Link
            to="/scans"
            className="hidden sm:flex items-center gap-1.5 h-8 px-2.5 rounded-md border border-status-info/30 bg-status-info/10 text-xs font-medium text-status-info hover:bg-status-info/15 transition-colors"
          >
            <Activity className="h-3.5 w-3.5 animate-pulse" />
            {runningScans.length} running
          </Link>
        )}

        {/* Command palette trigger */}
        <button
          type="button"
          onClick={openPalette}
          aria-label="Open command palette"
          className="inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md border border-border text-xs
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <Command className="h-3.5 w-3.5" />
          <kbd className="font-mono text-[10px]">K</kbd>
        </button>

        <button
          type="button"
          onClick={cycleTheme}
          className="relative inline-flex items-center justify-center h-8 w-8 rounded-md
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          title={`Theme: ${visibleTheme}${theme === "system" ? ` (${resolvedTheme})` : ""}`}
          aria-label={`Theme: ${visibleTheme}. Click to change.`}
        >
          <ThemeIcon className="h-4 w-4" />
        </button>

        {/* Notifications */}
        <div ref={notificationRef} className="relative">
          <button
            type="button"
            onClick={() => setNotificationOpen(!notificationOpen)}
            aria-label={`Notifications${notificationCount ? ` (${notificationCount})` : ""}`}
            aria-expanded={notificationOpen}
            className="relative inline-flex items-center justify-center h-8 w-8 rounded-md
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <Bell className="h-4 w-4" />
            {notificationCount > 0 && (
              <span className="absolute top-0.5 right-0.5 flex items-center justify-center h-4 min-w-[16px] px-1 rounded-full bg-status-error text-[10px] font-bold text-white">
                {notificationCount > 99 ? "99+" : notificationCount}
              </span>
            )}
          </button>

          {notificationOpen && (
            <div className="absolute right-0 top-full mt-1 w-80 rounded-lg border border-border bg-popover shadow-xl z-50 overflow-hidden">
              <div className="flex items-center justify-between px-4 py-3 border-b border-border">
                <h4 className="text-sm font-medium text-foreground">Activity</h4>
                {failedScans.length > 0 && (
                  <span className="text-xs px-2 py-0.5 rounded-full bg-status-error/10 text-status-error font-medium">
                    {failedScans.length} failed
                  </span>
                )}
              </div>

              <div className="max-h-80 overflow-y-auto">
                {notificationCount === 0 ? (
                  <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                    Nothing needs you
                  </div>
                ) : (
                  <>
                    {runningScans.map((scan) => (
                      <button
                        key={`running:${scan.id}`}
                        type="button"
                        onClick={() => {
                          navigate({ to: "/scans/$scanId", params: { scanId: scan.id } });
                          setNotificationOpen(false);
                        }}
                        className="flex w-full items-start gap-3 border-b border-border px-4 py-3 text-left hover:bg-accent/50 transition-colors"
                      >
                        <Activity className="mt-0.5 h-4 w-4 shrink-0 text-status-info animate-pulse" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium text-foreground">
                            Scan in progress
                          </span>
                          <span className="mt-0.5 flex items-center gap-2">
                            <StatusBadge status="running" size="sm" />
                            <span className="text-2xs text-muted-foreground">
                              {scan.started_at ? formatRelativeTime(scan.started_at) : ""}
                            </span>
                          </span>
                        </span>
                      </button>
                    ))}
                    {failedScans.map((scan) => (
                      <button
                        key={`failed:${scan.id}`}
                        type="button"
                        onClick={() => {
                          navigate({ to: "/scans/$scanId", params: { scanId: scan.id } });
                          setNotificationOpen(false);
                        }}
                        className="flex w-full items-start gap-3 border-b border-border last:border-0 px-4 py-3 text-left hover:bg-accent/50 transition-colors"
                      >
                        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-status-error" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium text-foreground">
                            Scan failed
                          </span>
                          <span className="mt-0.5 flex items-center gap-2">
                            <StatusBadge status="failed" size="sm" />
                            <span className="text-2xs text-muted-foreground">
                              {scan.created_at ? formatRelativeTime(scan.created_at) : ""}
                            </span>
                          </span>
                        </span>
                      </button>
                    ))}
                    {extraNotes.map((item, i) => (
                      <button
                        key={`inbox:${item.href ?? item.title}:${item.at ?? i}`}
                        type="button"
                        onClick={() => {
                          goNotification(navigate, item.href);
                          setNotificationOpen(false);
                        }}
                        className="flex w-full items-start gap-3 border-b border-border last:border-0 px-4 py-3 text-left hover:bg-accent/50 transition-colors"
                      >
                        <Bell className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium text-foreground">
                            {item.title}
                          </span>
                          {item.at ? (
                            <span className="mt-0.5 block text-2xs text-muted-foreground">
                              {formatRelativeTime(item.at)}
                            </span>
                          ) : null}
                        </span>
                      </button>
                    ))}
                  </>
                )}
              </div>

              <div className="px-4 py-2 border-t border-border">
                <button
                  type="button"
                  onClick={() => {
                    navigate({ to: "/scans" });
                    setNotificationOpen(false);
                  }}
                  className="w-full text-center text-xs text-muted-foreground hover:text-foreground transition-colors py-1"
                >
                  View all scans
                </button>
              </div>
            </div>
          )}
        </div>

        <UserMenu />
      </div>
    </header>
  );
}
