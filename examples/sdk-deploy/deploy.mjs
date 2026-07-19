// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Deploy a folder of static files to a live Dropway site with the SDK.
//
//   DROPWAY_API_KEY=dw_live_... node deploy.mjs
//
// Reads the key from DROPWAY_API_KEY (create one under Settings -> API keys),
// creates a site, deploys the ../synthwave-sunset fixture, and prints the live
// URL. Re-running reuses the same slug: create 400s on the duplicate, which we
// treat as "already exists" and deploy a new version instead.

import { fileURLToPath } from "node:url";
import { Dropway, DropwayError } from "@dropway/sdk";

const SLUG = process.env.SITE_SLUG ?? "sdk-example";
const FIXTURE = fileURLToPath(new URL("../synthwave-sunset", import.meta.url));

const dw = new Dropway({
  // apiKey defaults to process.env.DROPWAY_API_KEY.
  // baseUrl defaults to the hosted API; override for self-host:
  baseUrl: process.env.DROPWAY_API ?? undefined,
});

async function ensureSite(slug) {
  try {
    const site = await dw.sites.create({ slug });
    console.log(`Created site "${slug}" (${site.id})`);
    return site;
  } catch (err) {
    // A duplicate slug is a 400 — the site already exists, so find and reuse it.
    if (err instanceof DropwayError && err.status === 400) {
      const existing = (await dw.sites.list()).find((s) => s.slug === slug);
      if (existing) {
        console.log(`Reusing existing site "${slug}" (${existing.id})`);
        return existing;
      }
    }
    throw err;
  }
}

const site = await ensureSite(SLUG);

const result = await dw.sites.deploy(site.id, { dir: FIXTURE });

console.log(
  `Deployed version ${result.versionNo ?? result.versionId} ` +
    `(${result.filesUploaded} file(s) uploaded).`,
);
console.log(`Live at: ${result.liveUrl}`);
