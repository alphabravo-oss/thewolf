// Top bar: breadcrumbs (derived from the matched route), a global search
// trigger that opens the command palette, a running-scan pill, and an
// account/avatar slot.
import { useRouterState } from "@tanstack/react-router";
import { SearchIcon, ActivityIcon } from "lucide-react";
import { useUIStore } from "@/lib/store-ui";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Scan } from "@/lib/types";
import { Link } from "@tanstack/react-router";

export function Topbar() {
  const crumbs = useBreadcrumbs();
  const openPalette = useUIStore((s) => s.openPalette);

  // Subtle running-scan indicator. Polls every 5s; cheap and clearly
  // signals to the user when wolf is busy.
  const { data: running } = useQuery({
    queryKey: ["scans", "running"],
    queryFn: async () => {
      const r = await api.get<{ scans: Scan[] }>(
        "/scans?status=running&limit=10",
      );
      return r.data.scans ?? [];
    },
    refetchInterval: 5000,
    staleTime: 0,
  });

  return (
    <header className="h-14 shrink-0 border-b border-border flex items-center gap-3 px-4">
      <nav className="flex items-center gap-2 text-sm min-w-0">
        {crumbs.map((c, i) => (
          <span key={i} className="flex items-center gap-2 min-w-0">
            {i > 0 && <span className="text-muted-foreground/40">/</span>}
            <span
              className={
                i === crumbs.length - 1
                  ? "text-foreground font-medium truncate"
                  : "text-muted-foreground truncate"
              }
            >
              {c}
            </span>
          </span>
        ))}
      </nav>

      <div className="flex-1" />

      <button
        type="button"
        onClick={openPalette}
        className="hidden md:flex items-center gap-2 px-3 h-9 rounded-md border border-border bg-muted/30 hover:bg-muted/60 text-sm text-muted-foreground transition-colors min-w-[18rem]"
      >
        <SearchIcon className="size-4" />
        <span>Search…</span>
        <span className="ml-auto text-2xs px-1.5 py-0.5 rounded bg-muted/70 text-muted-foreground">
          ⌘K
        </span>
      </button>

      {running && running.length > 0 && (
        <Link
          to="/scans"
          className="flex items-center gap-2 h-9 px-3 rounded-md border border-blue-500/20 bg-blue-500/10 text-sm text-blue-300 hover:bg-blue-500/15 transition-colors glow-info"
        >
          <ActivityIcon className="size-4 animate-pulse" />
          <span className="font-medium">
            {running.length} scan{running.length === 1 ? "" : "s"} running
          </span>
        </Link>
      )}
    </header>
  );
}

// Derive a breadcrumb trail from the current router state. Each segment
// becomes a crumb; segments that look like UUIDs get truncated.
function useBreadcrumbs(): string[] {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  if (pathname === "/") return ["Dashboard"];
  return pathname
    .split("/")
    .filter(Boolean)
    .map((seg) => {
      // Truncate UUID-like segments to first 8 chars.
      if (/^[0-9a-f-]{20,}$/i.test(seg)) return seg.slice(0, 8);
      return seg.charAt(0).toUpperCase() + seg.slice(1);
    });
}
