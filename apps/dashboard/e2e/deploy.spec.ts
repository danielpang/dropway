// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// End-to-end: a brand-new user signs up, creates an org + a site, and deploys a
// folder via the drag-and-drop dropzone — driving the REAL browser DOM + React
// component + the full prepare→upload→finalize→publish path to a live site. This
// is the one layer the Go tests (fake store/JWT) and vitest (pure digest) can't
// reach. Runs against a live stack (see playwright.config.ts).

import path from "node:path";

import { expect, test } from "@playwright/test";

// One static example site doubles as the deploy fixture. synthwave-sunset is flat
// (index.html + style.css), so the uploaded paths are unambiguous.
const FIXTURE_DIR = path.resolve(process.cwd(), "../../examples/synthwave-sunset");

test("new user: sign up → org → site → drag-and-drop deploy goes live", async ({
  page,
}) => {
  const stamp = Date.now();
  const email = `e2e-${stamp}@example.com`;
  const password = "e2e-password-123";
  const siteSlug = `e2e-site-${stamp}`;

  // 1. Sign up with email/password. Verification is off locally, so this lands us
  //    signed in (the form navigates to /dashboard on success).
  await page.goto("/sign-up");
  await page.locator("#name").fill("E2E Tester");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  // Consent to the Terms is required before the sign-up button enables.
  await page.locator("#agree-terms").check();
  await page.getByRole("button", { name: "Sign up with email" }).click();

  // 2. A new user has no org, so the (app) layout redirects to onboarding.
  await page.waitForURL(/\/onboarding/, { timeout: 30_000 });
  await page.locator("#org-name").fill(`E2E Org ${stamp}`);
  await page.getByRole("button", { name: "Create organization" }).click();

  // 3. Org created + set active → the dashboard.
  await page.waitForURL(/\/dashboard/, { timeout: 30_000 });

  // 4. Create a site → its detail page.
  await page.getByRole("button", { name: "New site" }).first().click();
  await page.locator("#site-slug").fill(siteSlug);
  await page.getByRole("button", { name: "Create site" }).click();
  await page.waitForURL(/\/sites\/[0-9a-f-]+/, { timeout: 30_000 });
  await expect(page.getByText("Not deployed")).toBeVisible();

  // 5. Deploy: set the dropzone's hidden folder input to the example directory.
  //    This is the same path the "Choose folder" picker triggers (collectInputFiles
  //    → buildManifest → prepare → direct blob upload → finalize → publish).
  const fileInput = page.locator('input[type="file"]');
  await fileInput.setInputFiles(FIXTURE_DIR);

  // 6. Drop → live: the dropzone reaches its success state with the live URL.
  await expect(page.getByText("Deployed and live.")).toBeVisible({
    timeout: 90_000,
  });

  // The site's live URL is now present (both in the dropzone success state and the
  // page's "Live URL" card, which router.refresh() repopulated). The content host is
  // ORG-NAMESPACED (<orgSlug>--<slug>.<domain>) and the domain/scheme/port vary by
  // environment (localhost in dev, the content domain in prod), so match the
  // org-namespaced slug rather than a fixed URL. The badge also flips Live.
  await expect(
    page.locator(`a[href*="--${siteSlug}."]`).first(),
  ).toBeVisible();
  await expect(page.getByText("Live", { exact: true })).toBeVisible();
});
