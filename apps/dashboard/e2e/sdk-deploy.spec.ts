// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// End-to-end: mint an org-scoped API key, then drive @dropway/sdk against the
// live Go API through the full prepare -> upload -> finalize -> publish loop.
// Covers what the browser deploy spec can't: the SDK's own transport and the
// directory-walk deploy path (`{ dir }`) that the example and CI smoke both use.

import path from "node:path";

import { expect, test } from "@playwright/test";

import { Dropway } from "@dropway/sdk";

const FIXTURE_DIR = path.resolve(process.cwd(), "../../examples/synthwave-sunset");
const API_URL = process.env.E2E_API_URL ?? "http://localhost:8080";

test("mint an API key, then deploy a directory via the SDK to a live version", async ({
  page,
}) => {
  const stamp = Date.now();

  // Sign up + create an org so the session carries an active org (the key
  // inherits it). Mirrors deploy.spec.ts; verification is off locally.
  await page.goto("/sign-up");
  await page.locator("#name").fill("SDK Tester");
  await page.locator("#email").fill(`e2e-sdk-${stamp}@example.com`);
  await page.locator("#password").fill("e2e-password-123");
  await page.locator("#agree-terms").check();
  await page.getByRole("button", { name: "Sign up with email" }).click();

  await page.waitForURL(/\/onboarding/, { timeout: 30_000 });
  await page.locator("#org-name").fill(`SDK Org ${stamp}`);
  await page.getByRole("button", { name: "Create organization" }).click();
  await page.waitForURL(/\/dashboard/, { timeout: 30_000 });

  // The jwt() plugin mints an EdDSA JWT from the session cookie; page.request
  // shares the browser's cookie jar, so this is the signed-in caller.
  const tokenRes = await page.request.get("/api/auth/token");
  expect(tokenRes.ok()).toBe(true);
  const { token } = (await tokenRes.json()) as { token: string };

  // Mint a key with that JWT — the same POST /v1/api-keys the dashboard uses.
  const keyRes = await page.request.post(`${API_URL}/v1/api-keys`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { name: "e2e-sdk" },
  });
  expect(keyRes.ok()).toBe(true);
  const { key } = (await keyRes.json()) as { key: string };
  expect(key).toMatch(/^dw_live_/);

  // Deploy the fixture directory with the key alone — no browser, no session.
  const dw = new Dropway({ apiKey: key, baseUrl: API_URL });
  const slug = `e2e-sdk-${stamp}`;
  const site = await dw.sites.create({ slug });
  const res = await dw.sites.deploy(site.id, { dir: FIXTURE_DIR });

  expect(res.published).toBe(true);
  expect(res.liveUrl).toBeTruthy();
  expect(res.versionId).toBeTruthy();
  expect(res.filesUploaded).toBe(2); // index.html + style.css

  // Publish flipped the live pointer server-side.
  const fetched = await dw.sites.get(site.id);
  expect(fetched.current_version_id).toBe(res.versionId);
});
