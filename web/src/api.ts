// Thin client for the pyro engine's REST API (/api/v1).
// TODO(phase 1): generate these types from the OpenAPI spec.

export type Kind = "http" | "websocket" | "socketio" | `plugin:${string}`;

export interface TreeNode {
  type: "collection" | "folder" | "request";
  name: string;
  path: string;
  kind?: Kind;
  method?: string;
  children?: TreeNode[];
}

export interface WorkspaceInfo {
  name: string;
  root: string;
  collections: TreeNode[];
}

export interface KeyValue {
  key: string;
  value: string;
  disabled?: boolean;
}

export interface PyroRequest {
  kind: Kind;
  name: string;
  description?: string;
  method?: string;
  url: string;
  query?: KeyValue[];
  headers?: KeyValue[];
  body?: { type: string; content?: string; fields?: KeyValue[] };
  auth?: { type: string; params?: Record<string, string> };
  messages?: { name?: string; event?: string; content: string }[];
}

const TOKEN_KEY = "pyro.token";

// `pyro serve` prints a URL with ?token=…; keep it for this tab and strip it
// from the address bar so it isn't left in history or screenshots.
function takeToken(): string | null {
  const params = new URLSearchParams(window.location.search);
  const fromUrl = params.get("token");
  if (fromUrl) {
    try {
      sessionStorage.setItem(TOKEN_KEY, fromUrl);
    } catch {
      /* storage unavailable: keep the token in memory only */
    }
    params.delete("token");
    const qs = params.toString();
    window.history.replaceState(null, "", window.location.pathname + (qs ? `?${qs}` : "") + window.location.hash);
    return fromUrl;
  }
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

const token = takeToken();

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (token) headers["X-Pyro-Token"] = token;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, msg);
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

const encodePath = (p: string) => p.split("/").map(encodeURIComponent).join("/");

export const api = {
  health: () => call<{ status: string; version: string }>("GET", "/health"),
  workspace: () => call<WorkspaceInfo>("GET", "/workspace"),
  request: (path: string) => call<PyroRequest>("GET", `/requests/${encodePath(path)}`),
};
