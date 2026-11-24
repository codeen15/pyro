// Redis over RESP: the body is one command line (SET k v), the reply is the
// response body. A JavaScript example of a protocol plugin.
//
// Replies:
//   +simple string   status 0, body = the text
//   -error           status 1, statusText "ERR", body = the error text
//   :integer         status 0, body = the number
//   $bulk string     status 0, body = the text ($-1, a null, gives an empty
//                    body and statusText "nil")
//   *array           status 0, body = JSON (nulls are null, errors are strings)

// splitCommand splits a command line on whitespace. A double-quoted argument
// may contain spaces; inside quotes \" and \\ are escapes.
function splitCommand(line) {
  const args = []
  let cur = ""
  let inArg = false
  let quoted = false
  for (let i = 0; i < line.length; i++) {
    const c = line[i]
    if (quoted) {
      if (c === "\\" && (line[i + 1] === '"' || line[i + 1] === "\\")) {
        cur += line[++i]
      } else if (c === '"') {
        quoted = false
      } else {
        cur += c
      }
    } else if (c === '"') {
      quoted = true
      inArg = true
    } else if (/\s/.test(c)) {
      if (inArg) {
        args.push(cur)
        cur = ""
        inArg = false
      }
    } else {
      cur += c
      inArg = true
    }
  }
  if (quoted) throw new Error("unterminated quote in command")
  if (inArg) args.push(cur)
  return args
}

// encodeCommand writes args as a RESP array of bulk strings.
function encodeCommand(args) {
  const parts = ["*" + args.length + "\r\n"]
  for (const a of args) {
    const bytes = pyro.encoding.utf8.encode(a)
    parts.push("$" + bytes.length + "\r\n", a, "\r\n")
  }
  return parts.join("")
}

// Reader buffers what the connection delivers and parses RESP replies from
// it, reading more whenever a reply is incomplete.
class Reader {
  constructor(conn, timeoutMs) {
    this.conn = conn
    this.timeoutMs = timeoutMs
    this.buf = new Uint8Array(0)
    this.pos = 0
  }

  fill() {
    const chunk = this.conn.read({ timeoutMs: this.timeoutMs })
    if (chunk === null) throw new Error("connection closed before the reply was complete")
    const rest = this.buf.subarray(this.pos)
    const next = new Uint8Array(rest.length + chunk.length)
    next.set(rest, 0)
    next.set(chunk, rest.length)
    this.buf = next
    this.pos = 0
  }

  // line returns the next line without its CRLF.
  line() {
    for (;;) {
      for (let i = this.pos; i + 1 < this.buf.length; i++) {
        if (this.buf[i] === 13 && this.buf[i + 1] === 10) {
          const text = pyro.encoding.utf8.decode(this.buf.subarray(this.pos, i))
          this.pos = i + 2
          return text
        }
      }
      this.fill()
    }
  }

  // bytes returns the next n bytes and skips the CRLF after them.
  bytes(n) {
    while (this.buf.length - this.pos < n + 2) this.fill()
    const out = this.buf.slice(this.pos, this.pos + n)
    this.pos += n + 2
    return out
  }

  // reply parses one value: {type, value} where type is "simple", "error",
  // "int", "bulk" or "array" and a null bulk or array has value null.
  reply() {
    const line = this.line()
    const kind = line[0]
    const rest = line.slice(1)
    switch (kind) {
      case "+":
        return { type: "simple", value: rest }
      case "-":
        return { type: "error", value: rest }
      case ":":
        return { type: "int", value: parseInt(rest, 10) }
      case "$": {
        const n = parseInt(rest, 10)
        if (n < 0) return { type: "bulk", value: null }
        return { type: "bulk", value: pyro.encoding.utf8.decode(this.bytes(n)) }
      }
      case "*": {
        const n = parseInt(rest, 10)
        if (n < 0) return { type: "array", value: null }
        const items = []
        for (let i = 0; i < n; i++) items.push(this.reply())
        return { type: "array", value: items }
      }
      default:
        throw new Error("unexpected reply: " + line)
    }
  }
}

// plain turns a parsed reply into a JSON-friendly value.
function plain(r) {
  if (r.type === "array") return r.value === null ? null : r.value.map(plain)
  return r.value
}

// hostPort takes "redis://host[:port]" (or just "host[:port]") and adds the
// default port.
function hostPort(url) {
  const addr = url.replace(/^[a-z]+:\/\//i, "").replace(/[/?].*$/, "").replace(/^[^@]*@/, "")
  return /:\d+$/.test(addr) ? addr : addr + ":6379"
}

function toResponse(r) {
  switch (r.type) {
    case "error":
      return { status: 1, statusText: "ERR", body: r.value }
    case "int":
      return { status: 0, statusText: "OK", body: String(r.value) }
    case "simple":
      return { status: 0, statusText: "OK", body: r.value }
    case "bulk":
      if (r.value === null) return { status: 0, statusText: "nil", body: "" }
      return { status: 0, statusText: "OK", body: r.value }
    default:
      if (r.value === null) return { status: 0, statusText: "nil", body: "" }
      return { status: 0, statusText: "OK", body: JSON.stringify(plain(r)) }
  }
}

pyro.registerProtocol("redis", (ctx, req) => {
  const args = splitCommand(req.body)
  if (args.length === 0) throw new Error("the body must be a Redis command, for example PING or SET k v")
  const timeoutMs = parseInt(req.option("timeoutMs", "5000"), 10) || 5000
  const db = parseInt(req.option("db", "0"), 10) || 0
  const addr = hostPort(req.url)

  const conn = pyro.dial(addr, { timeoutMs })
  try {
    const reader = new Reader(conn, timeoutMs)
    if (db !== 0) {
      conn.write(encodeCommand(["SELECT", String(db)]))
      const sel = reader.reply()
      if (sel.type === "error") return toResponse(sel)
    }
    conn.write(encodeCommand(args))
    const res = toResponse(reader.reply())
    res.remoteAddr = addr
    return res
  } finally {
    conn.close()
  }
})
