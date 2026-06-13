// Findings view state: severity/status filters, search, saved views.
// Persisted to localStorage so a user's filter set survives reloads.
import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { FindingStatus, Severity } from "./types";

interface SavedView {
  id: string;
  name: string;
  severities: Severity[]; // serialized as array (Set isn't JSON-friendly)
  status: FindingStatus | null;
  search: string;
}

interface FindingsViewState {
  search: string;
  severities: Set<Severity>;
  status: FindingStatus | null;
  savedViews: SavedView[];
  activeViewId: string | null;

  set: (partial: {
    search?: string;
    status?: FindingStatus | null;
    severities?: Set<Severity>;
  }) => void;
  toggleSeverity: (s: Severity) => void;
  saveCurrent: (name: string) => void;
  applyView: (id: string | null) => void;
  deleteView: (id: string) => void;
  reset: () => void;
}

const initialSev = (): Set<Severity> =>
  new Set<Severity>(["critical", "high", "medium", "low", "info"]);

// Custom JSON storage that handles Set serialization round-trip.
const setSerializer = {
  reviver: (key: string, value: unknown) => {
    if (key === "severities" && Array.isArray(value)) {
      return new Set(value as Severity[]);
    }
    return value;
  },
  replacer: (key: string, value: unknown) => {
    if (value instanceof Set) return Array.from(value);
    return value;
  },
};

export const useFindingsView = create<FindingsViewState>()(
  persist(
    (set, get) => ({
      search: "",
      severities: initialSev(),
      status: null,
      savedViews: [],
      activeViewId: null,

      set: (partial) =>
        set((prev) => ({
          ...prev,
          ...partial,
          activeViewId: null,
        })),

      toggleSeverity: (s) => {
        const cur = new Set(get().severities);
        if (cur.has(s)) cur.delete(s);
        else cur.add(s);
        set({ severities: cur, activeViewId: null });
      },

      saveCurrent: (name) => {
        const id = `view-${Date.now().toString(36)}`;
        const view: SavedView = {
          id,
          name,
          severities: Array.from(get().severities),
          status: get().status,
          search: get().search,
        };
        set({
          savedViews: [...get().savedViews, view],
          activeViewId: id,
        });
      },

      applyView: (id) => {
        if (id === null) {
          set({
            severities: initialSev(),
            status: null,
            search: "",
            activeViewId: null,
          });
          return;
        }
        const v = get().savedViews.find((sv) => sv.id === id);
        if (!v) return;
        set({
          severities: new Set(v.severities),
          status: v.status,
          search: v.search,
          activeViewId: id,
        });
      },

      deleteView: (id) =>
        set({
          savedViews: get().savedViews.filter((v) => v.id !== id),
          activeViewId: get().activeViewId === id ? null : get().activeViewId,
        }),

      reset: () =>
        set({
          severities: initialSev(),
          status: null,
          search: "",
          activeViewId: null,
        }),
    }),
    {
      name: "wolf-findings-view",
      storage: {
        getItem: (name) => {
          const raw = localStorage.getItem(name);
          if (!raw) return null;
          return JSON.parse(raw, setSerializer.reviver);
        },
        setItem: (name, value) =>
          localStorage.setItem(name, JSON.stringify(value, setSerializer.replacer)),
        removeItem: (name) => localStorage.removeItem(name),
      },
    },
  ),
);
