// Process-local UI state: the things that DON'T live on the server.
// Persisted to localStorage where it makes sense (theme, sidebar
// collapsed); ephemeral otherwise (command-palette open, shortcuts
// overlay open).
import { create } from "zustand";
import { persist } from "zustand/middleware";

interface UIState {
  // Command palette open/close. Triggered by ⌘K / Ctrl+K and by topbar
  // search button.
  paletteOpen: boolean;
  openPalette: () => void;
  closePalette: () => void;
  togglePalette: () => void;

  // Shortcuts cheatsheet (? key).
  shortcutsOpen: boolean;
  openShortcuts: () => void;
  closeShortcuts: () => void;
  toggleShortcuts: () => void;

  // Sidebar collapsed (mobile auto, desktop persisted).
  sidebarCollapsed: boolean;
  setSidebarCollapsed: (v: boolean) => void;
}

export const useUIStore = create<UIState>()(
  persist(
    (set, get) => ({
      paletteOpen: false,
      openPalette: () => set({ paletteOpen: true }),
      closePalette: () => set({ paletteOpen: false }),
      togglePalette: () => set({ paletteOpen: !get().paletteOpen }),

      shortcutsOpen: false,
      openShortcuts: () => set({ shortcutsOpen: true }),
      closeShortcuts: () => set({ shortcutsOpen: false }),
      toggleShortcuts: () => set({ shortcutsOpen: !get().shortcutsOpen }),

      sidebarCollapsed: false,
      setSidebarCollapsed: (v: boolean) => set({ sidebarCollapsed: v }),
    }),
    {
      name: "wolf-ui",
      partialize: (s) => ({ sidebarCollapsed: s.sidebarCollapsed }),
    },
  ),
);
