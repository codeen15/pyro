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
  /** Older form of post-processors; still run, after `post`. */
  assertions?: Assertion[];
  extract?: Extractor[];
  /** Think time before the request in load tests; unset inherits the folder/collection's. */
  think?: Timer;
  pre?: Processor[];
  post?: Processor[];
  messages?: SavedMessage[];
  socketio?: SocketIOConfig;
  examples?: Example[];
}

/** A saved response. */
export interface Example {
  name: string;
  status?: number;
  headers?: KeyValue[];
  body?: string;
}

export interface Assertion {
  type: string;
  path?: string;
  op?: string;
  value?: string;
}

export interface Extractor {
  var: string;
  /** jsonpath | header | regex | status | boundary | xpath */
  from: string;
  path?: string;
  /** boundary: text between left and right. */
  left?: string;
  right?: string;
}

/** JMeter-style timer. delay is the constant part; range the random part
 * (uniform: +0..range, gaussian: ±deviation range, poisson: mean range).
 * Durations like "300ms". type "none" turns off an inherited timer. */
export interface Timer {
  type: "constant" | "uniform" | "gaussian" | "poisson" | "none";
  delay?: string;
  range?: string;
}

/** A pre or post processor. Pre: script | vars | counter | plugin:<id>.
 * Post: script | extract | assert | vars | plugin:<id>. */
export interface Processor {
  type: string;
  name?: string;
  disabled?: boolean;
  script?: string;
  vars?: KeyValue[];
  counter?: Counter;
  extract?: Extractor;
  assert?: Assertion;
  options?: Record<string, string>;
}

export interface Counter {
  var: string;
  start?: number;
  step?: number;
  end?: number;
  /** printf-style, e.g. "%05d" */
  format?: string;
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
  transport?: "auto" | "websocket" | "polling";
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
  info: { url: string; status?: number; headers?: KeyValue[]; subprotocol?: string; sid?: string; transport?: string };
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
  /** Requests were added to an existing collection or folder (slug). */
  into?: boolean;
  /** Load tests created (JMeter imports). */
  loadTests?: string[];
  requests: number;
  folders: number;
  environments?: string[];
  warnings: { path?: string; code: string; detail: string }[];
}

export interface StoredCookie {
  name: string;
  value: string;
  domain: string;
  path: string;
  hostOnly?: boolean;
  secure?: boolean;
  httpOnly?: boolean;
  sameSite?: string;
  expires?: string;
  created: string;
}

export interface OAuthToken {
  accessToken: string;
  tokenType?: string;
  refreshToken?: string;
  scope?: string;
  expiresAt?: string;
  obtainedAt: string;
}

export interface OAuthStatus {
  /** False when the request's auth (own or inherited) isn't OAuth 2.0. */
  oauth: boolean;
  grant: string;
  token?: OAuthToken;
  expired?: boolean;
  unresolved?: string[];
  interactive?: boolean;
  inherited?: boolean;
  redirectUri: string;
}

/** Session ("current") values layered over the files; null means unset this session. */
export interface SessionVariables {
  runtime: Record<string, string | null>;
  globals: Record<string, string | null>;
  environments: Record<string, Record<string, string | null>>;
  collections: Record<string, Record<string, string | null>>;
}

/** collection.yaml / folder.yaml: settings their requests inherit. */
export interface CollectionSettings {
  name: string;
  description?: string;
  variables?: Variable[];
  auth?: Auth;
  scripts?: { pre?: string; post?: string };
  think?: Timer;
  pre?: Processor[];
  post?: Processor[];
  order?: string[];
}

export interface MockRoute {
  method: string;
  path: string;
  request: string;
  name: string;
  examples: Example[];
}

export interface MockHit {
  at: string;
  method: string;
  path: string;
  status: number;
  example?: string;
  request?: string;
}

export interface MockView {
  collection: string;
  running: boolean;
  url: string;
  routes?: MockRoute[];
  hits?: MockHit[];
}

export interface ExportView {
  filename: string;
  content: unknown;
  report?: ImportReport;
}

/** Offers text as a file download. */
export function saveFile(filename: string, text: string, type = "application/json") {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// --- Load tests ---------------------------------------------------------------

/** How a data file becomes variables (JMeter's CSV Data Set Config). */
export interface DataFormat {
  /** First line holds column names (default: yes, unless variables are given). */
  header?: boolean;
  /** Names for the columns, in order, for files without a header line. */
  variables?: string[];
  /** Column → variable; "" leaves a column out. */
  map?: Record<string, string>;
  /** One character, or "tab". */
  delimiter?: string;
}

export interface DataPreview {
  columns: string[];
  variables: string[];
  rows: Record<string, string>[];
  total: number;
}

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
  /** This load generator's CPU use (0..1 of its cores; -1 if unknown) and memory. */
  cpu: number;
  memMB: number;
  agents?: AgentStatus[];
}

/** One agent's part of a distributed load test. */
export interface AgentStatus {
  name: string;
  segment: number;
  vus: number;
  requests: number;
  rps?: number;
  state: "running" | "done" | "failed";
  error?: string;
  cpu: number;
  peakCPU: number;
  memMB: number;
}

export interface AgentInfo {
  name: string;
  version: string;
  protocol: number;
  cpus: number;
  os: string;
  arch: string;
  busy: boolean;
  maxVUs: number;
  plugins?: string[];
}

export interface AgentView {
  address: string;
  info?: AgentInfo;
  error?: string;
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
  maxRPS?: number;
  agents?: AgentStatus[];
  warnings?: string[];
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
  kind: "protocol" | "auth" | "processor";
  id: string;
  title?: string;
  /** processor only: where it can run (default both). */
  phases?: ("pre" | "post")[];
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
  saveEnvironment: (name: string, env: Environment) => call<Environment>("PUT", `/environments/${encodeURIComponent(name)}`, env),
  deleteEnvironment: (name: string) => call<void>("DELETE", `/environments/${encodeURIComponent(name)}`),
  exportEnvironment: (name: string) => call<ExportView>("GET", `/environments/${encodeURIComponent(name)}/export`),
  oauthToken: (body: { path?: string; request?: PyroRequest; environment?: string; fetch?: boolean; clear?: boolean }) =>
    call<OAuthStatus>("POST", "/oauth2/token", body),
  oauthAuthorize: (body: { path?: string; request?: PyroRequest; environment?: string }) =>
    call<{ url: string; redirectUri: string }>("POST", "/oauth2/authorize", body),
  cookies: () => call<StoredCookie[]>("GET", "/cookies"),
  putCookie: (c: Partial<StoredCookie> & { domain: string; name: string }) => call<StoredCookie[]>("PUT", "/cookies", c),
  deleteCookies: (f: { domain?: string; path?: string; name?: string } = {}) =>
    call<{ deleted: number }>("DELETE", `/cookies?${new URLSearchParams(Object.entries(f).filter(([, v]) => v) as [string, string][])}`),
  globals: () => call<Environment>("GET", "/globals"),
  saveGlobals: (env: Environment) => call<Environment>("PUT", "/globals", env),
  sessionVariables: () => call<SessionVariables>("GET", "/session/variables"),
  setCurrentValue: (body: { scope: "globals" | "environment" | "collection"; name?: string; key: string; value?: string | null; reset?: boolean }) =>
    call<void>("PATCH", "/session/variables", body),
  send: (body: { path?: string; request?: PyroRequest; environment?: string }) => call<SendResult>("POST", "/send", body),
  history: (limit = 100) => call<HistoryEntry[]>("GET", `/history?limit=${limit}`),
  historyEntry: (id: number) => call<SendResult>("GET", `/history/${id}`),
  clearHistory: () => call<void>("DELETE", "/history"),
  importFile: (text: string, opts: { filename?: string; into?: string } = {}) => {
    const q = new URLSearchParams();
    if (opts.filename) q.set("filename", opts.filename);
    if (opts.into) q.set("into", opts.into);
    return call<ImportReport>("POST", `/import${q.size ? `?${q}` : ""}`, text, true);
  },
  collection: (slug: string) => call<CollectionSettings>("GET", `/collections/${encodeURIComponent(slug)}`),
  saveCollection: (slug: string, c: CollectionSettings) => call<CollectionSettings>("PUT", `/collections/${encodeURIComponent(slug)}`, c),
  createCollection: (slug: string, name: string) => call<CollectionSettings>("POST", "/collections", { slug, name }),
  folder: (path: string) => call<CollectionSettings>("GET", `/folders/${enc(path)}`),
  saveFolder: (path: string, c: CollectionSettings) => call<CollectionSettings>("PUT", `/folders/${enc(path)}`, c),
  createFolder: (path: string, name: string) => call<CollectionSettings>("POST", `/folders/${enc(path)}`, { name }),
  deleteFolder: (path: string) => call<void>("DELETE", `/folders/${enc(path)}`),
  deleteCollection: (slug: string) => call<void>("DELETE", `/collections/${encodeURIComponent(slug)}`),
  exportCollection: (slug: string) => call<ExportView>("GET", `/collections/${encodeURIComponent(slug)}/export`),
  openSession: (body: { path?: string; request?: PyroRequest; environment?: string }) => call<OpenResult>("POST", "/sessions", body),
  sessionState: (id: string) => call<SessionState>("GET", `/sessions/${id}`),
  sessionSend: (id: string, m: { data: string; binary?: boolean; event?: string; ack?: boolean }) =>
    call<{ message: SessionMessage; unresolved?: string[] }>("POST", `/sessions/${id}/send`, m),
  closeSession: (id: string) => call<void>("DELETE", `/sessions/${id}`),
  startRun: (body: { path: string; environment?: string; iterations?: number; data?: Record<string, string>[]; delayMs?: number; bail?: boolean; think?: boolean }) =>
    call<RunView>("POST", "/runs", body),
  run: (id: string) => call<RunView>("GET", `/runs/${id}`),
  cancelRun: (id: string) => call<void>("DELETE", `/runs/${id}`),
  loadTests: () => call<LoadTestView[]>("GET", "/loadtests"),
  loadTest: (name: string) => call<LoadTestView>("GET", `/loadtests/${encodeURIComponent(name)}`),
  saveLoadTest: (name: string, yaml: string) => call<LoadTestView>("PUT", `/loadtests/${encodeURIComponent(name)}`, { yaml }),
  deleteLoadTest: (name: string) => call<void>("DELETE", `/loadtests/${encodeURIComponent(name)}`),
  dataFiles: () => call<{ path: string; size: number }[]>("GET", "/data"),
  uploadData: (name: string, text: string) => call<{ path: string; size: number }>("PUT", `/data/${enc(name)}`, text, true),
  previewData: (body: { file: string; limit?: number } & DataFormat) => call<DataPreview>("POST", "/data/preview", body),
  agents: () => call<AgentView[]>("GET", "/agents"),
  startLoadTest: (name: string, body: { environment?: string; confirmHosts?: boolean; distributed?: boolean }) =>
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
  /** Fetches a collection's generated HTML API reference. */
  collectionDocs: async (slug: string) => {
    const res = await fetch(`/api/v1/collections/${encodeURIComponent(slug)}/docs`, { headers: token ? { "X-Pyro-Token": token } : {} });
    if (!res.ok) throw new ApiError(res.status, (await res.json().catch(() => ({}))).error ?? res.statusText);
    return res.text();
  },
  mocks: () => call<MockView[]>("GET", "/mocks"),
  mock: (slug: string) => call<MockView>("GET", `/mocks/${encodeURIComponent(slug)}`),
  startMock: (collection: string, opts: { port?: number; delayMs?: number } = {}) => call<MockView>("POST", "/mocks", { collection, ...opts }),
  stopMock: (slug: string) => call<void>("DELETE", `/mocks/${encodeURIComponent(slug)}`),
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
