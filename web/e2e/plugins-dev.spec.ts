import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { test, expect } from "./fixtures";
import { pluginsDir } from "./plugins-dir.mjs";

const bin = resolve(process.env.PYRO_BIN ?? "../bin/pyro");
const example = new URL("../../plugins/examples/redis-js", import.meta.url).pathname;
const pyro = (...args: string[]) => execFileSync(bin, ["plugin", ...args], { env: { ...process.env, PYRO_PLUGINS_DIR: pluginsDir }, stdio: "pipe" });

test("a plugin installed from the terminal shows up without reloading the page", async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".tree");

  const row = page.locator(".row-wrap").filter({ has: page.getByText("Shop", { exact: true }) });
  await row.hover();
  await row.getByRole("button", { name: "New request in Shop" }).click();
  const types = page.getByRole("radiogroup", { name: "Request type" });
  await expect(types.getByRole("radio", { name: /Socket\.IO/ })).toBeVisible();
  await expect(types.getByRole("radio", { name: /Redis/ })).toHaveCount(0);

  try {
    // The server watches the plugins folder, so an install by the CLI reaches the open page.
    pyro("install", example, "--yes");
    await expect(types.getByRole("radio", { name: /Redis/ })).toBeVisible({ timeout: 5_000 });
  } finally {
    // Leave the folder as found, so a retry starts clean.
    try {
      pyro("remove", "redis-js");
    } catch {
      /* not installed */
    }
  }
});
