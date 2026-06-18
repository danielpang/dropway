// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Regression: a RETURNING user who just signs in (no onboarding) must reach a
// working dashboard. Better Auth only sets the session's active org on org
// create/switch, so without the session.create backfill (lib/auth.ts) a fresh
// sign-in has no active org → the minted JWT carries org_id="" → the Go API 500s
// on /v1/sites ("claims missing org_id" for RLS) and the dashboard shows the
// "The API returned 500" banner. This guards that exact path.

import { expect, test } from "@playwright/test";

test("returning user: sign in (no onboarding) reaches a working dashboard", async ({
  page,
  context,
}) => {
  const stamp = Date.now();
  const email = `returning-${stamp}@example.com`;
  const password = "returning-pass-123";

  // First visit: sign up + onboard an org, so the user has a membership.
  await page.goto("/sign-up");
  await page.locator("#name").fill("Returning User");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: "Sign up with email" }).click();
  await page.waitForURL(/\/onboarding/, { timeout: 30_000 });
  await page.locator("#org-name").fill(`Returning Org ${stamp}`);
  await page.getByRole("button", { name: "Create organization" }).click();
  await page.waitForURL(/\/dashboard/, { timeout: 30_000 });

  // Simulate a RETURNING user: drop the session entirely and sign in fresh — this
  // creates a NEW session that never went through onboarding. The session.create
  // hook must backfill its active org so the JWT still carries org_id.
  await context.clearCookies();
  await page.goto("/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: "Sign in with email" }).click();
  await page.waitForURL(/\/dashboard/, { timeout: 30_000 });

  // No control-plane error banner — the /v1/sites call carried org_id and succeeded.
  await expect(page.getByText(/The API returned/i)).toHaveCount(0);
  await expect(page.getByText(/Start the API/i)).toHaveCount(0);
  // The sites surface rendered (proves /v1/sites returned 200, not 500).
  await expect(
    page.getByRole("button", { name: "New site" }).first(),
  ).toBeVisible();
});
