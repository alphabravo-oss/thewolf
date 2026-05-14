// Command palette (⌘K). Built on cmdk — the keystroke-fast popup that
// jumps to routes, runs scans, opens the shortcuts cheatsheet, etc.
// Pulls recent collections / scans / repos from the API so they're
// reachable from a single keystroke + filter.
import { useEffect } from "react";
import { Command } from "cmdk";
import { useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  PackageIcon,
  GaugeIcon,
  BugIcon,
  WrenchIcon,
  RepeatIcon,
  ContainerIcon,
  SettingsIcon,
  KeyboardIcon,
  LayoutDashboardIcon,
} from "lucide-react";
import { api } from "@/lib/api";
import type { Collection, Repo, Scan } from "@/lib/types";
import { useUIStore } from "@/lib/store-ui";
import { cn } from "@/lib/cn";

export function CommandPalette() {
  const open = useUIStore((s) => s.paletteOpen);
  const close = useUIStore((s) => s.closePalette);
  const toggle = useUIStore((s) => s.togglePalette);
  const openShortcuts = useUIStore((s) => s.openShortcuts);
  const navigate = useNavigate();

  // Global ⌘K / Ctrl+K binding.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        toggle();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggle]);

  // Light prefetch of recent entities so they show up instantly when
  // the palette opens.
  const collections = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      const r = await api.get<Collection[]>("/collections");
      return r.data ?? [];
    },
    enabled: open,
  });
  const recentScans = useQuery({
    queryKey: ["scans", "recent"],
    queryFn: async () => {
      const r = await api.get<Scan[]>("/scans?limit=10");
      return r.data ?? [];
    },
    enabled: open,
  });
  const repos = useQuery({
    queryKey: ["repos", "all"],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos");
      return r.data ?? [];
    },
    enabled: open,
  });

  function go(to: string) {
    close();
    navigate({ to });
  }

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 grid place-items-start pt-24 bg-black/50 backdrop-blur-sm animate-fade-in"
      onClick={close}
    >
      <Command
        label="Command palette"
        onClick={(e) => e.stopPropagation()}
        className="w-[min(40rem,calc(100vw-2rem))] glass-card border bg-popover/95 shadow-2xl"
      >
        <Command.Input
          placeholder="Search collections, scans, repos, or jump to a page…"
          className={cn(
            "w-full px-4 h-12 bg-transparent border-b border-border outline-none",
            "text-foreground placeholder:text-muted-foreground/70 text-sm",
          )}
          autoFocus
        />
        <Command.List className="max-h-[24rem] overflow-y-auto p-2 text-sm">
          <Command.Empty className="px-3 py-6 text-center text-muted-foreground">
            No matches.
          </Command.Empty>

          <Command.Group heading="Navigate">
            <PaletteItem
              onSelect={() => go("/")}
              icon={LayoutDashboardIcon}
              label="Dashboard"
              shortcut="g d"
            />
            <PaletteItem
              onSelect={() => go("/collections")}
              icon={PackageIcon}
              label="Collections"
              shortcut="g c"
            />
            <PaletteItem
              onSelect={() => go("/scans")}
              icon={GaugeIcon}
              label="Scans"
              shortcut="g s"
            />
            <PaletteItem
              onSelect={() => go("/findings")}
              icon={BugIcon}
              label="Findings"
              shortcut="g f"
            />
            <PaletteItem
              onSelect={() => go("/fixes")}
              icon={WrenchIcon}
              label="Fixes"
            />
            <PaletteItem
              onSelect={() => go("/loops")}
              icon={RepeatIcon}
              label="Loops"
            />
            <PaletteItem
              onSelect={() => go("/scanners")}
              icon={ContainerIcon}
              label="Scanners"
            />
            <PaletteItem
              onSelect={() => go("/settings")}
              icon={SettingsIcon}
              label="Settings"
            />
          </Command.Group>

          <Command.Group heading="Help">
            <PaletteItem
              onSelect={() => {
                close();
                openShortcuts();
              }}
              icon={KeyboardIcon}
              label="Show keyboard shortcuts"
              shortcut="?"
            />
          </Command.Group>

          {collections.data && collections.data.length > 0 && (
            <Command.Group heading="Collections">
              {collections.data.slice(0, 8).map((c) => (
                <PaletteItem
                  key={c.id}
                  onSelect={() => go(`/collections/${c.id}`)}
                  icon={PackageIcon}
                  label={c.name}
                  hint={`${c.repo_count ?? 0} repos`}
                />
              ))}
            </Command.Group>
          )}

          {recentScans.data && recentScans.data.length > 0 && (
            <Command.Group heading="Recent scans">
              {recentScans.data.slice(0, 6).map((s) => (
                <PaletteItem
                  key={s.id}
                  onSelect={() => go(`/scans/${s.id}`)}
                  icon={GaugeIcon}
                  label={s.repo?.name ?? s.id.slice(0, 8)}
                  hint={`${s.status} · ${s.finding_count} findings`}
                />
              ))}
            </Command.Group>
          )}

          {repos.data && repos.data.length > 0 && (
            <Command.Group heading="Repositories">
              {repos.data.slice(0, 8).map((r) => (
                <PaletteItem
                  key={r.id}
                  onSelect={() => go(`/scans?repo=${r.id}`)}
                  icon={BugIcon}
                  label={r.name}
                  hint={r.default_branch}
                />
              ))}
            </Command.Group>
          )}
        </Command.List>
      </Command>
    </div>
  );
}

function PaletteItem({
  onSelect,
  icon: Icon,
  label,
  hint,
  shortcut,
}: {
  onSelect: () => void;
  icon: typeof PackageIcon;
  label: string;
  hint?: string;
  shortcut?: string;
}) {
  return (
    <Command.Item
      onSelect={onSelect}
      className={cn(
        "flex items-center gap-3 px-3 py-2 rounded-md cursor-pointer",
        "aria-selected:bg-accent aria-selected:text-accent-foreground",
        "data-[selected=true]:bg-accent",
      )}
    >
      <Icon className="size-4 text-muted-foreground" />
      <span className="flex-1 truncate">{label}</span>
      {hint && (
        <span className="text-xs text-muted-foreground truncate">{hint}</span>
      )}
      {shortcut && (
        <kbd className="text-2xs px-1.5 py-0.5 rounded bg-muted/70 text-muted-foreground">
          {shortcut}
        </kbd>
      )}
    </Command.Item>
  );
}
