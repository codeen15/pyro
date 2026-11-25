import { spawn, type ChildProcess } from "node:child_process";
import { resolve } from "node:path";
import { test, expect } from "./fixtures";

// Admin → Agents on the team server (:7892, see server.mjs). It runs after
// team.spec.ts (hence the file name), whose first account is the administrator;
// run alone, it creates that account itself.
const TEAM = "http://127.0.0.1:7892";
const PASSWORD = "correct horse battery";
const bin = resolve(process.env.PYRO_BIN ?? "../bin/pyro");

test("admin adds a connecting agent, sees it connect, and removing it disconnects it", async ({ page, request }) => {
  await expect
    .poll(async () => (await request.get(`${TEAM}/api/v1/health`).catch(() => null))?.status() ?? 0, { timeout: 15_000 })
    .toBe(200);

  // Sign in as the administrator (the first account), creating it if the server is fresh.
  const headers = { "X-Pyro-CSRF": "1" };
  const login = await page.request.post(`${TEAM}/api/v1/auth/login`, { headers, data: { email: "owner@example.com", password: PASSWORD } });
  if (login.status() !== 200) {
    const up = await page.request.post(`${TEAM}/api/v1/auth/signup`, { headers, data: { email: "owner@example.com", name: "Olivia Owner", password: PASSWORD } });
    expect(up.status()).toBeLessThan(300);
  }
  await page.goto(TEAM);
  await page.getByRole("button", { name: "Account menu for Olivia Owner" }).click();
  await page.getByRole("menuitem", { name: "Administration" }).click();
  await expect(page.getByRole("heading", { name: "Administration" })).toBeVisible();
  await page.getByRole("tab", { name: /^Agents/ }).click();

  // Add an agent that connects in.
  await page.getByRole("button", { name: "Add agent" }).click();
  await page.getByRole("menuitem", { name: "Connects in…" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill("e2e-agent");
  await dialog.getByRole("button", { name: "Create agent" }).click();

  // The command is shown once, with the token.
  const token = await dialog.getByLabel("Agent token", { exact: true }).inputValue();
  expect(token).toMatch(/^pyro_agent_/);
  const docker = dialog.getByLabel("Docker command", { exact: true });
  await expect(docker).toContainText(`docker run -d --name pyro-agent --restart unless-stopped ghcr.io/codeen15/pyro:`);
  await expect(docker).toContainText(`agent --connect '${TEAM}' --token '${token}'`);
  await expect(dialog.getByText("Waiting for the agent to connect")).toBeVisible();
  // The token is shown once: Esc doesn't close the dialog by accident.
  await page.keyboard.press("Escape");
  await expect(dialog.getByLabel("Agent token", { exact: true })).toBeVisible();

  // Editing the address rebuilds the commands with the same shell quoting as the server.
  const address = dialog.getByLabel("Server address");
  await address.fill("http://10.0.0.1:7878");
  await expect(docker).toContainText(`agent --connect 'http://10.0.0.1:7878' --token '${token}'`);
  // A plain-HTTP server address is marked as not encrypted; https:// isn't.
  await expect(dialog.getByText(/^Not encrypted: over http:\/\//)).toBeVisible();
  await address.fill("https://pyro.example.com");
  await expect(dialog.getByText(/^Not encrypted/)).toHaveCount(0);
  await address.fill("http://10.0.0.1:7878");
  await expect(dialog.getByLabel("Plain command", { exact: true })).toHaveText(`pyro agent --connect 'http://10.0.0.1:7878' --token '${token}'`);
  await expect(dialog.getByLabel("Environment settings", { exact: true })).toHaveText(
    `PYRO_MODE=agent\nPYRO_AGENT_CONNECT=http://10.0.0.1:7878\nPYRO_AGENT_TOKEN=${token}`,
  );
  await address.fill("http://h/it's");
  await expect(docker).toContainText(`--connect 'http://h/it'\\''s' --token`);
  await address.fill(TEAM);

  // Start the agent the way a platform would: the image's default command
  // (serve --team) with the environment settings from the dialog.
  let child: ChildProcess | undefined;
  let log = "";
  try {
    child = spawn(bin, ["serve", "--team"], {
      stdio: ["ignore", "pipe", "pipe"],
      env: { ...process.env, PYRO_MODE: "agent", PYRO_AGENT_CONNECT: TEAM, PYRO_AGENT_TOKEN: token },
    });
    child.stdout?.on("data", (d) => (log += d));
    child.stderr?.on("data", (d) => (log += d));

    // The dialog turns to connected by itself, and the row becomes available.
    await expect(dialog.getByText("Connected", { exact: true })).toBeVisible({ timeout: 10_000 });
    await dialog.getByRole("button", { name: "Done" }).click();
    await expect(page.getByRole("row", { name: /e2e-agent/ })).toContainText("available", { timeout: 10_000 });
    await expect(page.getByRole("row", { name: /e2e-agent/ })).toContainText("Connects in");
    expect(log).toContain("connected to");

    // Run a load test on it from a project: the picker lists it ticked and available.
    await page.goto(TEAM);
    await expect(page).toHaveURL(/\/p\/[0-9a-f]+$/);
    const pid = new URL(page.url()).pathname.split("/")[2];
    const api = `${TEAM}/api/v1/projects/${pid}`;
    const col = await page.request.post(`${api}/collections`, { headers, data: { slug: "agentdemo", name: "Agent demo" } });
    expect([201, 409]).toContain(col.status());
    expect((await page.request.put(`${api}/requests/agentdemo/items`, { headers, data: { kind: "http", name: "Items", method: "GET", url: "http://127.0.0.1:7891/items" } })).status()).toBe(200);
    const plan = "name: Agent smoke\nscenarios:\n  s:\n    executor: constant-vus\n    vus: 2\n    duration: 2s\n    flow: [ { request: agentdemo/items }, { think: 50ms } ]\n";
    expect((await page.request.put(`${api}/loadtests/agent-smoke`, { headers, data: { yaml: plan } })).status()).toBe(200);
    await page.reload();
    await page.getByRole("tab", { name: "Load" }).click();
    await page.locator(".rail .row", { hasText: "Agent smoke" }).first().click();
    await page.getByRole("radio", { name: /^Agents/ }).check();
    const pick = page.getByRole("group", { name: "Agents to run on" });
    const box = pick.getByRole("checkbox", { name: /e2e-agent/ });
    await expect(box).toBeChecked();
    await expect(pick).toContainText("available");
    await expect(pick).toContainText("1 of 1 selected");
    // Status is part of the checkbox's description, not just its colour.
    await expect(box).toHaveAccessibleDescription(/available/);

    // Unticking it leaves nothing to run on, and the choice is remembered per load test.
    await box.uncheck();
    await expect(pick).toContainText("0 of 1 selected");
    await expect(page.getByRole("button", { name: "Start" })).toBeDisabled();
    await expect(page.getByText("Tick at least one agent")).toBeVisible();
    // Only the unticked agents are stored, under a key with the project and the test.
    const stored = await page.evaluate((k) => localStorage.getItem(k), `pyro.loadtest.agents.${pid}.agent-smoke`);
    expect(JSON.parse(stored ?? "null").unticked).toHaveLength(1);
    await page.reload();
    await page.getByRole("tab", { name: "Load" }).click();
    await page.locator(".rail .row", { hasText: "Agent smoke" }).first().click();
    await page.getByRole("radio", { name: /^Agents/ }).check();
    await expect(pick.getByRole("checkbox", { name: /e2e-agent/ })).not.toBeChecked();
    await pick.getByRole("checkbox", { name: /e2e-agent/ }).check();
    await expect(pick).toContainText("1 of 1 selected");
    await page.getByRole("button", { name: "Start" }).click();
    await expect(page.locator(".loadrun .verdict.v-passed")).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(".loadrun .agent-table")).toContainText("e2e-agent");

    // Back to the administration page for the removal.
    await page.getByRole("button", { name: "Account menu for Olivia Owner" }).click();
    await page.getByRole("menuitem", { name: "Administration" }).click();
    await page.getByRole("tab", { name: /^Agents/ }).click();

    // Removing it asks first, drops the row and the agent's connection.
    await page.getByRole("row", { name: /e2e-agent/ }).getByRole("button", { name: "Remove" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
    await expect(page.getByRole("row", { name: /e2e-agent/ })).toHaveCount(0);
    await expect.poll(() => log, { timeout: 10_000 }).toMatch(/rejected this agent/);
  } finally {
    child?.kill("SIGTERM");
  }
});

test("admin adds a direct agent, tests and edits it, and removes it", async ({ page, request }) => {
  await expect
    .poll(async () => (await request.get(`${TEAM}/api/v1/health`).catch(() => null))?.status() ?? 0, { timeout: 15_000 })
    .toBe(200);
  const login = await page.request.post(`${TEAM}/api/v1/auth/login`, { headers: { "X-Pyro-CSRF": "1" }, data: { email: "owner@example.com", password: PASSWORD } });
  expect(login.status()).toBe(200);

  let child: ChildProcess | undefined;
  try {
    child = spawn(bin, ["agent", "--host", "127.0.0.1", "--port", "7993", "--token", "direct-secret"], { stdio: "ignore" });
    const info = () => request.get("http://127.0.0.1:7993/agent/v1/info", { headers: { Authorization: "Bearer direct-secret" } }).then((r) => r.status(), () => 0);
    await expect.poll(info, { timeout: 10_000 }).toBe(200);

    await page.goto(TEAM);
    await page.getByRole("button", { name: "Account menu for Olivia Owner" }).click();
    await page.getByRole("menuitem", { name: "Administration" }).click();
    await page.getByRole("tab", { name: /^Agents/ }).click();
    await page.getByRole("button", { name: "Add agent" }).click();
    await page.getByRole("menuitem", { name: "Direct…" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Name").fill("e2e-direct");
    await dialog.getByLabel("Address").fill("127.0.0.1:7993");
    await dialog.getByLabel("Token").fill("direct-secret");
    await dialog.getByRole("button", { name: "Add and test" }).click();
    await expect(dialog.getByText("The agent answered")).toBeVisible();
    await dialog.getByRole("button", { name: "Done" }).click();
    const row = page.getByRole("row", { name: /e2e-direct/ });
    await expect(row).toContainText("available");
    await expect(row).toContainText("Direct");

    // A wrong token is saved with a warning, not refused; the row shows the error.
    await row.getByRole("button", { name: "Edit e2e-direct" }).click();
    await dialog.getByLabel("Token").fill("not-the-token");
    await dialog.getByRole("button", { name: "Save and test" }).click();
    await expect(dialog.getByText("Saved, but the agent can't be reached yet")).toBeVisible();
    await dialog.getByRole("button", { name: "Done" }).click();
    await expect(row).toContainText("error");

    // Back to the right token, then Test.
    await row.getByRole("button", { name: "Edit e2e-direct" }).click();
    await dialog.getByLabel("Token").fill("direct-secret");
    await dialog.getByRole("button", { name: "Save and test" }).click();
    await expect(dialog.getByText("The agent answered")).toBeVisible();
    await dialog.getByRole("button", { name: "Done" }).click();
    await row.getByRole("button", { name: "Test e2e-direct" }).click();
    await expect(page.getByText("e2e-direct is available")).toBeVisible();

    await row.getByRole("button", { name: "Remove e2e-direct" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
    await expect(page.getByRole("row", { name: /e2e-direct/ })).toHaveCount(0);
  } finally {
    child?.kill("SIGTERM");
  }
});
