import { Link, useRouterState } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  BookOpen,
  Bot,
  Bug,
  Building2,
  ChevronLeft,
  ChevronRight,
  Gauge,
  FolderGit2,
  KeyRound,
  Layers,
  LayoutDashboard,
  Menu,
  Package,
  Plug,
  ScrollText,
  Settings,
  ShieldAlert,
  ShieldCheck,
  X,
} from "lucide-react";
import { WolfLogo } from "@/components/wolf-logo";
import { useEdition, useVersion } from "@/lib/edition";
import { useFlag } from "@/lib/flags";
import { useIsAdmin } from "@/lib/me";
import { useUIStore } from "@/lib/store-ui";
import { cn } from "@/lib/utils";

type NavItem = {
  label: string;
  to: string;
  icon: typeof Package;
  exact?: boolean;
};

type NavGroup = {
  label: string;
  items: NavItem[];
};

const enterpriseIcons: Record<string, typeof Package> = {
  "/enterprise/identity": KeyRound,
  "/enterprise/tenancy": Building2,
  "/enterprise/integrations": Plug,
  "/enterprise/attack-paths": ShieldAlert,
  "/enterprise/verifications": ShieldCheck,
};

function useNavGroups(): NavGroup[] {
  const autofix = useFlag("autofix_enabled");
  const isAdmin = useIsAdmin();
  const editionQ = useEdition();

  return useMemo(() => {
    const primary: NavItem[] = [
      { label: "Home", to: "/", icon: LayoutDashboard, exact: true },
      { label: "Repositories", to: "/repos", icon: FolderGit2 },
      { label: "Findings", to: "/findings", icon: Bug },
      { label: "Vulnerabilities", to: "/vulnerabilities", icon: ShieldAlert },
      { label: "Scans", to: "/scans", icon: Gauge },
    ];
    if (autofix.enabled) primary.push({ label: "Fixes", to: "/fixes", icon: Bot });
    const secondary: NavItem[] = [
      { label: "Collections", to: "/collections", icon: Package },
      { label: "Scanner coverage", to: "/coverage", icon: Layers },
    ];

    const enterprise: NavItem[] = (editionQ.data?.edition === "enterprise" ? editionQ.data.ui_routes ?? [] : [])
      .filter((rt) => rt.path?.startsWith("/enterprise/") && rt.title)
      .map((rt) => ({
        label: rt.title,
        to: rt.path,
        icon: enterpriseIcons[rt.path] ?? Building2,
      }));

    const admin: NavItem[] = isAdmin ? [
      { label: "Settings", to: "/settings", icon: Settings },
      { label: "Audit", to: "/audit", icon: ScrollText },
    ] : [];

    return [
      { label: "Daily work", items: primary },
      { label: "Explore", items: secondary },
      { label: "Enterprise", items: enterprise },
      { label: "Administration", items: admin },
    ].filter((g) => g.items.length > 0);
  }, [autofix.enabled, isAdmin, editionQ.data]);
}

function isActive(pathname: string, item: NavItem) {
  if (item.exact || item.to === "/") return pathname === item.to;
  return pathname === item.to || pathname.startsWith(`${item.to}/`);
}

export function Sidebar({ onMobileOpenChange }: { onMobileOpenChange: (open: boolean) => void }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navGroups = useNavGroups();
  const collapsed = useUIStore((s) => s.sidebarCollapsed);
  const setCollapsed = useUIStore((s) => s.setSidebarCollapsed);

  const versionQ = useVersion();
  const version = versionQ.data?.community?.version || versionQ.data?.version;
  const displayVersion = version && version !== "unknown" ? version.replace(/^v/, "") : null;

  // Mobile drawer: hide the background, trap focus, and return it on close.
  const [mobileOpen, setMobileOpen] = useState(false);
  const [isDesktop, setIsDesktop] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia("(min-width: 768px)").matches,
  );
  const openButtonRef = useRef<HTMLButtonElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const drawerRef = useRef<HTMLElement>(null);

  useEffect(() => { onMobileOpenChange(mobileOpen && !isDesktop); }, [mobileOpen, isDesktop, onMobileOpenChange]);

  useEffect(() => {
    setMobileOpen(false);
  }, [pathname]);

  useEffect(() => {
    const desktopQuery = window.matchMedia("(min-width: 768px)");
    const updateViewport = () => {
      setIsDesktop(desktopQuery.matches);
      if (desktopQuery.matches) setMobileOpen(false);
    };
    updateViewport();
    desktopQuery.addEventListener("change", updateViewport);
    return () => desktopQuery.removeEventListener("change", updateViewport);
  }, []);

  useEffect(() => {
    if (!mobileOpen || isDesktop) return;
    closeButtonRef.current?.focus();
    const drawer = drawerRef.current;
    if (!drawer) return;

    const focusableSelector = [
      "a[href]",
      "button:not([disabled])",
      "input:not([disabled])",
      "select:not([disabled])",
      "textarea:not([disabled])",
      '[tabindex]:not([tabindex="-1"])',
    ].join(",");
    const focusableElements = () =>
      [...drawer.querySelectorAll<HTMLElement>(focusableSelector)].filter(
        (element) =>
          !element.hidden &&
          element.getAttribute("aria-hidden") !== "true" &&
          element.getClientRects().length > 0,
      );

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setMobileOpen(false);
        window.requestAnimationFrame(() => openButtonRef.current?.focus());
        return;
      }
      if (event.key !== "Tab") return;

      const elements = focusableElements();
      if (elements.length === 0) {
        event.preventDefault();
        return;
      }
      const first = elements[0];
      const last = elements[elements.length - 1];
      const active = document.activeElement;
      if (event.shiftKey && (active === first || !drawer.contains(active))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (active === last || !drawer.contains(active))) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isDesktop, mobileOpen]);

  function closeMobileMenu() {
    setMobileOpen(false);
    window.requestAnimationFrame(() => openButtonRef.current?.focus());
  }

  // Collapse only applies on desktop; the mobile drawer always shows labels.
  const railCollapsed = collapsed && isDesktop;

  return (
    <>
      {/* Mobile hamburger — fixed top-left, only visible <md. */}
      <button
        ref={openButtonRef}
        type="button"
        className={cn(
          // z-40 keeps the trigger above the sticky topbar (z-30) — at equal
          // z-index the topbar wins on DOM order and its breadcrumb nav
          // swallows the click. Still below the drawer backdrop and panel
          // (z-40 later in DOM / z-50), so an open drawer covers it.
          "md:hidden fixed top-3 left-3 z-40 size-9 grid place-items-center rounded-md",
          "bg-card border border-border shadow-md",
        )}
        aria-label="Open menu"
        aria-controls="primary-navigation"
        aria-expanded={mobileOpen}
        aria-haspopup="dialog"
        onClick={() => setMobileOpen(true)}
      >
        <Menu className="size-4" aria-hidden="true" />
      </button>

      {mobileOpen && (
        <button
          type="button"
          tabIndex={-1}
          aria-label="Close navigation menu"
          className="md:hidden fixed inset-0 z-40 border-0 bg-background/80 p-0 backdrop-blur-sm animate-fade-in"
          onClick={closeMobileMenu}
        />
      )}

      <aside
        id="primary-navigation"
        ref={drawerRef}
        role={isDesktop ? undefined : "dialog"}
        aria-modal={!isDesktop && mobileOpen ? true : undefined}
        aria-label={isDesktop ? undefined : "Primary navigation"}
        aria-hidden={!isDesktop && !mobileOpen ? true : undefined}
        inert={!isDesktop && !mobileOpen ? true : undefined}
        className={cn(
          "z-50 shrink-0 flex flex-col bg-sidebar border-r border-sidebar-border",
          "transition-all duration-200 ease-in-out",
          railCollapsed ? "w-16" : "w-60",
          "md:static md:flex md:translate-x-0",
          "fixed top-0 left-0 h-dvh md:h-screen overscroll-contain",
          mobileOpen ? "translate-x-0" : "-translate-x-full md:translate-x-0",
        )}
      >
        {/* Logo + collapse toggle */}
        <div className="flex items-center h-14 px-4 border-b border-sidebar-border">
          {!railCollapsed && (
            <Link to="/" className="flex items-center gap-2.5 min-w-0">
              {/* The mark sits in a solid accent tile (the steel accent from the
                  site palette), so the lockup reads in both themes. */}
              <div className="flex size-7 flex-shrink-0 items-center justify-center rounded-md bg-primary">
                <WolfLogo bare className="h-4 w-4 text-primary-foreground" />
              </div>
              <span className="text-sm font-semibold tracking-tight">Wolf</span>
              <span className="rounded-sm border border-sidebar-border px-1.5 py-0.5 font-narrow text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
                {versionQ.data?.edition === "enterprise" ? "Enterprise" : "Community"}
              </span>
            </Link>
          )}
          <button
            type="button"
            onClick={() => setCollapsed(!collapsed)}
            className={cn("nav-item hidden md:flex", railCollapsed ? "w-full justify-center px-0" : "ml-auto")}
            title={railCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-label={railCollapsed ? "Expand sidebar" : "Collapse sidebar"}
          >
            {railCollapsed ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
          </button>
          <button
            ref={closeButtonRef}
            type="button"
            onClick={closeMobileMenu}
            className="md:hidden ml-auto size-7 grid place-items-center rounded-md hover:bg-sidebar-accent"
            aria-label="Close menu"
          >
            <X className="size-4" aria-hidden="true" />
          </button>
        </div>

        {/* Navigation */}
        <nav aria-label="Primary" className="flex-1 overflow-y-auto py-3 px-2 no-scrollbar">
          {navGroups.map((group) => (
            <SidebarGroup
              key={group.label}
              group={group}
              pathname={pathname}
              collapsed={railCollapsed}

            />
          ))}
        </nav>

        {/* Bottom links */}
        <div className="mt-auto px-2 py-2 border-t border-sidebar-border space-y-1">
          <a
            href="https://alphabravo.io"
            target="_blank"
            rel="noopener noreferrer"
            className="nav-item w-full"
            title="Documentation"
          >
            <BookOpen className="h-4 w-4" />
            {!railCollapsed && <span className="text-xs">Documentation</span>}
          </a>
          {!railCollapsed && displayVersion && (
            <p className="px-3 py-1 text-xs text-muted-foreground tabular-nums">v{displayVersion}</p>
          )}
        </div>
      </aside>
    </>
  );
}

function SidebarGroup({ group, pathname, collapsed }: {
  group: NavGroup;
  pathname: string;
  collapsed: boolean;
}) {
  return (
    <div className={cn("space-y-1", group.label !== "Daily work" && "mt-4 border-t border-sidebar-border pt-3")}>
      {!collapsed && ["Administration", "Enterprise"].includes(group.label) && (
        <p className="eyebrow px-3 pt-1">{group.label}</p>
      )}
      {group.items.map((item) => {
        const Icon = item.icon;
        const active = isActive(pathname, item) || (item.to === "/fixes" && pathname.startsWith("/agents"));
        return (
          <Link key={item.to} to={item.to} title={collapsed ? item.label : undefined}
            aria-label={collapsed ? item.label : undefined} aria-current={active ? "page" : undefined}
            className={cn("nav-item min-h-10", collapsed && "justify-center px-0", active && "active")}>
            <Icon aria-hidden="true" />
            {!collapsed && <span className="truncate">{item.label}</span>}
          </Link>
        );
      })}
    </div>
  );
}
