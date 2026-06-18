import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // The dashboard talks to the Go API for all business data; never to Postgres
  // directly (Better Auth is the only TS->PG path, for its own identity tables).
  // `pg` is a Node-only dependency used by Better Auth's adapter on the server.
  serverExternalPackages: ["pg"],
};

export default nextConfig;
