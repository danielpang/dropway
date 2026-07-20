// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Live SDK lifecycle check: create a site, deploy a directory, verify it serves,
// then delete it and confirm it's gone. Exits non-zero on any failed assertion.
// Imports the built dist, so run `pnpm --filter @dropway/sdk build` first.
//
//   DROPWAY_API_KEY=dw_live_... node packages/sdk/e2e/lifecycle.mjs

import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { Dropway, NotFoundError } from "../dist/index.js";

const FIXTURE = fileURLToPath(
  new URL("../../../examples/synthwave-sunset", import.meta.url),
);
const slug = process.env.E2E_SLUG ?? `sdk-e2e-${Date.now()}`;

const dw = new Dropway({ baseUrl: process.env.DROPWAY_API ?? undefined });

const site = await dw.sites.create({ slug });
console.log(`created ${slug} (${site.id})`);

const res = await dw.sites.deploy(site.id, { dir: FIXTURE });
assert.equal(res.published, true, "deploy did not publish");
assert.ok(res.liveUrl, "deploy returned no live URL");
assert.equal(res.filesUploaded, 2, "expected 2 files uploaded"); // index.html + style.css
console.log(`deployed v${res.versionNo ?? res.versionId} -> ${res.liveUrl}`);

await waitFor(res.liveUrl, 200, "live URL never served 200");
console.log("live URL serves 200");

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

/** Poll url until it returns want, or throw after ~60s. */
async function waitFor(url, want, msg) {
  for (let i = 0; i < 20; i++) {
    const code = await fetch(url)
      .then((r) => r.status)
      .catch(() => 0);
    if (code === want) return;
    await new Promise((r) => setTimeout(r, 3000));
  }
  throw new Error(msg);
}
