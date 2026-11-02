// Starts everything the end-to-end tests need and stays up until killed:
// a mock API on :7891, and `pyro serve` on :7890 with a fresh workspace
// (its own HOME, so history and plugins are isolated). Playwright runs this
// as its webServer.
import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const TARGET = 7891;
const PYRO = 7890;

createServer((req, res) => {
  const url = new URL(req.url, "http://x");
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    res.setHeader("Content-Type", "application/json");
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
}).listen(TARGET, "127.0.0.1");

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
pyro.on("exit", (code) => process.exit(code ?? 1));
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => pyro.kill("SIGINT"));
