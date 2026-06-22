// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Verifies the dashboard ships the security response headers (M6): the CSP
// frame-ancestors clickjacking control on the OAuth consent / password-gate
// pages, plus the standard hardening headers. next.config.ts only type-imports
// `next` (erased at runtime), so its default export loads cleanly under node.

import { describe, expect, it } from "vitest";

import nextConfig from "../next.config";

async function headerMap() {
  const groups = await nextConfig.headers!();
  // All security headers are applied under the catch-all route group.
  const all = groups.find((g) => g.source === "/:path*");
  expect(all).toBeDefined();
  return new Map(all!.headers.map((h) => [h.key, h.value]));
}

describe("dashboard security headers", () => {
  it("blocks framing via CSP frame-ancestors and X-Frame-Options", async () => {
    const h = await headerMap();
    expect(h.get("Content-Security-Policy")).toContain("frame-ancestors 'none'");
    expect(h.get("X-Frame-Options")).toBe("DENY");
  });

  it("sets the standard hardening headers", async () => {
    const h = await headerMap();
    expect(h.get("X-Content-Type-Options")).toBe("nosniff");
    expect(h.get("Referrer-Policy")).toBe("strict-origin-when-cross-origin");
    expect(h.get("Strict-Transport-Security")).toContain("max-age=");
    expect(h.get("Permissions-Policy")).toContain("geolocation=()");
  });

  it("hardens the CSP base-uri/object-src/form-action without restricting scripts", async () => {
    const csp = (await headerMap()).get("Content-Security-Policy")!;
    expect(csp).toContain("object-src 'none'");
    expect(csp).toContain("base-uri 'self'");
    expect(csp).toContain("form-action 'self'");
    // No nonce-less script-src restriction (would break Next's inline hydration).
    expect(csp).not.toContain("script-src");
  });
});
