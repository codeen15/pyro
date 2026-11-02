import { test, expect, noHorizontalScroll, pickOption } from "./fixtures";

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".tree");
});

test("send a request and see its tests pass", async ({ page }) => {
  await page.getByRole("button", { name: "GET List items", exact: true }).click();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("200");
  await expect(page.locator(".response")).toContainText('"page"');
  await page.getByRole("tab", { name: /Tests/ }).last().click();
  await expect(page.locator(".response")).toContainText("status eq 200");
});

test("pre and post processors and think time", async ({ page }) => {
  await page.getByRole("button", { name: "GET List items", exact: true }).click();

  // Pre: a variable used by the query string, and a think time for load tests.
  await page.getByRole("tab", { name: /Pre-processors/ }).click();
  await page.getByRole("button", { name: "Add pre-processor" }).click();
  await page.getByRole("menuitem", { name: /Set variables/ }).click();
  await page.getByLabel("Variables key 1").fill("pg");
  await page.getByLabel("Variables value 1").fill("7");
  await pickOption(page, page.getByRole("combobox", { name: "Think time" }), "Constant");
  await page.getByLabel("Delay", { exact: true }).fill("200ms");
  await expect(page.locator(".timer-summary")).toHaveText("Waits 200 ms before the request in load tests");

  await page.getByRole("tab", { name: /Params/ }).click();
  await page.getByLabel("Query parameters value 1").fill("{{pg}}");

  // Post: the saved assertions show up as cards; drop the one expecting page 2.
  await page.getByRole("tab", { name: /Post-processors/ }).click();
  await expect(page.locator(".proc-card")).toHaveCount(2);
  await page.getByRole("button", { name: "Remove Assertion" }).nth(1).click();
  await page.getByRole("button", { name: "Add post-processor" }).click();
  await page.getByRole("menuitem", { name: /Extract a value/ }).click();
  await page.getByLabel("Extract path").fill("$.query.page");
  await page.getByLabel("Into variable").fill("seen");
  await page.getByRole("button", { name: "Add post-processor" }).click();
  await page.getByRole("menuitem", { name: /Assertion/ }).click();
  const added = page.locator(".proc-card").last();
  await pickOption(page, added.getByRole("combobox", { name: "Check" }), "JSON path");
  await added.getByLabel("Assertion path").fill("$.query.page");
  await added.getByLabel("Expected value").fill("7");

  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText('"page": "7"');
  await page.getByRole("tab", { name: /Tests/ }).last().click();
  await expect(page.locator(".test-list li.is-pass", { hasText: "$.query.page eq 7" })).toBeVisible();
  await expect(page.locator(".test-list li.is-fail")).toHaveCount(0);
  await expect(page.locator(".tests-view")).toContainText("{{seen}}");
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
  await page.getByRole("button", { name: "POST POST 127.0.0.1:7891/orders", exact: true }).click();
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
  await page.getByRole("button", { name: "GET Who am I", exact: true }).click();
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
  await page.getByRole("button", { name: "POST Log in", exact: true }).click();
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
  await page.getByRole("button", { name: "POST Create order", exact: true }).click();
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
  await page.getByRole("button", { name: "Theme" }).click();
  await page.getByRole("menuitem", { name: "Light" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Theme" }).click();
  await page.getByRole("menuitem", { name: "System theme" }).click();
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
  await expect(page.getByLabel("Collection name", { exact: true })).toHaveValue("Billing API");
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

test("a new request's type is chosen once, when it is created", async ({ page }) => {
  const row = page.locator(".row-wrap").filter({ has: page.getByText("Shop", { exact: true }) });
  await row.hover();
  await row.getByRole("button", { name: "New request in Shop" }).click();
  await page.getByRole("radio", { name: /Socket\.IO/ }).click();
  await page.getByRole("dialog").getByLabel("Request name").fill("Chat room");
  await page.getByRole("button", { name: "Create request" }).click();
  await expect(page.locator(".req-path")).toContainText("shop/chat-room");
  await expect(page.locator(".kind-label")).toHaveText("Socket.IO");
  await expect(page.getByRole("button", { name: "Connect" }).first()).toBeVisible();
  await expect(page.locator(".tree")).toContainText("Chat room");
});

test("add a folder, then delete a request and the folder", async ({ page }) => {
  const shop = page.locator(".row-wrap").filter({ has: page.getByText("Shop", { exact: true }) });
  await shop.hover();
  await shop.getByRole("button", { name: "More actions for Shop" }).click();
  await page.getByRole("menuitem", { name: "New folder in Shop" }).click();
  await page.getByLabel("Folder name").fill("Refunds");
  await page.getByRole("button", { name: "Create folder" }).click();
  const folder = page.locator(".row-wrap").filter({ has: page.getByText("Refunds", { exact: true }) });
  await expect(folder).toBeVisible();

  await folder.hover();
  await folder.getByRole("button", { name: "New request in Refunds" }).click();
  await page.getByRole("dialog").getByLabel("Request name").fill("Issue refund");
  await page.getByRole("button", { name: "Create request" }).click();
  await expect(page.locator(".req-path")).toContainText("shop/refunds/issue-refund");

  // Delete from the request header.
  await page.getByRole("button", { name: "Delete request Issue refund" }).click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(page.locator(".tree")).not.toContainText("Issue refund");
  await expect(page.locator(".req-path")).toHaveCount(0);

  // Delete the folder from its menu.
  await folder.hover();
  await folder.getByRole("button", { name: "More actions for Refunds" }).click();
  await page.getByRole("menuitem", { name: "Delete Refunds" }).click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(page.locator(".tree")).not.toContainText("Refunds");
  await expect(page.locator(".tree")).toContainText("List items");
});

test("rename, duplicate and delete a load test", async ({ page }) => {
  await page.getByRole("tab", { name: "Load" }).click();
  const row = page.locator(".rail .row-wrap", { hasText: "Smoke" }).first();
  await row.hover();
  await row.getByRole("button", { name: "More actions for Smoke" }).click();
  await page.getByRole("menuitem", { name: "Duplicate Smoke" }).click();
  await page.getByLabel("Load test name").fill("Smoke nightly");
  await page.getByRole("button", { name: "Duplicate", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Smoke nightly" })).toBeVisible();

  // Rename and delete from the plan's own header menu.
  await page.locator(".lt-head-actions").getByRole("button", { name: "More actions for Smoke nightly" }).click();
  await page.getByRole("menuitem", { name: "Rename Smoke nightly" }).click();
  await page.getByLabel("Load test name").fill("Nightly");
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Nightly" })).toBeVisible();
  await expect(page.locator(".rail")).not.toContainText("Smoke nightly");

  await page.locator(".lt-head-actions").getByRole("button", { name: "More actions for Nightly" }).click();
  await page.getByRole("menuitem", { name: "Delete Nightly" }).click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(page.locator(".rail")).not.toContainText("Nightly");
  await expect(page.locator(".rail")).toContainText("Smoke");
});

test("variables are coloured and editable from a hover card", async ({ page }) => {
  await page.getByRole("button", { name: "GET List items", exact: true }).click();
  await page.getByLabel("URL").fill("{{base}}/items/{{color}}");
  await expect(page.locator(".var-token.is-defined[data-var=base]")).toBeVisible();
  await expect(page.locator(".var-token.is-missing[data-var=color]")).toBeVisible();

  // Add the missing variable to the collection from its card.
  // The coloured text sits under the input, so point at where it is drawn.
  const box = (await page.locator(".var-token[data-var=color]").boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await expect(page.locator(".var-card")).toContainText("Not defined");
  await page.locator(".var-card").getByLabel("Value of color").fill("red");
  await page.locator(".var-card").getByRole("button", { name: "Add" }).click();
  await expect(page.locator(".var-token.is-defined[data-var=color]")).toBeVisible();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".response")).toContainText("/items/red");
});

test("load test data from a CSV and Socket.IO events with args", async ({ page }) => {
  // A Socket.IO request to use as a step.
  const shop = page.locator(".row-wrap").filter({ has: page.getByText("Shop", { exact: true }) });
  await shop.hover();
  await shop.getByRole("button", { name: "New request in Shop" }).click();
  await page.getByRole("radio", { name: /Socket\.IO/ }).click();
  await page.getByRole("dialog").getByLabel("Request name").fill("Chat");
  await page.getByRole("button", { name: "Create request" }).click();
  await expect(page.locator(".kind-label")).toHaveText("Socket.IO");

  await page.getByRole("tab", { name: "Load" }).click();
  await page.locator(".rail .row", { hasText: "Smoke" }).first().click();

  // A CSV without a header, named like JMeter's Variable Names.
  await page.locator(".lt-data input[type=file]").setInputFiles({ name: "users.csv", mimeType: "text/csv", buffer: Buffer.from("ada;s3cret;eu\nbob;hunter2;us\n") });
  await pickOption(page, page.getByRole("combobox", { name: "Delimiter" }), "Semicolon ;");
  await page.getByLabel("Variable names").fill("user,pass,region");
  await expect(page.locator(".lt-data")).toContainText("2 rows");
  await expect(page.locator(".lt-data-preview")).toContainText("hunter2");
  // Map a column to another variable name.
  await page.getByLabel("Variable for column user").first().fill("username");

  // The Socket.IO step: an event with JSON args using the data.
  await page.getByRole("button", { name: "Request", exact: true }).click();
  await pickOption(page, page.locator(".flow-step").last().getByRole("combobox", { name: "Request" }), "shop/chat (socketio)");
  await page.getByLabel("Keep the connection open for").fill("2s");
  await page.getByRole("button", { name: "Event", exact: true }).click();
  await page.getByLabel("Event of message 1").fill("join");
  // Typed arguments, like Postman: text then JSON.
  const step = page.locator(".step-msg").first();
  await step.getByRole("button", { name: "Add argument" }).click();
  await step.getByLabel("Argument 1", { exact: true }).fill("lobby");
  await step.getByRole("button", { name: "Add argument" }).click();
  await pickOption(page, step.getByRole("combobox", { name: "Type of argument 2" }), "JSON");
  await step.getByLabel("Argument 2", { exact: true }).fill('{"user": "{{username}}"}');
  await page.locator(".step-msg").getByText("Ack").click();

  await page.getByRole("tab", { name: "YAML" }).click();
  const yaml = page.getByLabel("Load test YAML");
  await expect(yaml).toHaveValue(/file: data\/users\.csv/);
  await expect(yaml).toHaveValue(/variables:\s*\n\s*- user\s*\n\s*- pass\s*\n\s*- region/);
  await expect(yaml).toHaveValue(/delimiter: "?;"?/);
  await expect(yaml).toHaveValue(/user: username/);
  await expect(yaml).toHaveValue(/event: join/);
  await expect(yaml).toHaveValue(/\[\\?"lobby\\?", \{\\?"user\\?": \\?"\{\{username\}\}\\?"\}\]/);
  await expect(yaml).toHaveValue(/ack: true/);
  await expect(yaml).toHaveValue(/hold: 2s/);
});

test("Socket.IO messages take several typed arguments, WebSocket a formatted payload", async ({ page }) => {
  const shop = page.locator(".row-wrap").filter({ has: page.getByText("Shop", { exact: true }) });
  for (const [kind, name] of [["Socket.IO", "Room"], ["WebSocket", "Feed"]]) {
    await shop.hover();
    await shop.getByRole("button", { name: "New request in Shop" }).click();
    await page.getByRole("radio", { name: new RegExp(kind.replace(".", "\\.")) }).click();
    await page.getByRole("dialog").getByLabel("Request name").fill(name);
    await page.getByRole("button", { name: "Create request" }).click();
    await expect(page.locator(".kind-label")).toHaveText(kind);
    if (kind === "Socket.IO") {
      await page.getByLabel("Event", { exact: true }).fill("join");
      for (let i = 0; i < 4; i++) await page.getByRole("button", { name: "Add argument" }).click();
      await page.getByLabel("Argument 1", { exact: true }).fill("lobby");
      await pickOption(page, page.getByRole("combobox", { name: "Type of argument 2" }), "JSON");
      await page.getByLabel("Argument 2", { exact: true }).fill('{"user": "{{user}}"}');
      await pickOption(page, page.getByRole("combobox", { name: "Type of argument 3" }), "Number");
      await page.getByLabel("Argument 3", { exact: true }).fill("{{count}}");
      await pickOption(page, page.getByRole("combobox", { name: "Type of argument 4" }), "Boolean");
      await page.getByLabel("Argument 4").getByText("true", { exact: true }).click();
      await page.getByRole("button", { name: "Move argument 4 up" }).click();
      // The same message as the raw JSON array the engine sends.
      await page.getByLabel("Edit arguments as JSON").click();
      await expect(page.getByLabel("Arguments as JSON", { exact: true })).toHaveValue('["lobby", {"user": "{{user}}"}, true, {{count}}]');
      await page.getByLabel("Edit arguments as JSON").click();
      await expect(page.locator(".arg-row")).toHaveCount(4);
      await page.getByRole("button", { name: "Save message" }).click();
      await page.getByRole("tab", { name: /Saved/ }).click();
      await expect(page.locator(".saved-list")).toContainText('["lobby", {"user": "{{user}}"}, true, {{count}}]');
    } else {
      await page.getByLabel("Message format").getByText("JSON", { exact: true }).click();
      await page.getByRole("textbox", { name: "Message", exact: true }).fill('{"op":"subscribe","topic":"{{topic}}"}');
      await page.getByRole("button", { name: "Format" }).click();
      await expect(page.getByRole("textbox", { name: "Message", exact: true })).toHaveValue('{\n  "op": "subscribe",\n  "topic": "{{topic}}"\n}');
    }
  }
});

test("the user guide opens from the top bar, links between pages and searches", async ({ page }) => {
  await page.getByRole("button", { name: "Docs", exact: true }).click();
  await expect(page.locator(".docs-body h1")).toHaveText("Pyro user guide");
  await page.locator(".docs-body").getByRole("link", { name: "Load testing" }).click();
  await expect(page.locator(".docs-body h1")).toHaveText("Load testing");
  await expect(page.locator(".docs-nav [aria-current=page]")).toHaveText("Load testing");
  // An in-guide link with an anchor.
  await page.locator(".docs-body").getByRole("link", { name: "the console" }).click();
  await expect(page.locator(".docs-body h1")).toHaveText("WebSocket and Socket.IO");
  await expect(page.locator("#sending-socketio-events")).toBeInViewport();
  await page.getByLabel("Search the guide").fill("Poisson");
  await expect(page.locator(".docs-results")).toContainText("Processors and tests");
});
