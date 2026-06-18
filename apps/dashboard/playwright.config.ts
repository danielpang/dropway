// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Playwright E2E config for the dashboard. These specs drive a REAL browser
// against a running full stack (dashboard + Go API + Postgres + MinIO), so they
// cover the one layer unit tests can't: the browser DOM + React (e.g. the folder
// drag-and-drop deploy component, end to end to a live site).
//
// They are NOT part of `pnpm test` (vitest) — run them with `pnpm test:e2e`
// against an already-running stack:
//   docker compose -f ../../deploy/docker-compose.yml --env-file ../../deploy/.env up -d --build
//   pnpm test:e2e
// Override the target with E2E_BASE_URL.

import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  // The deploy flow (hash → prepare → upload → finalize → publish) is multi-step;
  // give specs real headroom rather than chasing flakes.
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: "list",
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:3000",
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
