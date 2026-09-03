// Sidebar — structural port of astronomer/frontend/src/components/layout/sidebar.tsx,
// carrying Wolf's navigation instead of Astronomer's cluster tree.
//
// What comes from Astronomer: the `bg-sidebar` rail with a hairline right
// border, the 14-unit logo header with an inline collapse toggle, collapsible
// `NavGroup`s that auto-expand around the active route, the `.nav-item` class
// for every row, icon-only rendering at 4rem when collapsed, and the bottom
// docs/version block.
//
// What stays Wolf: the nav items themselves, and the gating rules behind them —
// Scans/Findings are for every signed-in user, Agents is behind the
// `autofix_enabled` flag, Audit is admin-only, and Collections is the folder
// model everyone browses.
//
// The mobile drawer (off-canvas below `md`, focus-trapped, Escape to close) is
// Wolf's own and has no Astronomer counterpart — Astronomer's console assumes a
// desktop viewport. It is kept because dropping it would regress accessibility
// on small screens.
import { Link, useRouterState } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  BookOpen,
  Bot,
  Bug,
  Building2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Gauge,
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
import { useEdition, useVersion, shortRev } from "@/lib/edition";
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
  defaultOpen?: boolean;
};

// Navigation follows the folder model: you browse Collections -> a collection
// -> a repo -> a scan -> its findings, so there is no top-level Repos item.
// Scans and Findings are owner-scoped inbox views for every signed-in user.
// Agents only when autonomous fixing is on.
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
    const platform: NavItem[] = [
      { label: "Home", to: "/", icon: LayoutDashboard, exact: true },
      { label: "Collections", to: "/collections", icon: Package },
    ];

    const fleet: NavItem[] = [
      { label: "Scans", to: "/scans", icon: Gauge },
      { label: "Findings", to: "/findings", icon: Bug },
      { label: "Vulnerabilities", to: "/vulnerabilities", icon: ShieldAlert },
      { label: "Coverage", to: "/coverage", icon: Layers },
    ];
    if (autofix.enabled) fleet.push({ label: "Agents", to: "/agents", icon: Bot });

    const enterprise: NavItem[] = (editionQ.data?.ui_routes ?? [])
      .filter((rt) => rt.path?.startsWith("/enterprise/") && rt.title)
      .map((rt) => ({
        label: rt.title,
        to: rt.path,
        icon: enterpriseIcons[rt.path] ?? Building2,
      }));

    const admin: NavItem[] = [];
    if (isAdmin) admin.push({ label: "Audit", to: "/audit", icon: ScrollText });
    admin.push({ label: "Settings", to: "/settings", icon: Settings });

    return [
      { label: "Platform", items: platform, defaultOpen: true },
      { label: "Fleet", items: fleet, defaultOpen: true },
      { label: "Enterprise", items: enterprise, defaultOpen: true },
      { label: "Administration", items: admin, defaultOpen: true },
    ].filter((g) => g.items.length > 0);
  }, [autofix.enabled, isAdmin, editionQ.data?.ui_routes]);
}

function isActive(pathname: string, item: NavItem) {
  if (item.exact || item.to === "/") return pathname === item.to;
  return pathname === item.to || pathname.startsWith(`${item.to}/`);
}

export function Sidebar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navGroups = useNavGroups();
  const collapsed = useUIStore((s) => s.sidebarCollapsed);
  const setCollapsed = useUIStore((s) => s.setSidebarCollapsed);

  const versionQ = useVersion();

  // Multiple groups stay open at once — a single-open accordion would collapse
  // your context every time you expanded another. Seeded from `defaultOpen`.
  const [openGroups, setOpenGroups] = useState<Set<string>>(
    () => new Set(navGroups.filter((g) => g.defaultOpen).map((g) => g.label)),
  );

  // Keep `defaultOpen` groups open and auto-expand the group holding the active
  // route, without collapsing the rest.
  useEffect(() => {
    setOpenGroups((prev) => {
      const next = new Set(prev);
      let changed = false;
      for (const g of navGroups) {
        if (g.defaultOpen && !next.has(g.label)) {
          next.add(g.label);
          changed = true;
        }
      }
      const activeGroup = navGroups.find((g) => g.items.some((i) => isActive(pathname, i)));
      if (activeGroup && !next.has(activeGroup.label)) {
        next.add(activeGroup.label);
        changed = true;
      }
      return changed ? next : prev;
    });
  }, [navGroups, pathname]);

  // ---- Mobile drawer (Wolf-specific; see file header) ----
  const [mobileOpen, setMobileOpen] = useState(false);
  const [isDesktop, setIsDesktop] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia("(min-width: 768px)").matches,
  );
  const openButtonRef = useRef<HTMLButtonElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const drawerRef = useRef<HTMLElement>(null);

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
              {/* The mark sits in a rounded gradient tile, matching the brand
                  lockup in the Astronomer console. Wolf keeps its own mark;
                  only the container treatment is shared. */}
              <div className="flex-shrink-0 w-7 h-7 rounded-lg bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center">
                <WolfLogo bare className="h-4 w-4 text-white" />
              </div>
              <div className="flex flex-col min-w-0">
                <span className="text-sm font-semibold text-foreground tracking-tight truncate leading-tight">
                  {versionQ.data?.product ?? "The Wolf"}
                </span>
                <span className="text-[10px] text-muted-foreground leading-tight">
                  {versionQ.data?.edition === "enterprise" ? "Enterprise · " : "Community · "}
                  by AlphaBravo
                </span>
              </div>
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
        <nav className="flex-1 overflow-y-auto py-2 px-1 no-scrollbar">
          {navGroups.map((group) => (
            <SidebarGroup
              key={group.label}
              group={group}
              pathname={pathname}
              collapsed={railCollapsed}
              isOpen={openGroups.has(group.label)}
              onToggle={() =>
                setOpenGroups((prev) => {
                  const next = new Set(prev);
                  if (next.has(group.label)) next.delete(group.label);
                  else next.add(group.label);
                  return next;
                })
              }
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
          {!railCollapsed && (
            <div className="px-3 py-1 space-y-0.5">
              <p className="text-[10px] text-muted-foreground tabular-nums">
                Community v{versionQ.data?.community?.version ?? versionQ.data?.version ?? ""}
                {versionQ.data?.community?.commit
                  ? ` · ${shortRev(versionQ.data.community.commit)}`
                  : versionQ.data?.commit
                    ? ` · ${shortRev(versionQ.data.commit)}`
                    : ""}
              </p>
              {versionQ.data?.overlay ? (
                <p className="text-[10px] text-muted-foreground tabular-nums">
                  Enterprise overlay {shortRev(versionQ.data.overlay.version || versionQ.data.overlay.commit)}
                </p>
              ) : (
                <p className="text-[10px] text-muted-foreground">
                  {versionQ.data?.product ?? "Wolf Community"} ·{" "}
                  {versionQ.data?.license ?? "source-available"}
                </p>
              )}
              <p className="text-[10px] text-muted-foreground">
                Built by{" "}
                <a
                  href="https://alphabravo.io"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="hover:text-foreground underline-offset-2 hover:underline"
                >
                  AlphaBravo
                </a>
              </p>
            </div>
          )}
        </div>
      </aside>
    </>
  );
}

// Collapsible nav group — Astronomer's Rancher-style accordion.
function SidebarGroup({
  group,
  pathname,
  collapsed,
  isOpen,
  onToggle,
}: {
  group: NavGroup;
  pathname: string;
  collapsed: boolean;
  isOpen: boolean;
  onToggle: () => void;
}) {
  if (collapsed) {
    return (
      <div className="space-y-0.5">
        {group.items.map((item) => {
          const Icon = item.icon;
          const active = isActive(pathname, item);
          return (
            <Link
              key={item.to}
              to={item.to}
              className={cn("nav-item group justify-center px-0", active && "active")}
              title={item.label}
            >
              <Icon
                className={cn(
                  "h-4 w-4 flex-shrink-0",
                  active ? "text-foreground" : "text-muted-foreground group-hover:text-foreground",
                )}
              />
            </Link>
          );
        })}
      </div>
    );
  }

  return (
    <div className="mb-1">
      {/* Group header with the chevron on the right (Rancher style). Sentence
          case at text-sm — NOT the small-caps treatment Wolf used to have;
          Astronomer's group labels read as headings, not as eyebrows. */}
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={isOpen}
        className="w-full flex items-center justify-between px-3 py-2 text-sm font-semibold text-muted-foreground hover:text-foreground transition-colors"
      >
        <span>{group.label}</span>
        {isOpen ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
      </button>
      {isOpen && (
        <div className="space-y-px">
          {group.items.map((item) => {
            const Icon = item.icon;
            const active = isActive(pathname, item);
            // Expanded rows are deliberately NOT `.nav-item`: Astronomer uses a
            // tighter row here (gap-2, py-1.5, inset by mx-1) and reserves the
            // roomier `.nav-item` for the collapsed rail and the footer links.
            return (
              <Link
                key={item.to}
                to={item.to}
                className={cn(
                  "flex items-center gap-2 px-3 py-1.5 mx-1 rounded-md text-sm transition-colors",
                  active
                    ? "bg-accent text-foreground font-medium"
                    : "text-muted-foreground hover:text-foreground hover:bg-accent/50",
                )}
              >
                <Icon
                  className={cn(
                    "h-4 w-4 flex-shrink-0",
                    active ? "text-foreground" : "text-muted-foreground",
                  )}
                />
                <span className="truncate flex-1">{item.label}</span>
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}
