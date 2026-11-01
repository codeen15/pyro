import { create } from "zustand";
import type { PyroRequest, SendResult } from "./api";

// Per-viewer conveniences only; the workspace files are the source of truth.
function load(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function save(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    /* storage unavailable */
  }
}

interface AppState {
  selected: string | null;
  environment: string | null;
  /** Unsaved edits, keyed by request path. */
  drafts: Record<string, PyroRequest>;
  /** Latest send result per request path ("" for history replays). */
  results: Record<string, SendResult>;
  sending: Record<string, boolean>;
  sidebarTab: "collections" | "history" | "load";

  select: (path: string | null) => void;
  setEnvironment: (name: string | null) => void;
  setDraft: (path: string, r: PyroRequest) => void;
  clearDraft: (path: string) => void;
  setResult: (path: string, r: SendResult) => void;
  setSending: (path: string, v: boolean) => void;
  setSidebarTab: (t: AppState["sidebarTab"]) => void;
}

export const useApp = create<AppState>((set) => ({
  selected: load("pyro.selected"),
  environment: load("pyro.environment"),
  drafts: {},
  results: {},
  sending: {},
  sidebarTab: "collections",

  select: (path) => {
    save("pyro.selected", path);
    set({ selected: path });
  },
  setEnvironment: (name) => {
    save("pyro.environment", name);
    set({ environment: name });
  },
  setDraft: (path, r) => set((s) => ({ drafts: { ...s.drafts, [path]: r } })),
  clearDraft: (path) =>
    set((s) => {
      const { [path]: _, ...rest } = s.drafts;
      return { drafts: rest };
    }),
  setResult: (path, r) => set((s) => ({ results: { ...s.results, [path]: r } })),
  setSending: (path, v) => set((s) => ({ sending: { ...s.sending, [path]: v } })),
  setSidebarTab: (t) => set({ sidebarTab: t }),
}));
