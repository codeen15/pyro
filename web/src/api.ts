// Thin client for the pyro engine's REST API (/api/v1). Types mirror the Go
// structs in pkg/engine/model, pkg/engine/protocol and internal/server.

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
  file?: boolean;
}

export interface Variable {
  key: string;
  value: string;
  secret?: boolean;
  disabled?: boolean;
}

export interface Body {
  type: string;
  content?: string;
  fields?: KeyValue[];
  file?: string;
  variables?: string;
}

export interface Auth {
  type: string;
  params?: Record<string, string>;
}

export interface PyroRequest {
  kind: Kind;
  name: string;
  description?: string;
  method?: string;
  url: string;
  query?: KeyValue[];
  headers?: KeyValue[];
  body?: Body;
  auth?: Auth;
  scripts?: { pre?: string; post?: string };
  assertions?: Assertion[];
  extract?: Extractor[];
  messages?: SavedMessage[];
  socketio?: SocketIOConfig;
}

export interface Assertion {
  type: string;
  path?: string;
  op?: string;
  value?: string;
}

export interface Extractor {
  var: string;
  from: string;
  path?: string;
}

export interface SavedMessage {
  name?: string;
  event?: string;
  content: string;
  binary?: boolean;
  ack?: boolean;
}

export interface SocketIOConfig {
  path?: string;
  namespace?: string;
  auth?: string;
  listen?: string[];
}

export interface Environment {
  name: string;
  variables?: Variable[];
}

export interface Timings {
  dns: number;
  connect: number;
  tls: number;
  send: number;
  wait: number;
  download: number;
  total: number;
  reused: boolean;
}

export interface Cookie {
  name: string;
  value: string;
  domain?: string;
  path?: string;
  expires?: string;
  httpOnly?: boolean;
  secure?: boolean;
}

export interface PyroResponse {
  status: number;
  statusText: string;
  proto: string;
  headers: KeyValue[] | null;
  cookies?: Cookie[];
  body: string | null; // base64
  bodyTruncated?: boolean;
  size: number;
  timings: Timings;
  remoteAddr?: string;
}

export interface TestResult {
  name: string;
  passed: boolean;
  error?: string;
  source: "assertion" | "script";
}

export interface LogLine {
  level: string;
  message: string;
}

export interface SendResult {
  sent?: PyroRequest;
  response?: PyroResponse;
  unresolved?: string[];
  error?: string;
  startedAt: string;
  historyId?: number;
  tests?: TestResult[];
  logs?: LogLine[];
  scriptErrors?: string[];
  extracted?: Record<string, string>;
  problems?: string[];
  skipped?: boolean;
}

export interface SessionMessage {
  seq: number;
  at: string;
  direction: "sent" | "received" | "system";
  kind: string;
  data?: string;
  event?: string;
  ackId?: number;
  rttMs?: number;
  size: number;
}

export interface SessionState {
  id: string;
  path?: string;
  kind: Kind;
  info: { url: string; status?: number; headers?: KeyValue[]; subprotocol?: string; sid?: string };
  openedAt: string;
  closed: boolean;
  error?: string;
  messages: SessionMessage[];
}

export interface OpenResult {
  sent?: PyroRequest;
  unresolved?: string[];
  error?: string;
  session?: SessionState;
}

export interface RunItem {
  iteration: number;
  path: string;
  name: string;
  kind?: Kind;
  result?: SendResult;
  note?: string;
}

export interface RunView {
  id: string;
  path: string;
  status: "running" | "finished" | "cancelled";
  done: number;
  total: number;
  results: RunItem[];
  summary?: {
    iterations: number;
    requests: number;
    failed: number;
    testsPassed: number;
    testsFailed: number;
    durationMs: number;
    stopped?: string;
  };
}

export interface HistoryEntry {
  id: number;
  at: string;
  path?: string;
  name?: string;
  method: string;
  url: string;
  status?: number;
  durationMs: number;
  error?: string;
}

export interface ImportReport {
  format: string;
  collection?: string;
  slug?: string;
  requests: number;
  folders: number;
  environments?: string[];
  warnings: { path?: string; code: string; detail: string }[];
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

/** The session token, for the event WebSocket (which can't send headers). */
export const sessionToken = () => token;

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const headers: Record<string, string> = {};
  if (token) headers["X-Pyro-Token"] = token;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : raw ? (body as string) : JSON.stringify(body),
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

const enc = (p: string) => p.split("/").map(encodeURIComponent).join("/");

export const api = {
  health: () => call<{ status: string; version: string }>("GET", "/health"),
  workspace: () => call<WorkspaceInfo>("GET", "/workspace"),
  request: (path: string) => call<PyroRequest>("GET", `/requests/${enc(path)}`),
  saveRequest: (path: string, r: PyroRequest) => call<PyroRequest>("PUT", `/requests/${enc(path)}`, r),
  deleteRequest: (path: string) => call<void>("DELETE", `/requests/${enc(path)}`),
  environments: () => call<string[]>("GET", "/environments"),
  environment: (name: string) => call<Environment>("GET", `/environments/${encodeURIComponent(name)}`),
  send: (body: { path?: string; request?: PyroRequest; environment?: string }) => call<SendResult>("POST", "/send", body),
  history: (limit = 100) => call<HistoryEntry[]>("GET", `/history?limit=${limit}`),
  historyEntry: (id: number) => call<SendResult>("GET", `/history/${id}`),
  clearHistory: () => call<void>("DELETE", "/history"),
  importFile: (json: string) => call<ImportReport>("POST", "/import", json, true),
  openSession: (body: { path?: string; request?: PyroRequest; environment?: string }) => call<OpenResult>("POST", "/sessions", body),
  sessionState: (id: string) => call<SessionState>("GET", `/sessions/${id}`),
  sessionSend: (id: string, m: { data: string; binary?: boolean; event?: string; ack?: boolean }) =>
    call<{ message: SessionMessage; unresolved?: string[] }>("POST", `/sessions/${id}/send`, m),
  closeSession: (id: string) => call<void>("DELETE", `/sessions/${id}`),
  startRun: (body: { path: string; environment?: string; iterations?: number; data?: Record<string, string>[]; delayMs?: number; bail?: boolean }) =>
    call<RunView>("POST", "/runs", body),
  run: (id: string) => call<RunView>("GET", `/runs/${id}`),
  cancelRun: (id: string) => call<void>("DELETE", `/runs/${id}`),
};

/** Decodes a base64 response body into bytes. */
export function decodeBody(b64: string | null): Uint8Array {
  if (!b64) return new Uint8Array();
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
