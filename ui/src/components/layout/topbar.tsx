import { Link, useRouterState, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
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
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuLabel } from "@/components/ui/dropdown-menu";
import { Popover, PopoverTrigger, PopoverContent } from "@/components/ui/popover";
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
  agents: "Fix agents",
  coverage: "Scanner coverage",
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

// Detail pages of these resources render their own PageHeader breadcrumbs
// (with the resource name), so the topbar trail stays out of their way.
const pageOwnedBreadcrumbs = new Set(["repos", "collections", "scans", "findings", "vulnerabilities", "agents"]);

function generateBreadcrumbs(pathname: string): { label: string; href: string }[] {
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length === 0) return [{ label: "Home", href: "/" }];

  const crumbs: { label: string; href: string }[] = [{ label: "Home", href: "/" }];
  let path = "";
  for (const segment of segments) {
    path += `/${segment}`;
    // Never expose an opaque identifier as a label.
    const label = /^[0-9a-f-]{20,}$/i.test(segment) ? "Details"
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
    // `href` lets the router parse a query string (e.g. ?tab=scanners).
    void navigate({ href });
  }
}

export function Topbar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const openPalette = useUIStore((s) => s.openPalette);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const segments = pathname.split("/").filter(Boolean);
  const pageOwnsBreadcrumbs = segments.length >= 2 && pageOwnedBreadcrumbs.has(segments[0]);

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

  const { theme, setTheme } = useTheme();
  const breadcrumbs = useMemo(() => (pageOwnsBreadcrumbs ? [] : generateBreadcrumbs(pathname)), [pathname, pageOwnsBreadcrumbs]);

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

  const ThemeIcon = theme === "dark" ? Moon : theme === "light" ? Sun : Monitor;

  return (
    <header className="sticky top-0 z-30 flex items-center justify-between h-14 shrink-0 gap-2 px-3 sm:px-6 border-b border-border bg-background">
      {/* Left: breadcrumbs. Padded on mobile to clear the hamburger. */}
      <nav aria-label="Breadcrumb" className="flex items-center gap-1.5 font-narrow text-sm min-w-0 pl-10 md:pl-0">
        {breadcrumbs.map((crumb, i) => (
          <div key={crumb.href} className={i === breadcrumbs.length - 1 ? "flex items-center gap-1.5 min-w-0" : "hidden sm:flex items-center gap-1.5 min-w-0"}>
            {i > 0 && <ChevronRight className="hidden sm:block h-3.5 w-3.5 text-muted-foreground flex-shrink-0" />}
            {i === breadcrumbs.length - 1 || crumb.href === "/enterprise" ? (
              <span aria-current={i === breadcrumbs.length - 1 ? "page" : undefined} className="text-foreground font-medium truncate">
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
      <div className="hidden lg:flex flex-1 justify-center px-4">
        <GlobalSearch />
      </div>

      {/* Right: actions */}
      <div className="flex shrink-0 items-center gap-1 sm:gap-2">
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
          onClick={() => openPalette()}
          aria-label="Search and commands"
          aria-keyshortcuts="Meta+K Control+K"
          title="Search and commands (⌘K / Ctrl+K)"
          className="inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md border border-border text-xs
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <Command className="h-3.5 w-3.5" />
          <kbd className="font-mono text-[10px]">K</kbd>
        </button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" aria-label={`Theme: ${theme}`} title="Choose theme"
              className="inline-flex size-9 items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent">
              <ThemeIcon className="size-4" aria-hidden="true" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuLabel>Appearance</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as "light" | "dark" | "system")}>
              <DropdownMenuRadioItem value="light"><Sun className="mr-2 size-4" />Light</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="dark"><Moon className="mr-2 size-4" />Dark</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="system"><Monitor className="mr-2 size-4" />System</DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>

        {/* Notifications */}
        <Popover open={notificationOpen} onOpenChange={setNotificationOpen}>
          <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={`Notifications${notificationCount ? ` (${notificationCount})` : ""}`}
            aria-expanded={notificationOpen}
            className="relative inline-flex items-center justify-center h-8 w-8 rounded-md
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <Bell className="h-4 w-4" />
            {notificationCount > 0 && (
              <span className="absolute top-0.5 right-0.5 flex items-center justify-center h-4 min-w-[16px] px-1 rounded-full bg-status-error text-[10px] font-bold text-destructive-foreground">
                {notificationCount > 99 ? "99+" : notificationCount}
              </span>
            )}
          </button>

          </PopoverTrigger>
            <PopoverContent align="end" aria-label="Notifications" className="w-80 max-w-[calc(100vw-2rem)] p-0 overflow-hidden">
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
            </PopoverContent>
        </Popover>

        <UserMenu />
      </div>
    </header>
  );
}
