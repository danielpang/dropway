// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Worker configuration for the Phase-2 gated serving path (docs/ARCHITECTURE.md
// §6, edge-token spec). Everything the gated path needs that is environment- or
// deployment-specific lives here so the serving logic stays pure and the values
// are injected (env vars / wrangler vars) — never hard-coded secrets.
//
// The Worker verifies the host-scoped EDGE TOKEN (a SEPARATE EdDSA keypair from
// Better Auth's user JWT) against the Go API's edge JWKS. It NEVER reads the
// operator dashboard JWT; the public path stays JWT-free.

import type { Env } from "./index";

/**
 * The fixed `iss` claim of every edge token — the Go API "edge signer". Pinned
 * on verify (mirrors internal/edgetoken.Issuer on the Go side). A token with any
 * other issuer is rejected.
 */
export const EDGE_TOKEN_ISSUER = "https://api.shipped.app/edge" as const;

/**
 * The host-only cookie that carries the edge token on the CONTENT host. The
 * `__Host-` prefix forces host-only + Secure + no `Domain=`, so a sibling tenant
 * on `*.shippedusercontent.com` can never set/overwrite it (cookie tossing) —
 * the PSL separation is the load-bearing isolation, this is defense in depth.
 */
export const EDGE_COOKIE_NAME = "__Host-edge" as const;

/** Default dashboard authz origin used when APP_AUTHZ_URL is unset. */
const DEFAULT_APP_AUTHZ_URL = "https://app.shipped.app/authz";

/** Default edge JWKS endpoint used when EDGE_JWKS_URL is unset. */
const DEFAULT_EDGE_JWKS_URL = "https://api.shipped.app/.well-known/edge-jwks";

/**
 * Resolved, validated gated-path configuration. Built once per request from the
 * Worker `Env` (wrangler `[vars]` / secrets). Falling back to the production
 * defaults keeps the Worker functional even if a var is briefly missing, but a
 * deployment SHOULD set both explicitly (see wrangler.toml / deploy/.env.example).
 */
export interface GatedConfig {
  /** Where to fetch the edge signer's public JWKS (OKP/Ed25519). */
  jwksUrl: string;
  /** Dashboard `/authz` exchange origin to 302 unauthenticated viewers to. */
  appAuthzUrl: string;
  /** Pinned issuer of the edge token. */
  issuer: string;
}

/** Read a string var off the Env, trimming and treating "" as unset. */
function readVar(env: Env, key: "EDGE_JWKS_URL" | "APP_AUTHZ_URL"): string | undefined {
  const v = env[key];
  if (typeof v !== "string") return undefined;
  const t = v.trim();
  return t === "" ? undefined : t;
}

/**
 * Build the gated-path config from the Worker environment. `EDGE_JWKS_URL` and
 * `APP_AUTHZ_URL` are configurable (per-environment); both default to the
 * production origins so a misconfigured preview still fails safe rather than
 * leaking content.
 */
export function gatedConfig(env: Env): GatedConfig {
  return {
    jwksUrl: readVar(env, "EDGE_JWKS_URL") ?? DEFAULT_EDGE_JWKS_URL,
    appAuthzUrl: readVar(env, "APP_AUTHZ_URL") ?? DEFAULT_APP_AUTHZ_URL,
    issuer: EDGE_TOKEN_ISSUER,
  };
}
