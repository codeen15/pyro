// Starts everything the end-to-end tests need and stays up until killed:
// a mock API on :7891, `pyro serve` on :7890 with a fresh workspace (its own
// HOME, so history and plugins are isolated), and a team server on :7892
// with an empty data directory. Playwright runs this as its webServer.
import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const TARGET = 7891;
const PYRO = 7890;
const TEAM = 7892;

// A tiny WebSocket chat at /chat (text frames, ping and close only): a
// {"type":"join","token":…} message is answered with a welcome that echoes
// the token, and POST /notify pushes its body to every socket.
const sockets = new Set();
const frame = (op, payload) => {
  const len = payload.length;
  const head = len < 126 ? Buffer.from([0x80 | op, len]) : Buffer.from([0x80 | op, 126, len >> 8, len & 255]);
  return Buffer.concat([head, payload]);
};
const onUpgrade = (req, sock) => {
  if (new URL(req.url, "http://x").pathname !== "/chat") return sock.destroy();
  const accept = createHash("sha1").update(req.headers["sec-websocket-key"] + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
  sock.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
  sockets.add(sock);
  let buf = Buffer.alloc(0);
  sock.on("data", (d) => {
    buf = Buffer.concat([buf, d]);
    while (buf.length >= 2) {
      const op = buf[0] & 15;
      let len = buf[1] & 127;
      let off = 2;
      if (len === 126) {
        if (buf.length < 4) return;
        len = buf.readUInt16BE(2);
        off = 4;
      }
      if (buf.length < off + 4 + len) return;
      const mask = buf.subarray(off, off + 4);
      const data = Buffer.from(buf.subarray(off + 4, off + 4 + len).map((b, i) => b ^ mask[i % 4]));
      buf = buf.subarray(off + 4 + len);
      if (op === 9) sock.write(frame(10, data));
      else if (op === 8) {
        sock.end(frame(8, data));
        sockets.delete(sock);
      } else if (op === 1) {
        try {
          const m = JSON.parse(data.toString());
          if (m.type === "join") sock.write(frame(1, Buffer.from(JSON.stringify({ type: "welcome", token: m.token }))));
        } catch {
          /* not JSON: ignore */
        }
      }
    }
  });
  sock.on("close", () => sockets.delete(sock));
  sock.on("error", () => sockets.delete(sock));
};

createServer((req, res) => {
  const url = new URL(req.url, "http://x");
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    res.setHeader("Content-Type", "application/json");
    if (url.pathname === "/notify" && req.method === "POST") {
      for (const s of sockets) s.write(frame(1, Buffer.from(body)));
      res.end(JSON.stringify({ pushed: sockets.size }));
      return;
    }
    if (url.pathname === "/orders" && req.method === "POST") {
      res.statusCode = 201;
      res.end(JSON.stringify({ id: "ord_1", received: body ? JSON.parse(body) : null }));
      return;
    }
    // A tiny OAuth 2.0 provider: /authorize approves at once and redirects
    // back with a code; /token issues tokens for any client.
    if (url.pathname === "/authorize") {
      const back = new URL(url.searchParams.get("redirect_uri"));
      back.searchParams.set("code", "c0de");
      back.searchParams.set("state", url.searchParams.get("state"));
      res.statusCode = 302;
      res.setHeader("Location", back.toString());
      res.end();
      return;
    }
    if (url.pathname === "/token") {
      const form = new URLSearchParams(body);
      const grant = form.get("grant_type");
      if (grant === "authorization_code" && !form.get("code_verifier")) {
        res.statusCode = 400;
        res.end(JSON.stringify({ error: "invalid_request", error_description: "PKCE verifier missing" }));
        return;
      }
      res.end(JSON.stringify({ access_token: `${grant}-token`, token_type: "Bearer", expires_in: 3600, scope: form.get("scope") ?? "" }));
      return;
    }
    if (url.pathname === "/login") {
      res.setHeader("Set-Cookie", "sid=abc123; Path=/; HttpOnly");
      res.end(JSON.stringify({ ok: true }));
      return;
    }
    if (url.pathname === "/whoami") {
      res.end(JSON.stringify({ authorization: req.headers.authorization ?? null }));
      return;
    }
    if (url.pathname === "/fail") {
      res.statusCode = 503;
      res.end(JSON.stringify({ error: "down" }));
      return;
    }
    res.end(JSON.stringify({ ok: true, path: url.pathname, query: Object.fromEntries(url.searchParams) }));
  });
})
  .on("upgrade", onUpgrade)
  .listen(TARGET, "127.0.0.1");

const root = mkdtempSync(join(tmpdir(), "pyro-e2e-"));
const ws = join(root, "ws");
const write = (p, s) => {
  mkdirSync(join(ws, p, ".."), { recursive: true });
  writeFileSync(join(ws, p), s);
};
write("pyro.yaml", "name: E2E\nversion: 1\n");
write("collections/shop/collection.yaml", `name: Shop\nvariables:\n  - { key: base, value: "http://127.0.0.1:${TARGET}" }\n`);
write(
  "collections/shop/list-items.req.yaml",
  `kind: http\nname: List items\nmethod: GET\nurl: "{{base}}/items"\nquery:\n  - { key: page, value: "2" }\nassertions:\n  - { type: status, value: "200" }\n  - { type: jsonpath, path: "$.query.page", value: "2" }\nexamples:\n  - { name: Two items, status: 200, headers: [ { key: Content-Type, value: application/json } ], body: '[{"id":1},{"id":2}]' }\n`,
);
write(
  "collections/shop/create-order.req.yaml",
  `kind: http\nname: Create order\nmethod: POST\nurl: "{{base}}/orders"\nbody: { type: json, content: '{"sku":"A-1"}' }\nassertions:\n  - { type: status, value: "201" }\nextract:\n  - { var: orderId, from: jsonpath, path: "$.id" }\n`,
);
write("collections/chat/collection.yaml", `name: Chat\nvariables:\n  - { key: base, value: "http://127.0.0.1:${TARGET}" }\n`);
write("collections/chat/open.req.yaml", `kind: websocket\nname: Open chat\nconnection: chat\nurl: "ws://127.0.0.1:${TARGET}/chat"\n`);
write("collections/chat/order.req.yaml", `kind: http\nname: Make order\nmethod: POST\nurl: "{{base}}/orders"\nextract:\n  - { var: orderId, from: jsonpath, path: "$.id" }\n`);
write(
  "collections/chat/join.req.yaml",
  `kind: websocket\nname: Join with order\naction: send\nconnection: chat\nmessages:\n  - { content: '{"type":"join","token":"{{orderId}}"}' }\nwait: { for: message, jsonpath: "$.type", equals: welcome, timeout: 5s }\nassertions:\n  - { type: jsonpath, path: "$.token", value: "ord_1" }\n`,
);
write("collections/chat/close.req.yaml", `kind: websocket\nname: Leave chat\naction: close\nconnection: chat\ncloseReason: bye\n`);
write("collections/account/collection.yaml", "name: Account\n");
write("collections/account/login.req.yaml", `kind: http\nname: Log in\nmethod: POST\nurl: "http://127.0.0.1:${TARGET}/login"\n`);
write("collections/shop/whoami.req.yaml", `kind: http\nname: Who am I\nmethod: GET\nurl: "{{base}}/whoami"\n`);
write(
  "loadtests/smoke.load.yaml",
  "name: Smoke\nscenarios:\n  s:\n    executor: constant-vus\n    vus: 3\n    duration: 3s\n    flow: [ { request: shop/list-items }, { think: 50ms } ]\nthresholds:\n  http_req_failed: [\"rate<0.01\"]\n",
);

const bin = resolve(process.env.PYRO_BIN ?? "../bin/pyro");
const pyro = spawn(bin, ["serve", "--port", String(PYRO), "--no-token", "-w", ws], {
  env: { ...process.env, HOME: join(root, "home"), PYRO_PLUGINS_DIR: join(root, "plugins") },
  stdio: "inherit",
});
const teamServer = spawn(bin, ["serve", "--team", "--port", String(TEAM), "--data", join(root, "team"), "--signup", "open"], {
  env: { ...process.env, HOME: join(root, "home"), PYRO_PLUGINS_DIR: join(root, "plugins") },
  stdio: "inherit",
});
pyro.on("exit", (code) => process.exit(code ?? 1));
teamServer.on("exit", (code) => process.exit(code ?? 1));
for (const sig of ["SIGINT", "SIGTERM"])
  process.on(sig, () => {
    pyro.kill("SIGINT");
    teamServer.kill("SIGINT");
  });
