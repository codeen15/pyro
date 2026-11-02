import { test as base, expect } from "@playwright/test";

// Every test fails on console errors or uncaught exceptions.
export const test = base.extend<{ errors: string[] }>({
  errors: [
    async ({ page }, use) => {
      const errors: string[] = [];
      page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
      page.on("pageerror", (e) => errors.push(String(e)));
      await use(errors);
      expect(errors, "console errors").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

export async function noHorizontalScroll(page: import("@playwright/test").Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow, "horizontal overflow (px)").toBeLessThanOrEqual(0);
}

/** Picks an option in an antd Select: opens it via its combobox, then clicks the option by its exact text
 * (in the newest dropdown; a previous one may still be animating closed). */
export async function pickOption(page: import("@playwright/test").Page, combobox: import("@playwright/test").Locator, text: string) {
  await combobox.click();
  const esc = text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  await page.locator(".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option", { hasText: new RegExp(`^${esc}$`) }).last().click();
}
