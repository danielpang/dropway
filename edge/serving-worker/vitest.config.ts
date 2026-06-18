// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The current suite (test/serve.test.ts) drives the serving/path-resolution
// logic through in-memory KV/R2 mocks, so it is runtime-agnostic and runs on the
// plain vitest node pool — no workerd needed. When we add tests that need real
// KV/R2 bindings we'll reintroduce `@cloudflare/vitest-pool-workers` with the
// API matching the pinned version (its `/config` subpath was removed in the
// vitest-v4-era 0.16.x line, which is why this uses the standard config).
//
// `@dropway/contracts` is a workspace package; we alias it straight to its
// TypeScript source so the suite (and the bundler) resolve the one cross-language
// contract without a build step or `node_modules` link. This mirrors how Wrangler
// bundles the workspace dependency at deploy time.

import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: {
      "@dropway/contracts": fileURLToPath(
        new URL("../../contracts/src/index.ts", import.meta.url),
      ),
    },
  },
  test: {
    environment: "node",
    include: ["test/**/*.test.ts"],
  },
});
