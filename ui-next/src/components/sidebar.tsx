// Sidebar nav. Astronomer-style: dense, grouped, with subtle active-state
// glow. Responsive: collapses to a hamburger-triggered drawer on screens
// narrower than `md` (768 px).
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import {
  LayoutDashboardIcon,
  PackageIcon,
  GitForkIcon,
  BugIcon,
  WrenchIcon,
  RepeatIcon,
  SettingsIcon,
  GaugeIcon,
  LogOutIcon,
  MenuIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { WolfLogo } from "./wolf-logo";
import { api, clearToken } from "@/lib/api";
import { cn } from "@/lib/cn";

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
          className="md:hidden fixed inset-0 z-40 bg-black/50 backdrop-blur-sm animate-fade-in"
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
        <div className="h-14 flex items-center justify-between gap-2 px-4 border-b border-sidebar-border">
          <Link to="/" className="flex items-center gap-2">
            <WolfLogo className="size-7" />
            <div className="font-semibold tracking-tight text-sidebar-foreground">
              The Wolf
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

        <nav className="flex-1 px-2 py-3 space-y-0.5 overflow-y-auto">
          {primary.map((item) => (
            <NavLink key={item.to} item={item} active={isActive(item.to)} />
          ))}
        </nav>

        <div className="px-2 py-3 border-t border-sidebar-border space-y-0.5">
          {secondary.map((item) => (
            <NavLink key={item.to} item={item} active={isActive(item.to)} />
          ))}
          <button
            type="button"
            onClick={handleLogout}
            className="nav-item w-full text-left"
            aria-label="Sign out"
          >
            <LogOutIcon className="size-4 shrink-0" />
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
      <Icon className="size-4 shrink-0" />
      <span className="truncate">{item.label}</span>
    </Link>
  );
}
