import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
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
