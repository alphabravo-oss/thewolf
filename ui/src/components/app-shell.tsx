// The persistent app frame: sidebar + topbar + main content area.
// Used by every authenticated route. The Outlet below renders the
// matched child route inside the main panel.
import { Outlet } from "@tanstack/react-router";
import { Sidebar } from "./sidebar";
import { Topbar } from "./topbar";
import { CommandPalette } from "./command-palette";
import { ShortcutsOverlay } from "./shortcuts-overlay";
import { OrphanRecordsBanner } from "./orphan-records-banner";
import { useRouteShortcuts } from "@/lib/route-shortcuts";

export function AppShell() {
  // `g <letter>` route shortcuts — registered once at the shell.
  useRouteShortcuts();
  return (
    <div className="flex h-screen overflow-hidden bg-background text-foreground">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[100] focus:rounded-md focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:text-foreground focus:shadow-lg focus:ring-2 focus:ring-ring"
      >
        Skip to main content
      </a>
      <Sidebar />
      <div className="flex-1 flex flex-col min-w-0">
        <Topbar />
        <main
          id="main-content"
          tabIndex={-1}
          className="flex-1 overflow-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          <OrphanRecordsBanner />
          <Outlet />
        </main>
      </div>
      <CommandPalette />
      <ShortcutsOverlay />
    </div>
  );
}
