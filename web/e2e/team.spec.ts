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

test("team server: invite a viewer, promote them, copy a collection and resolve a save conflict", async ({ browser, page, request }) => {
  await waitForTeam(request);
  await page.goto(TEAM);
  await page.getByRole("tab", { name: "Create account" }).click();
  await page.getByLabel("Name").fill("Alice");
  await page.getByLabel("Email").fill("alice@example.com");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByRole("button", { name: /^Project: My project/ })).toBeVisible();

  // A team workspace with a project and a collection holding one request.
  await page.getByRole("button", { name: "Account menu for Alice" }).click();
  await page.getByRole("menuitem", { name: "Workspaces and projects" }).click();
  await page.getByRole("button", { name: "New workspace" }).click();
  await page.getByRole("dialog").getByLabel("Name").fill("Shop team");
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click();
  const card = page.locator(".ws-card", { has: page.getByRole("heading", { name: "Shop team" }) });
  await card.getByRole("button", { name: "New project" }).click();
  await page.getByRole("dialog").getByLabel("Name").fill("Checkout");
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("button", { name: /^Project: Checkout/ })).toBeVisible();
  const pid = new URL(page.url()).pathname.split("/")[2];
  const api = `${TEAM}/api/v1/projects/${pid}`;
  const headers = { "X-Pyro-CSRF": "1" };
  expect((await page.request.post(`${api}/collections`, { headers, data: { slug: "orders", name: "Orders" } })).status()).toBe(201);
  expect((await page.request.put(`${api}/requests/orders/list`, { headers, data: { kind: "http", name: "List orders", method: "GET", url: "http://127.0.0.1:7891/orders" } })).status()).toBe(200);
  await page.reload();
  await expect(page.locator(".tree")).toContainText("Orders");

  // Invite a viewer by link.
  await page.getByRole("button", { name: "Account menu for Alice" }).click();
  await page.getByRole("menuitem", { name: "Workspaces and projects" }).click();
  await card.getByRole("button", { name: "Members" }).click();
  await expect(page.getByRole("heading", { name: "Shop team" })).toBeVisible();
  await page.getByRole("button", { name: "Invite people" }).click();
  await page.getByRole("dialog").getByLabel("Role").click();
  await page.locator(".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option", { hasText: /^Viewer$/ }).last().click();
  await page.getByRole("button", { name: "Create invite link" }).click();
  const link = await page.getByLabel("Invite link").inputValue();
  expect(link).toContain("/invite/");
  await page.getByRole("button", { name: "Done" }).click();

  // Bob signs up through the link and lands in the project, read-only.
  const bobCtx = await browser.newContext();
  const bob = await bobCtx.newPage();
  await bob.goto(link);
  await expect(bob.getByText("Alice invited you to join Shop team as a viewer.")).toBeVisible();
  await bob.getByLabel("Name").fill("Bob");
  await bob.getByLabel("Email").fill("bob.viewer@example.com");
  await bob.getByLabel("Password").fill(PASSWORD);
  await bob.getByRole("button", { name: "Create account" }).click();
  await expect(bob.getByRole("button", { name: /^Project: Checkout/ })).toBeVisible();
  await expect(bob.locator(".tree")).toContainText("Orders");
  await expect(bob.getByRole("button", { name: "New collection" })).toHaveCount(0);
  await expect(bob.getByRole("button", { name: "Import", exact: true })).toHaveCount(0);
  await bob.getByRole("button", { name: "GET List orders", exact: true }).click();
  await expect(bob.getByRole("button", { name: "Read only" })).toBeVisible();

  // Alice sees Bob, and makes him an editor.
  await page.reload();
  await expect(page.getByText("bob.viewer@example.com")).toBeVisible();
  await page.getByLabel("Role of Bob").click();
  await page.locator(".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option", { hasText: /^Editor$/ }).last().click();
  await expect(page.getByText("Role changed")).toBeVisible();
  await page.getByRole("tab", { name: "Activity" }).click();
  await expect(page.getByText("joined as viewer")).toBeVisible();

  await bob.reload();
  await expect(bob.getByRole("button", { name: "New collection" })).toBeVisible();

  // Both open the request; Alice saves first, so Bob's save asks first.
  await page.getByRole("button", { name: /^Project: Checkout/ }).click();
  await page.getByRole("menuitem", { name: "Workspaces and projects" }).press("Escape");
  await page.goto(`${TEAM}/p/${pid}`);
  await page.getByRole("button", { name: "GET List orders", exact: true }).click();
  await bob.getByRole("button", { name: "GET List orders", exact: true }).click();
  await expect(bob.getByLabel("URL")).toHaveValue("http://127.0.0.1:7891/orders");
  await bob.getByLabel("URL").fill("http://127.0.0.1:7891/orders?by=bob");
  await page.getByLabel("URL").fill("http://127.0.0.1:7891/orders?by=alice");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("button", { name: "Saved" })).toBeVisible();
  // Bob is told about the change, and keeps his unsaved edit.
  await expect(bob.getByText("Alice saved request orders/list")).toBeVisible();
  await expect(bob.getByLabel("URL")).toHaveValue("http://127.0.0.1:7891/orders?by=bob");
  await bob.getByRole("button", { name: "Save" }).click();
  await expect(bob.getByText("Someone else saved this").filter({ visible: true })).toBeVisible();
  await expect(bob.getByRole("button", { name: "Overwrite" })).toHaveCount(1);
  await expect(bob.getByText("Alice changed this since you opened it")).toBeVisible();
  await bob.getByRole("button", { name: "Overwrite" }).click();
  await expect(bob.getByRole("button", { name: "Saved" })).toBeVisible();

  // Copy the collection into Alice's personal project.
  const row = page.locator(".row-wrap", { hasText: "Orders" }).first();
  await row.hover();
  await row.getByRole("button", { name: "More actions for Orders" }).click();
  await page.getByRole("menuitem", { name: "Move or copy to…" }).click();
  await page.getByRole("dialog").getByLabel("Target project").click();
  await page.locator(".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option", { hasText: /^My project$/ }).last().click();
  await page.getByRole("button", { name: "Copy collection" }).click();
  await expect(page.getByText("Copied Orders to My project")).toBeVisible();
  await page.getByRole("button", { name: /^Project: Checkout/ }).click();
  await page.getByRole("menuitem", { name: "My project" }).click();
  await expect(page.locator(".tree")).toContainText("Orders");
  await page.getByRole("button", { name: "GET List orders", exact: true }).click();
  await expect(page.getByLabel("URL")).toHaveValue("http://127.0.0.1:7891/orders?by=bob");

  await bobCtx.close();
});

test("team server: administration, password reset links and API tokens", async ({ browser, page, request }) => {
  await waitForTeam(request);
  // The server's first account (from the first test) is the administrator.
  await page.goto(TEAM);
  await page.getByLabel("Email").fill("owner@example.com");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByRole("button", { name: "Account menu for Olivia Owner" }).click();
  await page.getByRole("menuitem", { name: "Administration" }).click();
  await expect(page.getByRole("heading", { name: "Administration" })).toBeVisible();
  const bobRow = page.getByRole("row", { name: /bob\.viewer@example\.com/ });
  await expect(bobRow).toBeVisible();

  // A one-time password link for Bob.
  await bobRow.getByRole("button", { name: "Reset password" }).click();
  const link = await page.getByLabel("Password link", { exact: true }).inputValue();
  expect(link).toContain("/reset/");
  await page.getByRole("button", { name: "Done" }).click();

  const bobCtx = await browser.newContext();
  const bob = await bobCtx.newPage();
  await bob.goto(link);
  await expect(bob.getByRole("heading", { name: "Choose a new password" })).toBeVisible();
  await expect(bob.getByText("bob.viewer@example.com")).toBeVisible();
  await bob.getByLabel("New password", { exact: true }).fill("a brand new password");
  await bob.getByRole("button", { name: "Set password and sign in" }).click();
  await expect(bob.getByRole("button", { name: /^Project:/ })).toBeVisible();

  // Disabling Bob signs him out.
  await bobRow.getByRole("switch", { name: "Active: Bob" }).click();
  await page.getByRole("button", { name: "Disable" }).click();
  await expect(page.getByText("Bob is disabled")).toBeVisible();
  await bob.reload();
  await expect(bob.getByRole("button", { name: "Sign in" })).toBeVisible();
  await bobCtx.close();

  // The Server tab shows how the server is set up.
  await page.getByRole("tab", { name: "Server" }).click();
  await expect(page.getByText("By invitation").or(page.getByText("Open to anyone"))).toBeVisible();

  // An API token works as a Bearer token and can be revoked.
  await page.getByRole("button", { name: "Account menu for Olivia Owner" }).click();
  await page.getByRole("menuitem", { name: "Account" }).click();
  await page.getByRole("button", { name: "New token" }).click();
  await page.getByLabel("Token name").fill("CI pipeline");
  await page.getByRole("button", { name: "Create token" }).click();
  const token = await page.getByLabel("API token", { exact: true }).inputValue();
  expect(token).toMatch(/^pyro_pat_/);
  await page.getByRole("button", { name: "Done" }).click();
  const me = await request.get(`${TEAM}/api/v1/me`, { headers: { Authorization: `Bearer ${token}` } });
  expect(me.status()).toBe(200);
  expect((await me.json()).user.email).toBe("owner@example.com");
  await page.getByRole("button", { name: "Revoke token CI pipeline" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(page.getByText("No tokens.")).toBeVisible();
  expect((await request.get(`${TEAM}/api/v1/me`, { headers: { Authorization: `Bearer ${token}` } })).status()).toBe(401);
});
