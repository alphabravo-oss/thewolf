// Sidebar nav. Astronomer-style: dense, grouped, with subtle active-state
// glow. Responsive: collapses to a hamburger-triggered drawer on screens
// narrower than `md` (768 px).
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import {
  LayoutDashboardIcon,
  PackageIcon,
  GitForkIcon,
  BugIcon,
  SettingsIcon,
  GaugeIcon,
  LogOutIcon,
  MenuIcon,
  XIcon,
  WrenchIcon,
  RepeatIcon,
  ContainerIcon,
  ScrollTextIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { WolfLogo } from "./wolf-logo";
import { ThemeToggle } from "./theme-toggle";
import { api, clearToken } from "@/lib/api";
import { cn } from "@/lib/utils";

type NavItem = {
  label: string;
  to: string;
  icon: typeof PackageIcon;
};

const primary: NavItem[] = [
  { label: "Dashboard", to: "/", icon: LayoutDashboardIcon },
  { label: "Collections", to: "/collections", icon: PackageIcon },
  { label: "Repos", to: "/repos", icon: GitForkIcon },
  { label: "Scans", to: "/scans", icon: GaugeIcon },
  { label: "Findings", to: "/findings", icon: BugIcon },
  { label: "Fixes", to: "/fixes", icon: WrenchIcon },
  { label: "Loops", to: "/loops", icon: RepeatIcon },
  { label: "Scanners", to: "/scanners", icon: ContainerIcon },
  { label: "Audit", to: "/audit", icon: ScrollTextIcon },
];

const secondary: NavItem[] = [
  { label: "Settings", to: "/settings", icon: SettingsIcon },
];

export function Sidebar() {
  const pathname = useRouterState({
    select: (s) => s.location.pathname,
  });
  const navigate = useNavigate();

  // Mobile drawer. Auto-closes on route change.
  const [mobileOpen, setMobileOpen] = useState(false);
  useEffect(() => {
    setMobileOpen(false);
  }, [pathname]);

  async function handleLogout() {
    // Best-effort server-side logout (clears server session if any);
    // either way we drop the local cookie and redirect to /login.
    try {
      await api.post("/auth/logout");
    } catch {
      // server returned 401 or 500 — we still want to clear locally.
    }
    clearToken();
    navigate({ to: "/login" });
  }

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
          {secondary.map((item) => (
            <NavLink key={item.to} item={item} active={isActive(item.to)} />
          ))}
          <ThemeToggle />
          <button
            type="button"
            onClick={handleLogout}
            className="nav-item w-full text-left"
            aria-label="Sign out"
          >
            <LogOutIcon />
            <span className="truncate">Sign out</span>
          </button>
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
