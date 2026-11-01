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
  options?: Record<string, string>;
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

// --- Load tests ---------------------------------------------------------------

export interface LoadTestView {
  name: string;
  title?: string;
  yaml?: string;
  peakVUs?: number;
  error?: string;
}

export interface Latency {
  avg: number;
  p50: number;
  p90: number;
  p95: number;
  p99: number;
  max: number;
}

export interface ThresholdResult {
  metric: string;
  condition: string;
  actual: number;
  passed: boolean;
  noData?: boolean;
  abortOnFail?: boolean;
  breached?: boolean;
}

export interface LoadTick {
  elapsed: number;
  vus: number;
  vusMax: number;
  rps: number;
  iterationsPerSecond: number;
  errorRate: number;
  requests: number;
  dropped?: number;
  receivedPerSecond: number;
  wsMessagesPerSecond?: number;
  latency: Latency;
  thresholds?: ThresholdResult[];
}

export interface Stat {
  type: string;
  count: number;
  value: number;
  perSecond?: number;
  avg?: number;
  min?: number;
  med?: number;
  max?: number;
  p90?: number;
  p95?: number;
  p99?: number;
}

export interface RequestStat {
  scenario: string;
  request: string;
  method: string;
  count: number;
  failed: number;
  perSecond: number;
  duration: Stat;
}

export interface ErrorCount {
  message: string;
  count: number;
}

export interface LoadResult {
  name: string;
  status: "passed" | "failed" | "aborted" | "stopped";
  reason?: string;
  startedAt: string;
  duration: number;
  ticks: LoadTick[] | null;
  metrics: Record<string, Stat>;
  requests: RequestStat[] | null;
  thresholds: ThresholdResult[] | null;
  errors: ErrorCount[] | null;
  maxVUs: number;
}

export interface LiveSnapshot {
  tick: LoadTick;
  requests: RequestStat[] | null;
  errors: ErrorCount[] | null;
}

export interface LoadRunView {
  id: string;
  test: string;
  status: "running" | LoadResult["status"];
  ticks?: LoadTick[] | null;
  live?: LiveSnapshot;
  result?: LoadResult;
  plan?: { name?: string };
}

export interface LoadRunRow {
  id: string;
  test: string;
  name: string;
  startedAt: string;
  status: LoadRunView["status"];
  duration: number;
  requests: number;
  p95: number;
  errorRate: number;
  maxVUs: number;
}

// --- Plugins --------------------------------------------------------------------

export interface PluginField {
  key: string;
  label?: string;
  type?: "string" | "secret" | "number" | "boolean" | "select" | "text";
  options?: string[];
  default?: string;
  help?: string;
}

export interface PluginProvide {
  kind: "protocol" | "auth";
  id: string;
  title?: string;
  urlPlaceholder?: string;
  fields?: PluginField[];
}

export interface PluginInfo {
  name: string;
  version: string;
  abi: number;
  title?: string;
  description?: string;
  author?: string;
  license?: string;
  homepage?: string;
  module: string;
  provides: PluginProvide[];
  capabilities: { network?: string[] };
  config?: PluginField[] | null;
  enabled: boolean;
  approved: boolean;
  ready: boolean;
  error?: string;
  /** Saved values for the manifest's `config` fields. */
  settings?: Record<string, string>;
}

export interface PluginList {
  plugins: PluginInfo[];
  available: boolean;
  indexConfigured: boolean;
  dir?: string;
}

export interface IndexEntry {
  name: string;
  version: string;
  title?: string;
  description?: string;
  kinds?: string[];
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
    /** The parsed JSON error body, when there was one. */
    public body?: Record<string, unknown>,
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const headers: Record<string, string> = {};
  if (token) headers["X-Pyro-Token"] = token;
  if (body !== undefined) headers["Content-Type"] = body instanceof Blob ? "application/zip" : "application/json";
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : raw || body instanceof Blob ? (body as BodyInit) : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    let body: Record<string, unknown> | undefined;
    try {
      body = await res.json();
      msg = (body?.error as string) ?? msg;
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, msg, body);
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
  loadTests: () => call<LoadTestView[]>("GET", "/loadtests"),
  loadTest: (name: string) => call<LoadTestView>("GET", `/loadtests/${encodeURIComponent(name)}`),
  saveLoadTest: (name: string, yaml: string) => call<LoadTestView>("PUT", `/loadtests/${encodeURIComponent(name)}`, { yaml }),
  deleteLoadTest: (name: string) => call<void>("DELETE", `/loadtests/${encodeURIComponent(name)}`),
  startLoadTest: (name: string, body: { environment?: string; confirmHosts?: boolean }) =>
    call<{ id: string; test: string }>("POST", `/loadtests/${encodeURIComponent(name)}/start`, body),
  plugins: () => call<PluginList>("GET", "/plugins"),
  installPlugin: (archive: Blob) => call<PluginInfo>("POST", "/plugins", archive),
  approvePlugin: (name: string) => call<PluginInfo>("POST", `/plugins/${encodeURIComponent(name)}/approve`),
  enablePlugin: (name: string, enabled: boolean) => call<PluginInfo>("POST", `/plugins/${encodeURIComponent(name)}/enabled`, { enabled }),
  configurePlugin: (name: string, config: Record<string, string>) => call<PluginInfo>("PUT", `/plugins/${encodeURIComponent(name)}/config`, { config }),
  removePlugin: (name: string) => call<void>("DELETE", `/plugins/${encodeURIComponent(name)}`),
  searchPlugins: (q: string) => call<IndexEntry[]>("GET", `/plugins/index?q=${encodeURIComponent(q)}`),
  installFromIndex: (name: string, version?: string) => call<PluginInfo>("POST", "/plugins/index/install", { name, version }),
  loadRuns: (test?: string) => call<LoadRunRow[]>("GET", `/loadruns${test ? `?test=${encodeURIComponent(test)}` : ""}`),
  loadRun: (id: string) => call<LoadRunView>("GET", `/loadruns/${id}`),
  stopLoadRun: (id: string, abort = false) => call<void>("POST", `/loadruns/${id}/stop${abort ? "?abort=1" : ""}`),
  deleteLoadRun: (id: string) => call<void>("DELETE", `/loadruns/${id}`),
  /** Fetches the HTML report (the token can't ride on a plain link). */
  loadReport: async (id: string) => {
    const res = await fetch(`/api/v1/loadruns/${id}/report`, { headers: token ? { "X-Pyro-Token": token } : {} });
    if (!res.ok) throw new ApiError(res.status, (await res.json().catch(() => ({}))).error ?? res.statusText);
    return res.text();
  },
};

/** Decodes a base64 response body into bytes. */
export function decodeBody(b64: string | null): Uint8Array {
  if (!b64) return new Uint8Array();
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
