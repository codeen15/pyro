// Types for the `pyro` global that Pyro gives a JavaScript plugin (runtime: js).
// Written by `pyro plugin new --lang js`; it declares the API of the Pyro
// version that wrote it. There is no module system: plugin.js is one script.
// If you write TypeScript, compile it to a single plugin.js.

/** A header or query parameter, as on the wire. */
interface PyroKeyValue {
  key: string
  value: string
  disabled?: boolean
}

/** Data you hand to Pyro: a string is sent as UTF-8. */
type PyroData = string | Uint8Array | ArrayBuffer

/** The plugin's settings, as strings. */
interface PyroContext {
  /** Settings the user saved for the plugin as a whole. */
  config: Record<string, string>
  /** The fields of this request type, auth type or processor (the manifest's `fields`). */
  params: Record<string, string>
}

interface PyroRequest {
  kind: string
  name: string
  method: string
  url: string
  headers: PyroKeyValue[]
  query: PyroKeyValue[]
  body: string
  /** The values of the request type's manifest `fields`. */
  options: Record<string, string>
  /** options[key], or `def` (default "") when it is missing or empty. */
  option(key: string, def?: string): string
  /** The first enabled header with this name (case-insensitive), or "". */
  header(name: string): string
  /** Replaces the header with this name (case-insensitive) or adds it. */
  setHeader(name: string, value: string): void
}

/** What a protocol handler returns. `undefined` means an empty response. */
interface PyroResponse {
  status?: number
  statusText?: string
  /** `[{key, value}]` or `{name: value | value[]}`. */
  headers?: PyroKeyValue[] | Record<string, string | string[]>
  body?: string | Uint8Array | ArrayBuffer
  remoteAddr?: string
}

/** The response a post-phase processor sees; `null` in the pre phase. */
interface PyroProcessResponse {
  status: number
  statusText: string
  headers: PyroKeyValue[]
  body: string
  bodyBytes: Uint8Array
  timeMs: number
}

interface PyroProcess {
  phase: "pre" | "post"
  /** The request; a processor may change it (header, url, body, ...). */
  request: PyroRequest
  response: PyroProcessResponse | null
  /** The variables when the processor started; setVar() updates it too. */
  vars: Record<string, string>
  setVar(name: string, value: string): void
  /** Reports a test result; `message` explains a failure. */
  test(name: string, passed: boolean, message?: string): void
}

interface PyroConn {
  /** Returns the number of bytes written. */
  write(data: PyroData): number
  /**
   * Reads what is available (up to `max` bytes, default 65536). Returns null
   * at end of stream; throws when the timeout (default 10000 ms) passes.
   */
  read(options?: { timeoutMs?: number; max?: number }): Uint8Array | null
  close(): void
}

interface PyroCodec {
  encode(data: PyroData): string
  decode(text: string): Uint8Array
}

interface PyroLog {
  debug(...args: unknown[]): void
  info(...args: unknown[]): void
  warn(...args: unknown[]): void
  error(...args: unknown[]): void
}

/** A handler may be async, as long as it awaits nothing that needs real I/O. */
type PyroResult<T> = T | Promise<T>

interface Pyro {
  /** Adds a request type, used as `plugin:<id>`. Throw to fail the request. */
  registerProtocol(id: string, handler: (ctx: PyroContext, req: PyroRequest) => PyroResult<PyroResponse | void>): void
  /** Adds an auth type. Change `req` (usually setHeader) to sign it. */
  registerAuth(id: string, handler: (ctx: PyroContext, req: PyroRequest) => PyroResult<void>): void
  /** Adds a pre/post processor. */
  registerProcessor(id: string, handler: (ctx: PyroContext, p: PyroProcess) => PyroResult<void>): void

  /**
   * Opens a TCP connection to "host:port". Only addresses the manifest's
   * `capabilities.network` lists are allowed; others throw.
   */
  dial(addr: string, options?: { tls?: boolean; timeoutMs?: number }): PyroConn

  log: PyroLog

  encoding: {
    utf8: {
      encode(text: string): Uint8Array
      decode(data: PyroData): string
    }
    base64: PyroCodec
    hex: PyroCodec
  }

  crypto: {
    sha256(data: PyroData): Uint8Array
    hmacSHA256(key: PyroData, data: PyroData): Uint8Array
  }
}

declare const pyro: Pyro
