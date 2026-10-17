import type { NextConfig } from "next";

// Security response headers applied to every dashboard route. These render
// authenticated org data, the OAuth "Authorize MCP access" consent screen, and
// the platform password gate, so the priority is anti-clickjacking plus the
// standard hardening headers.
//
// The CSP here deliberately sets ONLY the directives that are safe without a
// per-request nonce: frame-ancestors (the clickjacking control, which governs who
// may frame us and does NOT restrict script/style/connect at all), plus
// object-src, base-uri, and form-action. A full nonce-based script-src/connect-src
// CSP needs middleware to thread a nonce through Next's inline hydration scripts
// and is tracked as a follow-up; restricting script-src without it would break the
// app.
const securityHeaders = [
  {
    key: "Content-Security-Policy",
    value: [
      "frame-ancestors 'none'",
      "object-src 'none'",
      "base-uri 'self'",
      "form-action 'self'",
    ].join("; "),
  },
  // Legacy backstop for clients that ignore CSP frame-ancestors.
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  // Ignored by browsers over plain http (local dev), enforced on the https origin.
  // Note: `preload` only gets a domain onto the browser preload list when the
  // header is also served from the bare apex (https://dropway.dev). Served from the
  // app subdomain it still asserts HSTS for app.dropway.dev and its subdomains; the
  // tenant content domain is a separate apex (dropwaycontent.com), so
  // includeSubDomains never reaches tenant sites.
  {
    key: "Strict-Transport-Security",
    value: "max-age=63072000; includeSubDomains; preload",
  },
  // No feature this app uses; deny the high-risk ones by default.
  {
    key: "Permissions-Policy",
    value: "camera=(), microphone=(), geolocation=(), browsing-topics=()",
  },
];

const nextConfig: NextConfig = {
  reactStrictMode: true,
  async headers() {
    return [{ source: "/:path*", headers: securityHeaders }];
  },
  // The dashboard talks to the Go API for all business data; never to Postgres
  // directly (Better Auth is the only TS->PG path, for its own identity tables).
  // `pg` is a Node-only dependency used by Better Auth's adapter on the server.
  serverExternalPackages: ["pg"],
  // Expose a single literal `ENVIRONMENT` var to the BROWSER bundle (Next only
  // auto-exposes NEXT_PUBLIC_*). Used as the analytics `environment` property on
  // both server and client. Resolved at build time; defaults to NODE_ENV so a
  // plain build still reports "production" / "development".
  env: {
    ENVIRONMENT:
      process.env.ENVIRONMENT || process.env.NODE_ENV || "development",
  },
};

export default nextConfig;
