"use client";

import { create } from "zustand";
import type {
  User,
  Scan,
  Finding,
  Fix,
  Loop,
  Collection,
  Severity,
  Category,
  FindingStatus,
} from "./types";

// Auth store

interface AuthState {
  user: User | null;
  token: string | null;
  setAuth: (user: User, token: string) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  token: null,
  setAuth: (user, token) => set({ user, token }),
  logout: () => set({ user: null, token: null }),
}));

// Scan store

interface ScanState {
  scans: Scan[];
  activeScan: Scan | null;
  setScans: (scans: Scan[]) => void;
  setActiveScan: (scan: Scan | null) => void;
  updateScan: (id: string, updates: Partial<Scan>) => void;
}

export const useScanStore = create<ScanState>((set) => ({
  scans: [],
  activeScan: null,
  setScans: (scans) => set({ scans }),
  setActiveScan: (scan) => set({ activeScan: scan }),
  updateScan: (id, updates) =>
    set((state) => ({
      scans: state.scans.map((s) => (s.id === id ? { ...s, ...updates } : s)),
      activeScan:
        state.activeScan?.id === id
          ? { ...state.activeScan, ...updates }
          : state.activeScan,
    })),
}));

// Finding store

interface FindingFilters {
  severity?: Severity[];
  category?: Category[];
  tool?: string[];
  repo_id?: string;
  collection_id?: string;
  status?: FindingStatus[];
  search?: string;
}

interface FindingState {
  findings: Finding[];
  filters: FindingFilters;
  setFindings: (findings: Finding[]) => void;
  setFilters: (filters: FindingFilters) => void;
  clearFilters: () => void;
}

export const useFindingStore = create<FindingState>((set) => ({
  findings: [],
  filters: {},
  setFindings: (findings) => set({ findings }),
  setFilters: (filters) => set((state) => ({ filters: { ...state.filters, ...filters } })),
  clearFilters: () => set({ filters: {} }),
}));

// Fix store

interface FixState {
  fixes: Fix[];
  activeFix: Fix | null;
  setFixes: (fixes: Fix[]) => void;
  setActiveFix: (fix: Fix | null) => void;
}

export const useFixStore = create<FixState>((set) => ({
  fixes: [],
  activeFix: null,
  setFixes: (fixes) => set({ fixes }),
  setActiveFix: (fix) => set({ activeFix: fix }),
}));

// Loop store

interface LoopState {
  loops: Loop[];
  activeLoop: Loop | null;
  setLoops: (loops: Loop[]) => void;
  setActiveLoop: (loop: Loop | null) => void;
}

export const useLoopStore = create<LoopState>((set) => ({
  loops: [],
  activeLoop: null,
  setLoops: (loops) => set({ loops }),
  setActiveLoop: (loop) => set({ activeLoop: loop }),
}));

// Collection store

interface CollectionState {
  collections: Collection[];
  setCollections: (collections: Collection[]) => void;
  updateCollection: (id: string, updates: Partial<Collection>) => void;
}

export const useCollectionStore = create<CollectionState>((set) => ({
  collections: [],
  setCollections: (collections) => set({ collections }),
  updateCollection: (id, updates) =>
    set((state) => ({
      collections: state.collections.map((c) =>
        c.id === id ? { ...c, ...updates } : c,
      ),
    })),
}));
