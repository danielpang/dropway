// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { Dropway, DEFAULT_BASE_URL } from "../dist/index.js";
import { cleanupRunSites } from "./cleanup-sites.mjs";

const dw = new Dropway({
  baseUrl: process.env.DROPWAY_API || DEFAULT_BASE_URL,
  timeoutMs: 10000,
  maxRetries: 1,
});
await cleanupRunSites(dw, [process.env.E2E_SDK_SLUG, process.env.E2E_CLI_SLUG]);
