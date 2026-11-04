import { test, expect, noHorizontalScroll } from "./fixtures";

// The team server (:7892, open sign-up, empty data directory; see server.mjs).
const TEAM = "http://127.0.0.1:7892";
const PASSWORD = "correct horse battery";

async function waitForTeam(request: import("@playwright/test").APIRequestContext) {
  await expect
    .poll(async () => (await request.get(`${TEAM}/api/v1/health`).catch(() => null))?.status() ?? 0, { timeout: 15_000 })
    .toBe(200);
}

test("team server: sign up, switch projects, create a workspace and sign out", async ({ page, request, errors }) => {
  await waitForTeam(request);
  await page.goto(TEAM);

  // The first account becomes the administrator.
  await expect(page.getByText("The account you create becomes the server administrator.")).toBeVisible();
  await page.getByLabel("Name").fill("Olivia Owner");
  await page.getByLabel("Email").fill("owner@example.com");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Create account" }).click();

  // Lands in the personal workspace's first project.
  const switcher = page.getByRole("button", { name: /^Project: My project/ });
  await expect(switcher).toBeVisible();
  await expect(page).toHaveURL(/\/p\/[0-9a-f]+$/);
  await expect(switcher).toContainText("Personal");

  // The project API works through the team server: create a collection.
  await page.getByRole("button", { name: "Account menu for Olivia Owner" }).click();
  await page.getByRole("menuitem", { name: "Workspaces and projects" }).click();
  await expect(page.getByRole("heading", { name: "Workspaces and projects" })).toBeVisible();

  // A team workspace with a project.
  await page.getByRole("button", { name: "New workspace" }).click();
  await page.getByRole("dialog").getByLabel("Name").fill("QA team");
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "QA team" })).toBeVisible();
  const qa = page.locator(".ws-card", { has: page.getByRole("heading", { name: "QA team" }) });
  await expect(qa.getByText("No projects yet.")).toBeVisible();
  await qa.getByRole("button", { name: "New project" }).click();
  await page.getByRole("dialog").getByLabel("Name").fill("Payments");
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click();

  // Creating a project opens it.
  const payments = page.getByRole("button", { name: /^Project: Payments/ });
  await expect(payments).toBeVisible();
  await expect(payments).toContainText("QA team");
  await expect(page.getByRole("heading", { name: "Bring your API in" })).toBeVisible();

  // Switch back through the top bar.
  await payments.click();
  await page.getByRole("menuitem", { name: "My project" }).click();
  await expect(page.getByRole("button", { name: /^Project: My project/ })).toBeVisible();

  // Phone width: the switcher stays reachable and nothing overflows.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await expect(page.getByRole("button", { name: /^Project: My project/ })).toBeVisible();
  await noHorizontalScroll(page);
  await page.setViewportSize({ width: 1440, height: 900 });

  // Sign out, then back in.
  await page.getByRole("button", { name: "Account menu for Olivia Owner" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  await page.getByLabel("Email").fill("owner@example.com");
  await page.getByLabel("Password").fill("wrong password!!");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByText("wrong email or password")).toBeVisible();
  // The browser logs the refused sign-in; that one is expected.
  errors.splice(0, errors.length, ...errors.filter((e) => !e.includes("400 (Bad Request)")));
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: /^Project: My project/ })).toBeVisible();
});

test("team server: another user can't see someone else's workspaces", async ({ browser, request }) => {
  await waitForTeam(request);
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(TEAM);
  await page.getByRole("tab", { name: "Create account" }).click();
  await page.getByLabel("Email").fill("bob@example.com");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Create account" }).click();
  const switcher = page.getByRole("button", { name: /^Project: My project/ });
  await expect(switcher).toBeVisible();
  await switcher.click();
  await expect(page.getByRole("menuitem", { name: "Payments" })).toHaveCount(0);
  await ctx.close();
});
