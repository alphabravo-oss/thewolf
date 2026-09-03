// Command palette (⌘K). Built on shadcn's CommandDialog wrapper over cmdk —
// the keystroke-fast popup that jumps to routes, runs scans, opens the
// shortcuts cheatsheet, etc. Pulls recent collections / scans / repos from
// the API so they're reachable from a single keystroke + filter.
import { useEffect } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  PackageIcon,
  GaugeIcon,
  BugIcon,
  ShieldAlertIcon,
  LayersIcon,
  SettingsIcon,
  KeyboardIcon,
  LayoutDashboardIcon,
} from "lucide-react";
import { api } from "@/lib/api";
import type { Collection, Repo, Scan } from "@/lib/types";
import { useUIStore } from "@/lib/store-ui";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut,
} from "@/components/ui/command";

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

  return (
    <CommandDialog open={open} onOpenChange={(o) => (o ? toggle() : close())}>
      <CommandInput placeholder="Search collections, scans, repos, or jump to a page…" />
      <CommandList>
        <CommandEmpty>No matches.</CommandEmpty>

        <CommandGroup heading="Navigate">
          <PaletteItem
            onSelect={() => go("/")}
            icon={LayoutDashboardIcon}
            label="Home"
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
            onSelect={() => go("/vulnerabilities")}
            icon={ShieldAlertIcon}
            label="Vulnerabilities"
            shortcut="g v"
          />
          <PaletteItem
            onSelect={() => go("/coverage")}
            icon={LayersIcon}
            label="Coverage"
          />
          <PaletteItem
            onSelect={() => go("/settings")}
            icon={SettingsIcon}
            label="Settings"
          />
        </CommandGroup>

        <CommandGroup heading="Help">
          <PaletteItem
            onSelect={() => {
              close();
              openShortcuts();
            }}
            icon={KeyboardIcon}
            label="Show keyboard shortcuts"
            shortcut="?"
          />
        </CommandGroup>

        {collections.data && collections.data.length > 0 && (
          <CommandGroup heading="Collections">
            {collections.data.slice(0, 8).map((c) => (
              <PaletteItem
                key={c.id}
                onSelect={() => go(`/collections/${c.id}`)}
                icon={PackageIcon}
                label={c.name}
                hint={`${c.repo_count ?? 0} repos`}
              />
            ))}
          </CommandGroup>
        )}

        {recentScans.data && recentScans.data.length > 0 && (
          <CommandGroup heading="Recent scans">
            {recentScans.data.slice(0, 6).map((s) => (
              <PaletteItem
                key={s.id}
                onSelect={() => go(`/scans/${s.id}`)}
                icon={GaugeIcon}
                label={s.repo?.name ?? s.id.slice(0, 8)}
                hint={`${s.status} · ${s.finding_count} findings`}
              />
            ))}
          </CommandGroup>
        )}

        {repos.data && repos.data.length > 0 && (
          <CommandGroup heading="Repositories">
            {repos.data.slice(0, 8).map((r) => (
              <PaletteItem
                key={r.id}
                onSelect={() => go(`/scans?repo=${r.id}`)}
                icon={BugIcon}
                label={r.name}
                hint={r.default_branch}
              />
            ))}
          </CommandGroup>
        )}
      </CommandList>
    </CommandDialog>
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
    <CommandItem onSelect={onSelect}>
      <Icon className="size-4 text-muted-foreground" />
      <span className="flex-1 truncate">{label}</span>
      {hint && (
        <span className="text-xs text-muted-foreground truncate">{hint}</span>
      )}
      {shortcut && <CommandShortcut>{shortcut}</CommandShortcut>}
    </CommandItem>
  );
}
