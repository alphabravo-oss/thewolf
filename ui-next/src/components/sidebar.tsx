// Sidebar nav. Astronomer-style: dense, grouped, with subtle active-state
// glow. Items are mostly Link-driven; the bottom group holds Settings +
// account info.
import { Link, useRouterState } from "@tanstack/react-router";
import {
  LayoutDashboardIcon,
  PackageIcon,
  BugIcon,
  WrenchIcon,
  RepeatIcon,
  ContainerIcon,
  SettingsIcon,
  GaugeIcon,
} from "lucide-react";
import { WolfLogo } from "./wolf-logo";
import { cn } from "@/lib/cn";

type NavItem = {
  label: string;
  to: string;
  icon: typeof PackageIcon;
};

const primary: NavItem[] = [
  { label: "Dashboard", to: "/", icon: LayoutDashboardIcon },
  { label: "Collections", to: "/collections", icon: PackageIcon },
  { label: "Scans", to: "/scans", icon: GaugeIcon },
  { label: "Findings", to: "/findings", icon: BugIcon },
  { label: "Fixes", to: "/fixes", icon: WrenchIcon },
  { label: "Loops", to: "/loops", icon: RepeatIcon },
  { label: "Scanners", to: "/scanners", icon: ContainerIcon },
];

const secondary: NavItem[] = [
  { label: "Settings", to: "/settings", icon: SettingsIcon },
];

export function Sidebar() {
  const pathname = useRouterState({
    select: (s) => s.location.pathname,
  });

  function isActive(to: string) {
    if (to === "/") return pathname === "/";
    return pathname.startsWith(to);
  }

  return (
    <aside className="w-60 shrink-0 bg-sidebar border-r border-sidebar-border flex flex-col">
      <div className="h-14 flex items-center gap-2 px-4 border-b border-sidebar-border">
        <WolfLogo className="size-7" />
        <div className="font-semibold tracking-tight text-sidebar-foreground">
          The Wolf
        </div>
      </div>

      <nav className="flex-1 px-2 py-3 space-y-0.5 overflow-y-auto">
        <NavSection label="">
          {primary.map((item) => (
            <NavLink key={item.to} item={item} active={isActive(item.to)} />
          ))}
        </NavSection>
      </nav>

      <div className="px-2 py-3 border-t border-sidebar-border space-y-0.5">
        {secondary.map((item) => (
          <NavLink key={item.to} item={item} active={isActive(item.to)} />
        ))}
      </div>
    </aside>
  );
}

function NavSection({
  label,
  children,
}: {
  label?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-0.5">
      {label && (
        <div className="px-3 pt-2 pb-1 text-2xs uppercase tracking-wide text-muted-foreground/70">
          {label}
        </div>
      )}
      {children}
    </div>
  );
}

function NavLink({ item, active }: { item: NavItem; active: boolean }) {
  const Icon = item.icon;
  return (
    <Link
      to={item.to}
      className={cn("nav-item", active && "active")}
    >
      <Icon className="size-4 shrink-0" />
      <span className="truncate">{item.label}</span>
    </Link>
  );
}
