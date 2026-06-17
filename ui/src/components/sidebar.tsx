// Sidebar nav. Astronomer-style: dense, grouped, with subtle active-state
// glow. Responsive: collapses to a hamburger-triggered drawer on screens
// narrower than `md` (768 px).
import { Link, useRouterState } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  LayoutDashboardIcon,
  PackageIcon,
  BugIcon,
  SettingsIcon,
  GaugeIcon,
  MenuIcon,
  XIcon,
  WrenchIcon,
  RepeatIcon,
  ContainerIcon,
  ScrollTextIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { WolfLogo } from "./wolf-logo";
import { api } from "@/lib/api";
import { useFlag } from "@/lib/flags";
import { useIsAdmin } from "@/lib/me";
import { cn } from "@/lib/utils";

type NavItem = {
  label: string;
  to: string;
  icon: typeof PackageIcon;
};

// Navigation follows the folder model: you browse Collections -> a collection
// -> a repo -> a scan -> its findings, so there is no top-level Repos item.
// The cross-repo global lists (Scans, Findings) only make sense in a fleet
// view, and the agentic surface (Fixes, Loops) only when autonomous fixing is
// on, so both groups are gated by their feature flag.
function usePrimaryNav(): NavItem[] {
  const autofix = useFlag("autofix_enabled");
  const isAdmin = useIsAdmin();
  return [
    { label: "Dashboard", to: "/", icon: LayoutDashboardIcon },
    { label: "Collections", to: "/collections", icon: PackageIcon },
    // The cross-repo fleet lists are the admin view; regular users browse
    // their own work via Collections.
    ...(isAdmin
      ? [
          { label: "Scans", to: "/scans", icon: GaugeIcon },
          { label: "Findings", to: "/findings", icon: BugIcon },
        ]
      : []),
    ...(autofix.enabled
      ? [
          { label: "Fixes", to: "/fixes", icon: WrenchIcon },
          { label: "Loops", to: "/loops", icon: RepeatIcon },
        ]
      : []),
    // Scanner-image management and the audit log are admin-only.
    ...(isAdmin
      ? [
          { label: "Scanners", to: "/scanners", icon: ContainerIcon },
          { label: "Audit", to: "/audit", icon: ScrollTextIcon },
        ]
      : []),
  ];
}

const secondary: NavItem[] = [
  { label: "Settings", to: "/settings", icon: SettingsIcon },
];

export function Sidebar() {
  const pathname = useRouterState({
    select: (s) => s.location.pathname,
  });
  const primary = usePrimaryNav();
  // Settings is visible to everyone: regular users manage their own secrets +
  // SSH nodes there, while the admin-only tabs (general, users, scanners) are
  // hidden inside the page for non-admins.
  const footerNav = secondary;

  // App version for the footer (GET /version → { version }).
  const versionQ = useQuery({
    queryKey: ["version"],
    queryFn: async () => {
      const r = await api.get<{ version: string }>("/version");
      return r.data?.version ?? "";
    },
    staleTime: 5 * 60 * 1000,
  });

  // Mobile drawer. Auto-closes on route change.
  const [mobileOpen, setMobileOpen] = useState(false);
  useEffect(() => {
    setMobileOpen(false);
  }, [pathname]);

  function isActive(to: string) {
    if (to === "/") return pathname === "/";
    return pathname.startsWith(to);
  }

  return (
    <>
      {/* Mobile hamburger — fixed top-left, only visible <md. */}
      <button
        type="button"
        className={cn(
          "md:hidden fixed top-3 left-3 z-30 size-9 grid place-items-center rounded-md",
          "bg-card border border-border shadow-md",
        )}
        aria-label="Open menu"
        onClick={() => setMobileOpen(true)}
      >
        <MenuIcon className="size-4" />
      </button>

      {/* Backdrop for mobile drawer. */}
      {mobileOpen && (
        <div
          className="md:hidden fixed inset-0 z-40 bg-background/80 backdrop-blur-sm animate-fade-in"
          onClick={() => setMobileOpen(false)}
        />
      )}

      <aside
        className={cn(
          "z-50 w-60 shrink-0 bg-sidebar border-r border-sidebar-border flex flex-col",
          // Desktop: always visible inline.
          "md:static md:flex md:translate-x-0",
          // Mobile: off-canvas drawer, slides in.
          "fixed top-0 left-0 h-screen transition-transform duration-200 ease-out",
          mobileOpen ? "translate-x-0" : "-translate-x-full md:translate-x-0",
        )}
      >
        <div className="h-16 flex items-center justify-between gap-2 px-4 border-b border-sidebar-border">
          <Link to="/" className="flex items-center gap-2.5">
            <WolfLogo className="size-9" />
            <div className="flex flex-col leading-none gap-1">
              <span className="text-[0.9rem] font-semibold tracking-tight text-foreground">
                The Wolf
              </span>
              <span className="text-3xs uppercase tracking-[0.16em] text-faint">
                by AlphaBravo
              </span>
            </div>
          </Link>
          <button
            type="button"
            onClick={() => setMobileOpen(false)}
            className="md:hidden size-7 grid place-items-center rounded-md hover:bg-sidebar-accent"
            aria-label="Close menu"
          >
            <XIcon className="size-4" />
          </button>
        </div>

        <nav className="flex-1 px-3 py-2 space-y-0.5 overflow-y-auto">
          <div className="nav-section">Platform</div>
          {primary.map((item) => (
            <NavLink key={item.to} item={item} active={isActive(item.to)} />
          ))}
        </nav>

        <div className="px-3 py-3 border-t border-sidebar-border space-y-0.5">
          {footerNav.map((item) => (
            <NavLink key={item.to} item={item} active={isActive(item.to)} />
          ))}

          <div className="pt-2 mt-1 border-t border-sidebar-border flex items-center justify-between px-2 text-3xs text-faint">
            <span className="tabular-nums">
              {versionQ.data ? `v${versionQ.data}` : ""}
            </span>
            <span>
              built by{" "}
              <a
                href="https://alphabravo.io"
                target="_blank"
                rel="noopener noreferrer"
                className="font-medium text-muted-foreground hover:text-foreground transition-colors"
              >
                AlphaBravo
              </a>
            </span>
          </div>
        </div>
      </aside>
    </>
  );
}

function NavLink({ item, active }: { item: NavItem; active: boolean }) {
  const Icon = item.icon;
  return (
    <Link to={item.to} className={cn("nav-item", active && "active")}>
      <Icon />
      <span className="truncate">{item.label}</span>
    </Link>
  );
}
