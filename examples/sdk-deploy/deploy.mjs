// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Deploy ../synthwave-sunset to a live Dropway site with the SDK:
//   DROPWAY_API_KEY=dw_live_... node deploy.mjs

import { fileURLToPath } from "node:url";
import { Dropway, DropwayError } from "@dropway/sdk";

const SLUG = process.env.SITE_SLUG ?? "sdk-example";
const FIXTURE = fileURLToPath(new URL("../synthwave-sunset", import.meta.url));

// apiKey defaults to DROPWAY_API_KEY; baseUrl to the hosted API (override for self-host).
const dw = new Dropway({ baseUrl: process.env.DROPWAY_API ?? undefined });

// Re-running reuses the slug: a duplicate 400s, so find and deploy over it.
async function ensureSite(slug) {
  try {
    const site = await dw.sites.create({ slug });
    console.log(`Created site "${slug}" (${site.id})`);
    return site;
  } catch (err) {
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
