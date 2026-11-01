import { test, expect, noHorizontalScroll } from "./fixtures";

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
  await expect(page.getByText("2 requests · 0 failed")).toBeVisible();
  await expect(page.getByText("3 tests · 0 failed")).toBeVisible();
});

test("paste a cURL command into an existing collection", async ({ page }) => {
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await page.selectOption('select[aria-label="Add to"]', "shop");
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
  const [download] = await Promise.all([page.waitForEvent("download"), row.getByRole("button", { name: "Export Shop to Postman" }).click()]);
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
});
