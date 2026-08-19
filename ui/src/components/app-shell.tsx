// The persistent app frame — structural port of Astronomer's dashboard layout
// (astronomer/frontend/src/routes/dashboard/route.tsx).
//
// Astronomer's shape, reproduced: a full-height flex row of
// `Sidebar | (Topbar + main)`, `bg-background`, the offline banner between the
// topbar and content, and `main` scrolling a `p-6 max-w-[1600px] mx-auto
// animate-fade-in` container so every page shares one gutter and one measure.
// Pages therefore render bare content and never their own page padding.
import { Outlet } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { WifiOff } from "lucide-react";
import { Sidebar } from "./layout/sidebar";
import { Topbar } from "./layout/topbar";
import { CommandPalette } from "./command-palette";
import { ShortcutsOverlay } from "./shortcuts-overlay";
import { OrphanRecordsBanner } from "./orphan-records-banner";
import { useRouteShortcuts } from "@/lib/route-shortcuts";

export function AppShell() {
  // `g <letter>` route shortcuts — registered once at the shell.
  useRouteShortcuts();

  // Surface browser offline so hung tables/mutations are explained.
  const [online, setOnline] = useState(true);
  useEffect(() => {
    if (typeof navigator === "undefined") return;
    const sync = () => setOnline(navigator.onLine);
    sync();
    window.addEventListener("online", sync);
    window.addEventListener("offline", sync);
    return () => {
      window.removeEventListener("online", sync);
      window.removeEventListener("offline", sync);
    };
  }, []);

  return (
    <div data-testid="app-shell" className="flex h-screen overflow-hidden bg-background">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[100] focus:rounded-md focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:text-foreground focus:shadow-lg focus:ring-2 focus:ring-ring"
      >
        Skip to main content
      </a>
      <Sidebar />
      <div className="flex flex-col flex-1 min-w-0 overflow-hidden">
        <Topbar />
        {!online && (
          <div
            role="status"
            className="flex items-center gap-2 bg-amber-500/15 text-amber-900 dark:text-amber-100 border-b border-amber-500/30 px-4 py-2 text-sm"
          >
            <WifiOff className="h-4 w-4 shrink-0" />
            You are offline. Live updates and mutations will fail until connectivity returns.
          </div>
        )}
        <main
          id="main-content"
          tabIndex={-1}
          className="flex-1 min-h-0 overflow-y-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          <div className="p-6 max-w-[1600px] mx-auto animate-fade-in">
            <OrphanRecordsBanner />
            <Outlet />
          </div>
        </main>
      </div>
      <CommandPalette />
      <ShortcutsOverlay />
    </div>
  );
}
