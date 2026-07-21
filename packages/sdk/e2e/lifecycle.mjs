// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Live SDK lifecycle check: create a site, deploy a directory, verify it serves,
// then delete it and confirm it's gone. Exits non-zero on any failed assertion.
// Imports the built dist, so run `pnpm --filter @dropway/sdk build` first.
//
//   DROPWAY_API_KEY=dw_live_... node packages/sdk/e2e/lifecycle.mjs

import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { Dropway, DEFAULT_BASE_URL, NotFoundError } from "../dist/index.js";

const FIXTURE = fileURLToPath(
  new URL("../../../examples/synthwave-sunset", import.meta.url),
);
const slug = process.env.E2E_SLUG ?? `sdk-e2e-${Date.now()}`;

// Explicit default: DROPWAY_API is set to "" (not unset) when the environment
// has no such secret, so `||` falls back to a concrete base URL rather than
// passing an empty string (which would throw) or undefined.
const dw = new Dropway({ baseUrl: process.env.DROPWAY_API || DEFAULT_BASE_URL });

// Create it public: the org's default visibility is org_only, which the edge
// gates behind auth, so the live URL would never serve the fixture below.
const site = await dw.sites.create({ slug, accessMode: "public" });
console.log(`created ${slug} (${site.id})`);

const res = await dw.sites.deploy(site.id, { dir: FIXTURE });
assert.equal(res.published, true, "deploy did not publish");
assert.ok(res.liveUrl, "deploy returned no live URL");
assert.ok(res.versionId, "deploy returned no version id");
// NOT asserting filesUploaded: blobs are content-addressed and shared per org,
// so a reused org already has the fixture's blobs and uploads 0. The deploy
// still publishes; serving the live URL below is the real proof of content.
console.log(
  `deployed v${res.versionNo ?? res.versionId} -> ${res.liveUrl} ` +
    `(${res.filesUploaded} new blob(s))`,
);

// Verify BOTH fixture files actually deployed and serve — a content check that
// (unlike the upload count) doesn't depend on whether the org already had the
// blobs. Root serves index.html with its title; style.css serves 200.
await waitForServed(res.liveUrl, "Synthwave Sunset");
const cssUrl = res.liveUrl.replace(/\/$/, "") + "/style.css";
assert.equal(await statusOf(cssUrl), 200, "style.css was not served");
console.log("live URL serves index.html + style.css");

await dw.sites.delete(site.id);
console.log("deleted");

// Confirm deletion: the site is gone from the control plane (authoritative).
await assert.rejects(
  () => dw.sites.get(site.id),
  NotFoundError,
  "site still resolves after delete",
);
assert.equal(
  (await dw.sites.list()).some((s) => s.id === site.id),
  false,
  "deleted site still appears in list",
);
console.log("confirmed: site no longer exists");

/** Wait until url serves 200 with a body containing marker (throws after ~60s).
 *  Tolerates edge propagation lag right after publish. */
async function waitForServed(url, marker) {
  for (let i = 0; i < 20; i++) {
    const body = await fetch(url)
      .then((r) => (r.ok ? r.text() : null))
      .catch(() => null);
    if (body?.includes(marker)) return;
    await new Promise((r) => setTimeout(r, 3000));
  }
  throw new Error(`live URL never served a body containing "${marker}"`);
}

/** One-shot HTTP status for url (0 on network error). */
async function statusOf(url) {
  return fetch(url)
    .then((r) => r.status)
    .catch(() => 0);
}
