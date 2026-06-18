// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Test stub for the `server-only` marker package. In a Next.js build `import
// "server-only"` throws if a module is pulled into a CLIENT bundle; under vitest
// there is no client/server split, so we alias it to this no-op so server-tagged
// lib modules (lib/api.ts, lib/audit.ts, lib/billing-server.ts) can be imported
// to unit-test their PURE exports without dragging in the Next runtime.
export {};
