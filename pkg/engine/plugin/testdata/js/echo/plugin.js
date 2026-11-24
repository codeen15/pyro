// Test plugin: a line protocol over TCP, an HMAC auth and a processor.

pyro.registerProtocol("js-echo", (ctx, req) => {
  const addr = req.url.replace(/^[a-z]+:\/\//, "")
  pyro.log.info("dialing " + addr)
  const conn = pyro.dial(addr, { timeoutMs: 5000 })
  try {
    conn.write(req.body + "\n")
    let line = ""
    while (!line.includes("\n")) {
      const chunk = conn.read({ timeoutMs: 5000 })
      if (chunk === null) break
      line += pyro.encoding.utf8.decode(chunk)
    }
    return { status: 0, statusText: "OK", body: line.trim() }
  } finally {
    conn.close()
  }
})

// js-sign sets X-Sig to the hex HMAC-SHA256 of the body, keyed with "secret".
pyro.registerAuth("js-sign", (ctx, req) => {
  const sig = pyro.crypto.hmacSHA256("secret", req.body)
  req.setHeader("X-Sig", pyro.encoding.hex.encode(sig))
})

pyro.registerProcessor("js-check", (ctx, p) => {
  if (p.phase === "post") {
    const ok = p.response.status >= 200 && p.response.status < 300
    p.test("status ok", ok, ok ? "" : "status " + p.response.status)
  }
  p.setVar("seen", "1")
})
