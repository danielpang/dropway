// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Minimal vitest setup for the dashboard's PURE lib logic. The dashboard is
// mostly RSC/server components (not unit-tested here — there is no DOM runner),
// but the framework-free helpers in lib/ ARE worth covering: the 402 quota
// narrowing (lib/api.ts), the billing ladder/matrix (lib/billing.ts), the
// account-state gate (lib/billing-server.ts), the audit highlight matcher
// (lib/audit.ts), the /authz host+redirect validation (lib/authz-host.ts), and
// the className merge (lib/utils.ts).
//
// Two aliases let these modules import under node without the Next runtime:
//   - `server-only` → a no-op stub (the bare `import "server-only"` marker).
//   - `@/lib/auth`  → a stub (the real module opens a live pg Pool at import).
// The order matters: the specific `@/lib/auth` alias is listed BEFORE the
// catch-all `@/` so it wins. `next/headers` is import-safe (its functions only
// throw when CALLED outside a request — the pure exports under test never call
// them), so it needs no stub.

import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const root = fileURLToPath(new URL("./", import.meta.url));

export default defineConfig({
  resolve: {
    alias: [
      { find: "server-only", replacement: fileURLToPath(new URL("./test/stubs/server-only.ts", import.meta.url)) },
      { find: "@/lib/auth", replacement: fileURLToPath(new URL("./test/stubs/auth.ts", import.meta.url)) },
      // Catch-all for the remaining `@/...` path imports → the dashboard root.
      { find: /^@\/(.*)$/, replacement: `${root}$1` },
    ],
  },
  test: {
    environment: "node",
    include: ["test/**/*.test.ts"],
  },
});
