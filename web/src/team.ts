import { create } from "zustand";
import type { Me, ProjectInfo } from "./api";

// The signed-in user and open project on a team server; both stay null in
// local (single-user) mode.
interface TeamState {
  me: Me | null;
  project: ProjectInfo | null;
  set: (s: Partial<Pick<TeamState, "me" | "project">>) => void;
}

export const useTeam = create<TeamState>((set) => ({
  me: null,
  project: null,
  set: (s) => set(s),
}));

/** Opens a project (a full page load, so every query starts fresh). */
export function openProject(id: string) {
  try {
    localStorage.setItem("pyro.project", id);
  } catch {
    /* storage unavailable */
  }
  window.location.assign(`/p/${encodeURIComponent(id)}`);
}

/** The project id in the address bar (/p/<id>), if any. */
export function projectFromURL(): string | null {
  const m = /^\/p\/([^/]+)/.exec(window.location.pathname);
  return m ? decodeURIComponent(m[1]) : null;
}

export function lastProject(): string | null {
  try {
    return localStorage.getItem("pyro.project");
  } catch {
    return null;
  }
}
