import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { test, expect } from "./fixtures";

// The same folder e2e/server.mjs gives `pyro serve` as PYRO_PLUGINS_DIR.
const pluginsDir = join(tmpdir(), "pyro-e2e-plugins");
const bin = resolve(process.env.PYRO_BIN ?? "../bin/pyro");
const example = new URL("../../plugins/examples/redis-js", import.meta.url).pathname;

test("a plugin installed from the terminal shows up without reloading the page", async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".tree");

  const row = page.locator(".row-wrap").filter({ has: page.getByText("Shop", { exact: true }) });
  await row.hover();
  await row.getByRole("button", { name: "New request in Shop" }).click();
  const types = page.getByRole("radiogroup", { name: "Request type" });
  await expect(types.getByRole("radio", { name: /Socket\.IO/ })).toBeVisible();
  await expect(types.getByRole("radio", { name: /Redis/ })).toHaveCount(0);

  // The server watches the plugins folder, so an install by the CLI reaches the open page.
  execFileSync(bin, ["plugin", "install", example, "--yes"], { env: { ...process.env, PYRO_PLUGINS_DIR: pluginsDir }, stdio: "pipe" });
  await expect(types.getByRole("radio", { name: /Redis/ })).toBeVisible({ timeout: 5_000 });
});
