<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Deploy a site with the Dropway SDK

A minimal, runnable example of [`@dropway/sdk`](../../packages/sdk): create a
site and deploy a folder of static files to a live URL, authenticated with an
org-scoped API key.

## Run it

1. Create an API key in the dashboard under **Settings → API keys** and copy it
   (it is shown only once).
2. Install the SDK and run the script:

   ```sh
   npm install @dropway/sdk
   DROPWAY_API_KEY=dw_live_... node deploy.mjs
   ```

It creates a site with the slug `sdk-example` and deploys the
[`../synthwave-sunset`](../synthwave-sunset) fixture, then prints the live URL.
Re-running deploys a new version of the same site.

Environment variables:

| Variable | Purpose |
| --- | --- |
| `DROPWAY_API_KEY` | **Required.** The org-scoped key (`dw_live_...`). |
| `SITE_SLUG` | Optional. Override the site slug (default `sdk-example`). |
| `DROPWAY_API` | Optional. API base URL for self-host (defaults to the hosted API). |

## In CI

Store the key as a repository secret and set it as `DROPWAY_API_KEY` in the job
environment — the SDK picks it up automatically. See
[`.github/workflows/sdk-smoke.yml`](../../.github/workflows/sdk-smoke.yml) for a
working GitHub Actions example.
