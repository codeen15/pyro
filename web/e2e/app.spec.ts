import { test, expect, noHorizontalScroll, pickOption } from "./fixtures";

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".tree");
});

test("send a request and see its tests pass", async ({ page }) => {
  await page.getByRole("button", { name: "List items" }).click();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("200");
  await expect(page.locator(".response")).toContainText('"page"');
  await page.getByRole("tab", { name: /Tests/ }).last().click();
  await expect(page.locator(".response")).toContainText("status eq 200");
});

test("run a collection", async ({ page }) => {
  const row = page.locator(".row-wrap", { hasText: "Shop" });
  await row.hover();
  await row.getByRole("button", { name: "Run Shop" }).click();
  await page.getByRole("button", { name: /^Run/ }).last().click();
  await expect(page.locator(".tests-pass", { hasText: "Passed" })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText("3 requests · 0 failed")).toBeVisible();
  await expect(page.getByText("3 tests · 0 failed")).toBeVisible();
});

test("paste a cURL command into an existing collection", async ({ page }) => {
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await pickOption(page, page.getByLabel("Add to"), "Shop");
  await page.fill(".paste textarea", "curl -X POST 'http://127.0.0.1:7891/orders' -H 'content-type: application/json' --data-raw '{\"sku\":\"B-2\"}'");
  await page.getByRole("button", { name: "Import", exact: true }).last().click();
  await expect(page.getByRole("heading", { name: "Added 1 request to shop" })).toBeVisible();
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: /POST 127\.0\.0\.1:7891\/orders/ }).click();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("201");
});

test("export a collection to Postman", async ({ page }) => {
  const row = page.locator(".row-wrap", { hasText: "Shop" });
  await row.hover();
  await row.getByRole("button", { name: "More actions for Shop" }).click();
  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("menuitem", { name: "Export Shop to Postman" }).click()]);
  expect(download.suggestedFilename()).toBe("shop.postman_collection.json");
});

test("run a load test and compare two runs", async ({ page }) => {
  await page.getByRole("tab", { name: "Load" }).click();
  for (let i = 0; i < 2; i++) {
    await page.locator(".rail .row", { hasText: "Smoke" }).first().click();
    await page.getByRole("button", { name: "Start" }).click();
    await expect(page.locator(".loadrun .verdict.v-passed")).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(".loadrun")).toContainText("Requests");
  }
  await page.locator(".rail .row", { hasText: "Smoke" }).first().click();
  await page.getByRole("tab", { name: "Runs" }).click();
  const boxes = page.locator(".run-row input[type=checkbox]");
  await boxes.nth(0).check();
  await boxes.nth(1).check();
  await page.getByRole("button", { name: "Compare" }).click();
  await expect(page.getByRole("heading", { name: "Compare runs" })).toBeVisible();
  await expect(page.locator(".compare-table").first()).toContainText("Requests per second");
});

test("fits a phone screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await noHorizontalScroll(page);
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await noHorizontalScroll(page);
  await page.getByRole("tab", { name: "Load" }).click();
  await page.locator(".rail .row", { hasText: "Smoke" }).first().click();
  await noHorizontalScroll(page);
  await page.getByRole("button", { name: "Edit environments and globals" }).click();
  await noHorizontalScroll(page);
  await page.getByRole("button", { name: "Manage cookies" }).click();
  await noHorizontalScroll(page);
});

test("edit globals and environments with current values", async ({ page }) => {
  await page.getByRole("button", { name: "Edit environments and globals" }).click();
  await expect(page.getByRole("heading", { name: "Globals" })).toBeVisible();
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.getByLabel("Variable name").last().fill("region");
  await page.getByLabel("Initial value of region").fill("eu");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("button", { name: "Saved" })).toBeVisible();

  // A session value overrides the file without changing it.
  const current = page.getByLabel("Current value of region");
  await current.fill("us");
  await current.press("Enter");
  await expect(page.getByRole("button", { name: "Reset region" })).toBeVisible();
  await page.getByRole("button", { name: "Save current value of region as initial" }).click();
  await expect(page.getByLabel("Initial value of region")).toHaveValue("us");
  await expect(page.getByRole("button", { name: "Reset region" })).toHaveCount(0);

  // New environment, used from the top bar.
  await page.getByLabel("New environment name").fill("staging");
  await page.getByRole("button", { name: "Create environment" }).click();
  await expect(page.getByLabel("Environment name", { exact: true })).toHaveValue("staging");
  await page.getByRole("button", { name: "Use this environment" }).click();
  await expect(page.locator(".env-select")).toContainText("staging");
});

test("OAuth 2.0: client credentials and authorization code with PKCE", async ({ page }) => {
  await page.getByRole("button", { name: "Who am I" }).click();
  await page.getByRole("tab", { name: "Auth" }).click();
  await pickOption(page, page.locator("#auth-type"), "OAuth 2.0");
  await pickOption(page, page.getByLabel("Grant type"), "Client credentials");
  await page.getByLabel("Access token URL").fill("{{base}}/token");
  await page.getByLabel("Client ID").fill("app");
  await page.getByLabel("Client secret").fill("s3cret");
  await page.getByRole("button", { name: "Get new access token" }).click();
  await expect(page.locator(".token-panel")).toContainText("expires in");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("Bearer client_credentials-token");

  // Browser sign-in: the provider's window redirects back to Pyro's callback.
  await pickOption(page, page.getByLabel("Grant type"), "Authorization code (with PKCE)");
  await page.getByLabel("Auth URL").fill("{{base}}/authorize");
  const [popup] = await Promise.all([page.waitForEvent("popup"), page.getByRole("button", { name: "Get new access token" }).click()]);
  await expect(popup.getByRole("heading", { name: "Signed in" })).toBeVisible();
  await popup.close();
  await expect(page.locator(".token-panel")).toContainText("expires in");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("Bearer authorization_code-token");
});

test("cookies set by responses can be viewed, edited and deleted", async ({ page }) => {
  await page.getByRole("button", { name: "Log in" }).click();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("200");
  await page.getByRole("button", { name: "Manage cookies" }).click();
  await expect(page.getByRole("heading", { name: "Cookies", exact: true })).toBeVisible();
  const value = page.getByLabel("Value of sid");
  await expect(value).toHaveValue("abc123");
  await expect(page.locator(".cookie-table")).toContainText("HttpOnly");
  await value.fill("edited");
  await value.press("Enter");
  await page.reload();
  await expect(page.getByLabel("Value of sid")).toHaveValue("edited");
  await page.getByRole("button", { name: "Delete cookie sid" }).click();
  await expect(page.getByText("No cookies yet")).toBeVisible();
});

test("save a response as an example", async ({ page }) => {
  await page.getByRole("button", { name: "Create order" }).click();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("201");
  await page.getByRole("button", { name: "Save as example" }).click();
  await page.getByRole("tab", { name: /Examples/ }).click();
  await expect(page.getByLabel("Example name")).toHaveValue("201 Created");
  await expect(page.getByLabel("Example body")).toHaveValue(/"id": "ord_1"/);
  await expect(page.locator(".example-body")).not.toContainText("Keep-Alive");
  await page.keyboard.press("Control+s");
  await page.reload();
  await page.getByRole("tab", { name: /Examples/ }).click();
  await expect(page.getByLabel("Example name")).toHaveValue("201 Created");
});

test("theme choice applies and persists", async ({ page }) => {
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark"); // the test browser prefers dark
  await page.locator(".theme-switch").getByText("Light", { exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.locator(".theme-switch").getByText("System", { exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});

test("edit collection settings and create a collection", async ({ page }) => {
  const row = page.locator(".row-wrap", { hasText: "Shop" });
  await row.hover();
  await row.getByRole("button", { name: "Settings for Shop" }).click();
  await expect(page.getByLabel("Collection name")).toHaveValue("Shop");
  await page.getByRole("tab", { name: /Variables/ }).click();
  await expect(page.getByLabel("Value of base")).toHaveValue("http://127.0.0.1:7891");
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.getByLabel("Variable name").last().fill("region");
  await page.getByLabel("Value of region").fill("eu");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("button", { name: "Saved" })).toBeVisible();
  await page.reload();
  await page.getByRole("tab", { name: /Variables/ }).click();
  await expect(page.getByLabel("Value of region")).toHaveValue("eu");

  await page.getByRole("button", { name: "New collection" }).click();
  await page.getByLabel("New collection name").fill("Billing API");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByLabel("Collection name")).toHaveValue("Billing API");
  await expect(page.locator(".tree")).toContainText("Billing API");
});

test("API docs and mock server from examples", async ({ page, context }) => {
  const row = page.locator(".row-wrap", { hasText: "Shop" });
  await row.hover();
  await row.getByRole("button", { name: "More actions for Shop" }).click();
  const [docs] = await Promise.all([context.waitForEvent("page"), page.getByRole("menuitem", { name: "API docs for Shop" }).click()]);
  await docs.waitForLoadState();
  await expect(docs).toHaveTitle("Shop · API reference");
  await expect(docs.locator("section.req", { hasText: "List items" })).toContainText("Two items");
  await docs.close();

  await row.hover();
  await row.getByRole("button", { name: "Settings for Shop" }).click();
  await page.getByRole("tab", { name: "Mock server" }).click();
  await page.getByRole("button", { name: "Start mock server" }).click();
  const url = (await page.locator(".mock-running code").textContent())!;
  await expect(page.locator(".mock-panel")).toContainText("/items");
  const res = await page.request.get(url + "/items");
  expect(res.status()).toBe(200);
  expect(await res.text()).toBe('[{"id":1},{"id":2}]');
  await expect(page.locator(".mock-panel")).toContainText("GET /items", { timeout: 5000 });
  await page.getByRole("button", { name: "Stop" }).click();
  await expect(page.getByRole("button", { name: "Start mock server" })).toBeVisible();
});

test("import a JMeter test plan as requests and a load test", async ({ page }) => {
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await page.setInputFiles(".drop-zone input[type=file]", new URL("../../pkg/engine/importer/jmeter/testdata/shop.jmx", import.meta.url).pathname);
  await expect(page.getByRole("heading", { name: /Imported “Shop checkout”/ })).toBeVisible();
  await expect(page.locator(".report")).toContainText("jmeter");
  await page.locator(".report").getByRole("button", { name: "shop-checkout" }).click();
  await expect(page.locator(".designer")).toBeVisible();
  // One scenario per thread group; the shoppers' first step logs in once per VU.
  const login = page.locator('.flow-step[data-request="shop-checkout/shoppers/log-in"]');
  await expect(login).toHaveCount(1);
  await expect(login.locator('input[type="checkbox"]')).toBeChecked();
});
