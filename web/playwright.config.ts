import { defineConfig, devices } from "@playwright/test";

// End-to-end tests drive the real binary (../bin/pyro, from `make build`)
// against a mock API; see e2e/server.mjs.
export default defineConfig({
  testDir: "e2e",
  timeout: 60_000,
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: "http://127.0.0.1:7890",
    colorScheme: "dark",
    trace: "retain-on-failure",
    ...devices["Desktop Chrome"],
    viewport: { width: 1440, height: 900 },
  },
  webServer: {
    command: "node e2e/server.mjs",
    url: "http://127.0.0.1:7890/api/v1/health",
    reuseExistingServer: false,
    timeout: 30_000,
  },
});
