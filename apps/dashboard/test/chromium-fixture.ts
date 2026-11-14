// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";

/** Keep cold Chrome startup outside the security assertions' 20-second budget.
 * Each test still gets its own context, cookies, storage, and page.
 */
export function chromiumPage(executablePath: string): () => Page {
  let browser: Browser | undefined;
  let context: BrowserContext | undefined;
  let page: Page;

  beforeAll(async () => {
    browser = await chromium.launch({
      executablePath,
      headless: true,
      args: ["--no-sandbox"],
      timeout: 45_000,
    });
  }, 60_000);
  beforeEach(async () => {
    context = await browser!.newContext();
    page = await context.newPage();
    page.setDefaultNavigationTimeout(10_000);
  }, 30_000);
  afterEach(async () => {
    await context?.close();
    context = undefined;
  }, 15_000);
  afterAll(async () => {
    await browser?.close();
  }, 15_000);

  return () => page;
}
