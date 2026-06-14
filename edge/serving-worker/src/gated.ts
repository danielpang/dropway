// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Gated serve orchestration — wires the (pure-ish) authz primitives in authz.ts
// to the manifest→blob serving the public path already does, for the three
// identity-gated access modes (password | allowlist | org_only). Kept separate
// from index.ts so the public path stays a clean, JWT-free read and this module
// owns ALL of the edge-token / cookie / redirect handling.
//
// Two entry points share this code:
//   1. A normal gated content request → verify the __Host-edge cookie; serve
//      (private, never shared-cached) or 302 to the dashboard /authz exchange.
//   2. GET /__authz/callback?token=&next=  → verify the freshly minted token,
//      Set-Cookie __Host-edge, 302 to a SAFE same-host next path.

import type { Env } from "./index";
import type { RouteValue } from "./route";
import type { GatedConfig } from "./config";
import type { FetchLike } from "./edgetoken";
import {
  asPrivate,
  authorizeGated,
  handleAuthzCallback,
  isAuthzCallback,
  redirectToAuthz,
} from "./authz";
import { type RevokedKVLike, isRevoked } from "./revoke";

/** Resolved gated-path inputs (config + injected fetch + clock). */
export interface GateOpts {
  cfg: GatedConfig;
  fetchImpl: FetchLike;
  now: number;
}

/** How the gated path streams the protected bytes once a viewer is authorized. */
export interface GatedContent {
  /**
   * Build the success Response for the resolved request (manifest→blob), with
   * the SAME content headers the public path would use. The gated wrapper then
   * OVERRIDES Cache-Control to `private, no-store` so protected bytes never enter
   * a shared cache (§10). For HEAD the body is already stripped by the caller.
   */
  serveContent: () => Promise<Response>;
  /**
   * KV backing the hard-revocation denylist (reuses ROUTES with the `revoked:`
   * prefix, or a dedicated REVOKED binding). Checked AFTER token verification.
   */
  revokedKV: RevokedKVLike;
  /** The route's org_id — the org dimension of the denylist check. */
  orgId: string;
}

/**
 * Serve a request whose route is password/allowlist/org_only.
 *
 *  - `/__authz/callback` → verify the minted token, set the cookie, safe-redirect.
 *  - otherwise → verify the `__Host-edge` cookie against THIS route (aud==host,
 *    site_id match, EdDSA, iss, exp). Valid → serve privately. Invalid/absent →
 *    302 to the dashboard `/authz` exchange.
 *
 * The Worker NEVER reads the operator Better Auth JWT here — only the edge token.
 */
export async function serveGated(
  request: Request,
  _env: Env,
  route: RouteValue,
  url: URL,
  opts: GateOpts,
  content: GatedContent,
): Promise<Response> {
  // The post-mint callback is the one gated path that accepts a token in the URL
  // (the dashboard 302'd the viewer here). It is fully re-verified before we set
  // the cookie, so a forged/stale ?token= simply bounces back to the exchange.
  if (isAuthzCallback(url.pathname)) {
    return handleAuthzCallback(request, route, url, opts.cfg, opts.fetchImpl, opts.now);
  }

  const claims = await authorizeGated(
    request,
    route,
    url,
    opts.cfg,
    opts.fetchImpl,
    opts.now,
  );
  if (claims === null) {
    // No/invalid edge cookie → bounce to the dashboard exchange. The `next` is
    // the path+query the viewer wanted, returned to the content-host callback.
    const nextPath = url.pathname + url.search;
    return redirectToAuthz(opts.cfg, url.host, nextPath);
  }

  // HARD REVOCATION (§6 contract, Phase 4): the token verified, but a ban /
  // unshare / org-suspension may have invalidated it before its 15m exp. Consult
  // the KV denylist (revoked:user:<sub> / site:<site_id> / org:<orgId>); if any
  // min_iat > token.iat, treat the token as invalid → re-auth via /authz. The
  // org dimension comes from the ROUTE (authoritative), not a token claim. Fails
  // CLOSED (a denylist read error → revoked → 302), per §10.
  const revoked = await isRevoked(content.revokedKV, {
    sub: claims.sub,
    siteId: claims.site_id,
    orgId: content.orgId,
    iat: claims.iat,
  });
  if (revoked) {
    const nextPath = url.pathname + url.search;
    return redirectToAuthz(opts.cfg, url.host, nextPath);
  }

  // Authorized. Serve the protected bytes, but FORCE private/no-store so the
  // public Cache API (and any shared cache) never holds them.
  const response = await content.serveContent();
  // Only rewrite caching on a real content body; a 404 from the resolver is
  // already non-sensitive but we keep it private too (consistent, no leak).
  const headers = asPrivate(new Headers(response.headers));
  return new Response(response.body, { status: response.status, headers });
}
