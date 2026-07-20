<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# @dropway/sdk

The official TypeScript SDK for [Dropway](https://dropway.dev). Create and deploy
sites over the `/v1` API with an org-scoped API key — no browser, no interactive
login. Built for CI, server scripts, and automation.

- **Zero runtime dependencies.** Uses the built-in `fetch` and `node:crypto`.
- **Node ≥ 18** (ESM).
- **Typed errors**, per-key rate-limit awareness, and automatic retries for
  idempotent requests.

## Install

```sh
npm install @dropway/sdk
```

## Authenticate

Create an org-scoped key in the dashboard (**Settings → API keys**) and set it as
`DROPWAY_API_KEY`. The SDK reads it automatically:

```ts
import { Dropway } from "@dropway/sdk";

const dw = new Dropway(); // reads process.env.DROPWAY_API_KEY
// or: new Dropway({ apiKey: "dw_live_..." })
```

A key acts as the member who created it and is limited to member-level actions —
it can create and deploy sites, but not administer the organization.

## Deploy a site

```ts
const site = await dw.sites.create({ slug: "launch-page" });

const { liveUrl } = await dw.sites.deploy(site.id, {
  files: {
    "index.html": "<!doctype html><h1>Hello from CI</h1>",
    "styles.css": "h1 { font-family: system-ui }",
  },
});

console.log(`Live at ${liveUrl}`);
```

Or deploy a built directory (Node only):

```ts
await dw.sites.deploy(site.id, { dir: "./dist" });
```

`deploy()` hashes each file, uploads only the blobs the server doesn't already
have (directly to object storage), finalizes (the server re-verifies every byte),
and publishes. Pass `publish: false` to stage a version without going live, then
`dw.sites.publish(site.id, { versionId })` later (also how you roll back).

## Delete a site

```ts
await dw.sites.delete(site.id);
```

Permanently removes the site and every version. You can delete a site you own;
deleting someone else's needs an org admin. Irreversible.

## Errors

Non-2xx responses throw typed errors:

```ts
import { QuotaExceededError, RateLimitError } from "@dropway/sdk";

try {
  await dw.sites.create({ slug: "x" });
} catch (err) {
  if (err instanceof QuotaExceededError) {
    console.error(`Site cap reached (${err.current}/${err.max} on ${err.planTier}). Upgrade: ${err.upgradeUrl}`);
  } else if (err instanceof RateLimitError) {
    console.error(`Rate limited; retry after ${err.retryAfterSeconds}s`);
  }
}
```

`AuthError` (401), `ForbiddenError` (403, with `interactiveRequired` for the
member-level ceiling), `NotFoundError` (404), and the base `DropwayError` cover the
rest.

## Any endpoint

The `sites` namespace covers the deploy flow; anything else in the API is one call
away via the escape hatch:

```ts
const { keys } = await dw.request<{ keys: unknown[] }>("GET", "/v1/api-keys");
```

## GitHub Actions

```yaml
- run: node deploy.mjs
  env:
    DROPWAY_API_KEY: ${{ secrets.DROPWAY_API_KEY }}
```

## Self-host

```ts
new Dropway({ apiKey, baseUrl: "https://api.your-dropway.example" });
```
