// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Dropway serving Worker — *.dropwaycontent.com (Cloudflare Workers, Module
// syntax). Phase 1 implements the PUBLIC serve path only: the 95% case that is
// JWT-free and cacheable.
//
// Request lifecycle (public), content-addressed layout:
//   browser → PoP → resolve `route:<host>` from KV (ROUTES) →
//   if access_mode === "public":
//     fetch the deploy manifest from R2 at
//       manifests/<org_id>/<site_id>/<version_id>.json
//     resolve the request path (index.html + directory fallback) to its
//       { sha256, content_type }, then stream the blob from R2 at
//       blobs/<org_id>/<sha256> with the right Content-Type + Cache-Control
//       (immutable for hashed assets, short for HTML); 404 page otherwise.
//
// The public path NEVER reads a JWT. Identity-gated modes
// (password|allowlist|org_only) are Phase-2 stubs that return a clearly-marked
// 501 (see `gatedStub`) and DO NOT exchange identity here — that is the
// host-scoped `/authz` exchange on app.dropway.dev, built in Phase 2.

import {
  type RouteValue,
  SUPPORTED_SCHEMA_VERSION,
  cleanPath,
  diagnoseRouteParseFailure,
  isRouteExpired,
  parseRouteValue,
  routeKey,
} from "./route";
import {
  type Manifest,
  NOT_FOUND_PATH,
  blobKey,
  manifestKey,
  parseManifest,
  resolveManifestEntry,
} from "./manifest";
import { embedResponseHeaders, publicResponseHeaders, securityHeaders } from "./http";
import {
  EMBED_BADGE_BYTE_LENGTH,
  embedGatePlaceholder,
  injectEmbedBadge,
  isEmbedRequested,
  isInjectableEmbedBadge,
  shouldShowEmbedBadge,
} from "./embed";
import { directoryPrefix, listDirectory, renderDirectoryListing } from "./listing";
import {
  MARKDOWN_MAX_RENDER_BYTES,
  isMarkdownPath,
  renderMarkdownPage,
} from "./markdown";
import {
  CONTENT_CSP,
  applyHeaders,
  isServiceWorkerRequest,
  isServiceWorkerScript,
  platformSecurityHeaders,
} from "./security";
import {
  CRAWLER_BLOCKED_BODY,
  isAICrawler,
  llmsTxtBody,
  robotsTxtBody,
} from "./llm";
import { type GatedConfig, gatedConfig } from "./config";
import type { FetchLike } from "./edgetoken";
import { serveGated } from "./gated";
import {
  type CounterKVLike,
  DEFAULT_RATE_LIMIT,
  type RateLimitPolicy,
  type RateLimiterLike,
  type StatusKVLike,
  isBlockingStatus,
  rateLimitDecision,
  rateLimitIdentity,
  rateLimitNative,
  readOrgStatus,
} from "./ratelimit";
import type { RevokedKVLike } from "./revoke";
import { type AnalyticsEnv, captureServe404, captureSiteVisit } from "./analytics";
import { captureException } from "./errtrack";
import {
  BANNER_BYTE_LENGTH,
  injectBanner,
  isInjectableContentType,
  shouldInjectBanner,
} from "./banner";
import {
  CHAT_PILL_BYTE_LENGTH,
  CHAT_RESERVED_PATH,
  chatTranscriptKey,
  injectChatPill,
  renderChatPage,
  shouldInjectChatPill,
} from "./chat";

// --- Binding interfaces -----------------------------------------------------
// Narrow structural types over the R2/KV bindings, so the serving logic can be
// driven by in-memory mocks in tests without @cloudflare/workers-types runtime.

/** Minimal R2 object shape we consume (subset of R2ObjectBody). */
export interface R2ObjectLike {
  body: ReadableStream | null;
  httpEtag?: string;
  uploaded?: Date;
  size?: number;
  /** Present on R2ObjectBody; lets us read the manifest JSON in one call. */
  json?: () => Promise<unknown>;
}

/** Minimal R2 bucket binding: a content-addressed get by key. */
export interface BucketLike {
  get(key: string): Promise<R2ObjectLike | null>;
}

/**
 * Minimal KV binding for the route projection. The Worker reads the route value
 * as parsed JSON; the SAME namespace also backs the hard-revocation denylist
 * (`revoked:*` keys) per the revocation contract — Cloudflare KV's `get` supports both a
 * typed-json read and a plain-string read, so we declare both overloads.
 */
export interface RoutesKVLike {
  get(key: string, type: "json"): Promise<unknown>;
  get(key: string): Promise<string | null>;
}

/** Worker environment bindings + vars (declared in wrangler.toml). */
export interface Env {
  /** KV namespace: `route:<host>` → RouteValue (written only by the Go API). */
  ROUTES: RoutesKVLike;
  /** Single private R2 bucket holding all tenant content, per-org prefixed. */
  BUCKET: BucketLike;
  /**
   * Edge JWKS endpoint (the Go API's edge signer public keys). The gated path
   * fetches + caches this to verify the host-scoped edge token. Optional: falls
   * back to the production default in config.ts.
   */
  EDGE_JWKS_URL?: string;
  /**
   * Dashboard `/authz` exchange origin. A gated request with no/invalid edge
   * token 302s here. Optional: falls back to the production default.
   */
  APP_AUTHZ_URL?: string;
  /**
   * PRIMARY edge rate limiter: Cloudflare's native, atomic Rate Limiting binding
   * (declared as a `ratelimit` binding in wrangler.toml). Correctly counts a
   * single-IP flood — unlike the KV counter, which can't (KV throttles writes to
   * ~1/sec/key). When present it is preferred; absent → fall back to the LIMITS KV
   * counter (best-effort only) or no-op.
   */
  RATE_LIMITER?: RateLimiterLike;
  /**
   * OPTIONAL (Phase 4) KV for the FALLBACK rate-limiter counters AND the per-org
   * suspension/over-limit status (`rl:*` and `org_status:*` keys). Read+write
   * (counters), read (status). The `rl:*` counter is a best-effort fallback only
   * (see RATE_LIMITER); the `org_status:*` read is authoritative-cache. Absent →
   * fallback rate limiting is a no-op (fail open) and the org-status check is
   * skipped; the Go API + billing remain authoritative.
   */
  LIMITS?: CounterKVLike;
  /**
   * OPTIONAL (Phase 4) KV for the hard-revocation denylist (`revoked:user|site|
   * org:*` keys). When unset the Worker reuses the ROUTES namespace with the
   * `revoked:` prefix (revocation contract). Read-only; the Go API is the sole writer.
   */
  REVOKED?: RevokedKVLike;
  /** OPTIONAL: rate-limit max requests per window (overrides DEFAULT_RATE_LIMIT). */
  RATE_LIMIT_MAX?: string;
  /** OPTIONAL: rate-limit window length in seconds (overrides the default 60s). */
  RATE_LIMIT_WINDOW_SECONDS?: string;
  /**
   * OPTIONAL analytics: PostHog project key for the per-site `site_visit` metric.
   * UNSET → no visit events are emitted. POSTHOG_HOST / ENVIRONMENT / VISIT_SALT
   * tune the host, the `environment` label, and the visitor-hash salt. See
   * src/analytics.ts.
   */
  POSTHOG_KEY?: string;
  POSTHOG_HOST?: string;
  ENVIRONMENT?: string;
  VISIT_SALT?: string;
  /**
   * OPTIONAL: when truthy ("true"/"1"/"yes"/"on"), inject the slim, dismissible
   * "Deployed with Dropway" attribution banner into HTML pages served for
   * FREE-tier orgs (RouteValue.plan_tier === "free"). Unset/false → never inject
   * (the default, so an OSS self-host Worker shows no banner). See src/banner.ts.
   */
  ATTRIBUTION_BANNER?: string;
}

/**
 * Minimal Cache API surface. The public path caches successful blob responses
 * so a warm PoP serves without an R2 Class-B op. Injected/overridable so tests
 * run on the node pool without the global `caches`.
 */
export interface CacheLike {
  match(request: Request): Promise<Response | undefined>;
  put(request: Request, response: Response): Promise<void>;
}

/** The default (per-PoP) cache, when the runtime exposes one. */
function defaultCache(): CacheLike | null {
  const c = (globalThis as { caches?: { default?: CacheLike } }).caches;
  return c?.default ?? null;
}

// --- Worker entry -----------------------------------------------------------

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    try {
      return await serve(request, env, { waitUntil: (p) => ctx.waitUntil(p) });
    } catch (err) {
      // serve() is designed to return Responses rather than throw, so reaching
      // here is an unexpected bug. Capture it (off the response path) and fail
      // closed with a generic 500 — never leak tenant content or internals.
      const url = safePath(request.url);
      ctx.waitUntil(
        captureException(env, err, { path: url, method: request.method }),
      );
      return new Response("Internal Server Error", {
        status: 500,
        headers: { "Content-Type": "text/plain; charset=utf-8" },
      });
    }
  },
};

/**
 * Report a NON-NULL KV route value that failed validation to error tracking. The
 * caller still fails closed (a 500); this makes the otherwise-silent rejection
 * observable. The deploy-skew case — a schema_version newer than this Worker
 * supports — gets an explicit, unmistakable message and a `schema_too_new` flag so
 * it stands out in PostHog Error Tracking from ordinary malformed-projection drift.
 * Scheduled via `waitUntil` (off the response path); a no-op without one.
 */
function reportRejectedRoute(
  env: Env,
  host: string,
  raw: unknown,
  opts: ServeOptions,
): void {
  if (!opts.waitUntil) return;
  const diag = diagnoseRouteParseFailure(raw);
  const message = diag.schemaTooNew
    ? `route projection rejected: schema_version ${diag.schemaVersion} is newer than this Worker accepts (${SUPPORTED_SCHEMA_VERSION}) — deploy skew, the API writer is ahead of this reader; deploy the serving Worker`
    : `route projection rejected: ${diag.reason}`;
  opts.waitUntil(
    captureException(env, new Error(message), {
      kind: "route_projection_rejected",
      host,
      route_schema_version: diag.schemaVersion ?? null,
      supported_schema_version: SUPPORTED_SCHEMA_VERSION,
      schema_too_new: diag.schemaTooNew,
    }),
  );
}

/**
 * 500 for a route value that EXISTS in KV but this Worker cannot parse (a
 * schema_version newer than supported, or a malformed/drifted projection). Unlike
 * an unknown host (a genuine 404), this is a server-side problem — bad projection
 * data or a reader behind the writer — so it is surfaced as a 500: never cached
 * (no-store), visible in server-error monitoring, and distinct from "no such
 * site." Strict platform headers (our page, not tenant content).
 */
function projectionError(): Response {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "no-store",
    ...platformSecurityHeaders(),
  });
  return new Response(PROJECTION_ERROR_HTML, { status: 500, headers });
}

/** Best-effort path extraction for the error report (never throws). */
function safePath(rawURL: string): string {
  try {
    return new URL(rawURL).pathname;
  } catch {
    return "";
  }
}

/** Side-channels the Worker uses but tests can stub (cache + background work). */
export interface ServeOptions {
  /** Cache API instance; defaults to `caches.default` when available. */
  cache?: CacheLike | null;
  /** Schedules background work (cache writes) past the response. */
  waitUntil?: (p: Promise<unknown>) => void;
  /**
   * Fetch used by the gated path to load the edge JWKS. Defaults to the runtime
   * `fetch`. Injected in tests to serve a mock JWKS without network.
   */
  fetchImpl?: FetchLike;
  /** Clock injection for tests (edge-token exp + route expiry + rate window). */
  now?: Date;
}

/** Resolve the rate-limit policy from env vars, falling back to the default. */
function rateLimitPolicy(env: Env): RateLimitPolicy {
  const max = Number.parseInt(env.RATE_LIMIT_MAX ?? "", 10);
  const win = Number.parseInt(env.RATE_LIMIT_WINDOW_SECONDS ?? "", 10);
  return {
    limit: Number.isFinite(max) && max > 0 ? max : DEFAULT_RATE_LIMIT.limit,
    windowSeconds:
      Number.isFinite(win) && win > 0 ? win : DEFAULT_RATE_LIMIT.windowSeconds,
  };
}

/** The KV backing the org-status read (the LIMITS namespace, when configured). */
function statusKV(env: Env): StatusKVLike | undefined {
  return env.LIMITS;
}

/**
 * The KV backing the hard-revocation denylist. Prefers a dedicated REVOKED
 * binding; otherwise reuses the ROUTES namespace with the `revoked:` prefix
 * (reuse the ROUTES KV with a `revoked:` prefix, or a REVOKED
 * binding). ROUTES is always present, so a gated deployment always has a
 * denylist to consult (a missing `revoked:*` key is a clean miss → not revoked).
 */
function revokedKV(env: Env): RevokedKVLike {
  return env.REVOKED ?? env.ROUTES;
}

/**
 * Core request handler, exported for tests. Pure with respect to the injected
 * `env` bindings; performs no global side effects beyond an optional best-effort
 * cache write (scheduled via `waitUntil`).
 *
 * EMBED post-pass: when the request opted into the embed surface (?embed=1) the
 * response — WHATEVER it is — must render inside a cross-origin <iframe>. The
 * success paths (serveEmbed, the gate placeholder) already emit framable headers,
 * but the failure pages (unknown-host/path 404, 410 link-expired, 429, 503
 * suspended, 500 projection error) carry `X-Frame-Options: DENY` + CSP
 * `frame-ancestors 'none'`, and a framing-blocked response renders as a BLANK
 * iframe in Notion/Linear/Confluence — indistinguishable from a broken embed,
 * with zero diagnostics for the person pasting the URL. So every embed-mode
 * response is passed through asFramable(): the error page becomes visible inside
 * the frame and says what's wrong. This relaxes framing only for pages we (or the
 * public site) already show to anonymous visitors — gated bytes still never reach
 * an embed (serveEmbed fails closed to the sign-in placeholder).
 */
export async function serve(
  request: Request,
  env: Env,
  opts: ServeOptions = {},
): Promise<Response> {
  const response = await dispatch(request, env, opts);
  return isEmbedRequested(new URL(request.url)) ? asFramable(response) : response;
}

/**
 * Rewrite a response's framing posture to the embed surface's: X-Frame-Options
 * dropped (no "allow any origin" value exists; its presence vetoes the CSP), CSP
 * `frame-ancestors 'none'` widened to `*`, CORP widened to `cross-origin` (a
 * COEP `require-corp` parent would otherwise refuse the frame). A no-op for
 * responses that are already framable (the embed success paths). Returns a fresh
 * Response because upstream ones (e.g. from the Cache API) can be immutable.
 */
function asFramable(response: Response): Response {
  const out = new Response(response.body, response);
  out.headers.delete("X-Frame-Options");
  out.headers.set("Cross-Origin-Resource-Policy", "cross-origin");
  const csp = out.headers.get("Content-Security-Policy");
  if (csp !== null) {
    out.headers.set(
      "Content-Security-Policy",
      csp.replace("frame-ancestors 'none'", "frame-ancestors *"),
    );
  }
  return out;
}

/** The pre-embed-post-pass request handler (see serve() above). */
async function dispatch(
  request: Request,
  env: Env,
  opts: ServeOptions = {},
): Promise<Response> {
  // Only GET/HEAD are meaningful for static content.
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("Method Not Allowed", {
      status: 405,
      headers: { Allow: "GET, HEAD", ...securityHeaders() },
    });
  }

  // BLOCK SERVICE-WORKER REGISTRATION on the content origin, BEFORE
  // the cache lookup — a SW-script fetch carries `Service-Worker: script` and must
  // be refused under ANY path (a warm cache entry must not be served back as a
  // registrable SW script). isServiceWorkerScript() (in resolveBlob) additionally
  // 404s the conventional SW filenames as belt-and-suspenders.
  if (isServiceWorkerRequest(request)) {
    return notFound("service_worker_blocked", {
      request,
      env,
      waitUntil: opts.waitUntil,
    });
  }

  const url = new URL(request.url);
  const nowDate = opts.now ?? new Date();

  // 0. EDGE RATE LIMITING (denial-of-wallet). Keyed by client IP (else
  //    host), BEFORE the route lookup, so a flood is rejected without touching the
  //    route projection or R2. PREFER the native Rate Limiting binding (atomic —
  //    actually counts a single-IP flood); fall back to the KV counter (best-effort
  //    only) when the native binding isn't configured. Fails OPEN (a missing binding
  //    or limiter error never blocks a real request); authoritative spend caps live
  //    in the Go API.
  const policy = rateLimitPolicy(env);
  const identity = rateLimitIdentity(request, url.host);
  const rl = env.RATE_LIMITER
    ? await rateLimitNative(env.RATE_LIMITER, identity, policy.windowSeconds)
    : await rateLimitDecision(env.LIMITS, identity, nowDate.getTime(), policy);
  if (!rl.allowed) {
    return tooManyRequests(rl.retryAfterSeconds);
  }

  // 1. Resolve the host → route value from KV. The Host header is the tenant
  //    identity on the content domain; there is no JWT to consult. The shared
  //    contract validator also pins `schema_version` and fails closed on an
  //    unsupported (or malformed) projection value.
  const raw = await env.ROUTES.get(routeKey(url.host), "json");
  const route = parseRouteValue(raw);
  if (route === null) {
    // Two very different failures both parse to null — split them by status:
    //  - raw === null: no route for this host → genuinely "not found" → 404.
    //  - raw !== null: a route value EXISTS but this Worker can't parse it
    //    (schema_version newer than supported, or a malformed/drifted projection).
    //    That is a SERVER-SIDE problem, not "no such site", so we report it AND
    //    fail with 500 — uncached, visible in server-error monitoring, and honestly
    //    distinct from an unknown host. (A genuinely MISSING route — e.g. written to
    //    the wrong KV namespace — still looks like an unknown host → 404.)
    if (raw !== null) {
      reportRejectedRoute(env, url.host, raw, opts);
      return projectionError();
    }
    return notFound("route_not_found", {
      request,
      env,
      waitUntil: opts.waitUntil,
    });
  }

  // 2. Edge link-expiry (v2 RouteValue.expires_at). A public/unlisted share can
  //    carry an expiry the Go API serializes into the projection; once past, the
  //    Worker refuses to serve and shows a platform "link expired" page. (Gated
  //    expiry is refused earlier, at mint time in the Go API.)
  if (isRouteExpired(route, nowDate)) {
    return linkExpired();
  }

  // 3. PER-ORG SUSPENSION / over-limit (denial-of-wallet). If the Go API
  //    has flagged this route's org as suspended (billing/abuse) or over-limit
  //    (quota/egress cap) in KV, serve a platform "account suspended" page instead
  //    of ANY tenant content — public or gated. Skipped (served) when no status KV
  //    is configured; fails OPEN on a KV miss/error (the Go API stays
  //    authoritative). One extra KV get, only after the route is known.
  const orgStatus = await readOrgStatus(statusKV(env), route.org_id);
  if (isBlockingStatus(orgStatus)) {
    return accountSuspended(orgStatus);
  }

  // 3.5 LLM-access surface — robots.txt, AI-crawler gating, and the generated
  //     /llms.txt index. Runs before content dispatch: public sites welcome crawlers
  //     and expose /llms.txt; gated sites disallow crawlers (robots + 403) so their
  //     content is reachable by LLMs only through the authenticated Dropway MCP.
  const llmMeta = await handleLLMMeta(request, env, route, url, opts);
  if (llmMeta !== null) return bodyFor(request, llmMeta);

  // 3.9 EMBED surface (?embed=1): a framable, chrome-stripped rendering of the top
  //     document, so a site can be iframed into Notion/Linear/Confluence. Access
  //     control is fully preserved — a gated site shows a "Sign in to view"
  //     placeholder, never its bytes — so this runs AFTER suspension/expiry but its
  //     own gate check (in serveEmbed) fails closed for every non-public mode.
  if (isEmbedRequested(url)) {
    return serveEmbed(request, env, route, url, opts);
  }

  // 4. Dispatch by access mode.
  switch (route.access_mode) {
    case "public":
      return servePublic(request, env, route, url, opts);
    case "password":
    case "allowlist":
    case "org_only":
      return serveGated(request, env, route, url, gateOpts(env, opts), {
        serveContent: () => servePublicBody(request, env, route, url, opts),
        revokedKV: revokedKV(env),
        orgId: route.org_id,
      });
    default:
      // Unreachable given parseRouteValue, but fail closed.
      return notFound("unknown_access_mode", {
        request,
        route,
        env,
        waitUntil: opts.waitUntil,
      });
  }
}

/**
 * LLM-access surface: robots.txt, AI-crawler gating, and the generated /llms.txt.
 * Returns a Response when handled, or null to fall through to the content dispatch.
 * Mirrors services/serve/internal/serve/llm.go (serveLLMMeta).
 */
async function handleLLMMeta(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
  opts: ServeOptions,
): Promise<Response | null> {
  const clean = cleanPath(url.pathname);
  if (clean === null) return null; // unsafe path → let the normal flow 404
  const isPublic = route.access_mode === "public";

  // robots.txt — served to everyone (incl. crawlers) so they learn the rules.
  if (clean === "robots.txt") {
    const headers = new Headers(securityHeaders());
    headers.set("Content-Type", "text/plain; charset=utf-8");
    headers.set(
      "Cache-Control",
      isPublic
        ? "public, max-age=3600"
        : "private, no-store, max-age=0, must-revalidate",
    );
    return new Response(robotsTxtBody(isPublic), { status: 200, headers });
  }

  // AI-crawler gate: refuse known AI user-agents on non-public sites (403) rather
  // than bouncing them through /authz. Public sites welcome crawlers.
  if (!isPublic && isAICrawler(request.headers.get("User-Agent"))) {
    const headers = new Headers(securityHeaders());
    headers.set("Content-Type", "text/plain; charset=utf-8");
    headers.set("Cache-Control", "private, no-store, max-age=0, must-revalidate");
    headers.set("Vary", "User-Agent");
    return new Response(CRAWLER_BLOCKED_BODY, { status: 403, headers });
  }

  // /llms.txt index — public sites only. On a gated site it is treated like any
  // other content (gated/404), never exposed to discovery.
  if (clean === "llms.txt" && isPublic) {
    const manifest = await loadManifest(env, route);
    if (manifest === null)
      return notFound("manifest_missing", {
        request,
        route,
        env,
        waitUntil: opts.waitUntil,
      });
    const headers = new Headers(securityHeaders());
    headers.set("Content-Type", "text/plain; charset=utf-8");
    headers.set("Cache-Control", "public, max-age=300");
    return new Response(llmsTxtBody(url.host, manifest, url.origin), {
      status: 200,
      headers,
    });
  }

  return null;
}

/** Resolve the gated-path config + injected fetch from env/opts. */
function gateOpts(
  env: Env,
  opts: ServeOptions,
): { cfg: GatedConfig; fetchImpl: FetchLike; now: number } {
  return {
    cfg: gatedConfig(env),
    fetchImpl: opts.fetchImpl ?? defaultFetch(),
    now: (opts.now ?? new Date()).getTime(),
  };
}

/** The runtime `fetch`, narrowed to FetchLike (tests inject their own). */
function defaultFetch(): FetchLike {
  return (input: string) => fetch(input);
}

/**
 * PUBLIC serve path — no JWT, cacheable. Fetches the deploy manifest, resolves
 * the request path (with index.html + directory fallback) to a content-addressed
 * blob, then streams it from R2 with the manifest's Content-Type and the policy
 * Cache-Control. Successful responses are written to the Cache API so a warm PoP
 * serves without an R2 op.
 */
async function servePublic(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
  opts: ServeOptions,
): Promise<Response> {
  // Reserved transcript path (Share This Session). Checked BEFORE the Cache API:
  // the transcript mutates independently of deploys, so it is served no-store and
  // must never populate (or be satisfied from) the PoP cache.
  const chat = await serveChatIfRequested(env, route, url);
  if (chat !== null) return bodyFor(request, chat);

  const cache = opts.cache !== undefined ? opts.cache : defaultCache();

  // Cache hit? Serve straight from the PoP (HEAD reuses the GET-keyed entry,
  // stripping the body below). The cache key is content-version-scoped because
  // the manifest is per version and the blob key is the sha256 (see cacheKey).
  if (cache) {
    const key = cacheKey(route, url);
    const hit = await cache.match(key);
    if (hit) {
      // Count the visit even on a warm cache hit (HTML is short-cached but still
      // a page view), using the cached response's own Content-Type.
      scheduleVisit(env, request, route, url, hit.headers.get("Content-Type"), opts);
      return bodyFor(request, hit);
    }
  }

  const resolved = await resolveBlob(request, env, route, url, opts);
  if (resolved.kind === "not-found") return resolved.response;

  // Best-effort per-site visit metric (HTML pages only; never blocks the response).
  scheduleVisit(env, request, route, url, resolved.contentType, opts);

  // Free-tier attribution banner (HTML only). Done BEFORE the Response is built so
  // the banner-injected body is what gets cached at the PoP below.
  const out = await bannerize(env, route, resolved, request.method === "HEAD");
  const response = contentResponse(resolved.servedPath, out);

  // Best-effort: populate the PoP cache for subsequent requests. We cache a clone so
  // the streamed body is still available to the caller. Only GET responses are
  // cached: a HEAD carries no body (and its banner length is derived without
  // buffering), so caching it would poison the shared GET/HEAD key with an empty body.
  if (cache && request.method === "GET") {
    const key = cacheKey(route, url);
    const write = cache.put(key, response.clone());
    if (opts.waitUntil) opts.waitUntil(write);
    else await write.catch(() => {});
  }

  return bodyFor(request, response);
}

/**
 * Build the body of a GATED (password/allowlist/org_only) success response —
 * the SAME manifest→blob resolution as the public path, but WITHOUT the public
 * Cache API and with the caller (gated module) overriding Cache-Control to
 * `private, no-store` (protected bytes never enter a shared cache). Returns
 * a 200 Response with content headers, or the appropriate 404 on a miss/drift.
 * `bodyFor` strips the body for HEAD. Never consults or writes any cache.
 */
async function servePublicBody(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
  opts: ServeOptions,
): Promise<Response> {
  // Reserved transcript path (Share This Session) for GATED sites. This function
  // only runs AFTER serveGated's authz passed, so the transcript is exactly as
  // gated as the site — the pre-auth hooks never serve it. (The gated wrapper
  // then forces Cache-Control to private/no-store on top.)
  const chat = await serveChatIfRequested(env, route, url);
  if (chat !== null) return bodyFor(request, chat);

  const resolved = await resolveBlob(request, env, route, url, opts);
  if (resolved.kind === "not-found") return resolved.response;

  // Count the visit for GATED sites too (org_only/password/allowlist). A served
  // HTML page is a page view regardless of access mode; the public path records it
  // in servePublic, and without this the `site_visit` metric silently excludes
  // EVERY gated site (an org whose sites are all org_only would see zero visits).
  // isVisit (inside captureSiteVisit) still gates to a GET of an HTML document, so
  // assets and HEAD probes don't count.
  scheduleVisit(env, request, route, url, resolved.contentType, opts);

  // Free-tier attribution banner (HTML only). Gated responses are never cached,
  // but a free-tier gated page still carries the banner ("each page").
  const out = await bannerize(env, route, resolved, request.method === "HEAD");
  const response = contentResponse(resolved.servedPath, out);
  return bodyFor(request, response);
}

/**
 * EMBED serve path (?embed=1) — a framable, chrome-stripped rendering of the top
 * document. Two arms:
 *
 *   - GATED (password/allowlist/org_only): NEVER serve tenant bytes into an embed.
 *     Return the framable "Sign in to view" placeholder that links out to the real
 *     site (new tab) for authentication. This fails closed for every non-public
 *     mode — an embed can't be a bypass around the gate.
 *   - PUBLIC: resolve the blob exactly like the public path, but emit FRAMABLE
 *     headers (X-Frame-Options dropped, CSP `frame-ancestors *`) and inject only the
 *     "Powered by Dropway" badge (the free-tier banner + chat pill are suppressed in
 *     an embed). Not written to the PoP Cache API — embed traffic is low-volume and
 *     this avoids a second cache-key dimension; the browser still caches per
 *     Cache-Control.
 */
async function serveEmbed(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
  opts: ServeOptions,
): Promise<Response> {
  if (route.access_mode !== "public") {
    // The placeholder links to the site ROOT with no ?embed=1, so clicking it opens
    // the gated site (new tab) and runs the normal /authz sign-in.
    return bodyFor(request, embedGatePlaceholder(new URL("/", url).toString()));
  }

  const resolved = await resolveBlob(request, env, route, url, opts);
  if (resolved.kind === "not-found") return bodyFor(request, resolved.response);

  // Count the embed view like any other HTML page view (isVisit still gates to a GET
  // of an HTML document, so embedded assets and HEAD probes don't count).
  scheduleVisit(env, request, route, url, resolved.contentType, opts);

  const out = await embedBadgeInject(route, url, resolved, request.method === "HEAD");
  const response = new Response(out.body, {
    status: 200,
    headers: embedResponseHeaders(resolved.servedPath, {
      contentType: out.contentType,
      etag: out.etag,
      lastModified: out.lastModified,
      contentLength: out.contentLength,
    }),
  });
  return bodyFor(request, response);
}

/**
 * Inject the "Powered by Dropway" embed badge into an HTML document, when the badge
 * should show for this route (see shouldShowEmbedBadge) and the body is injectable
 * UTF-8 HTML. Mirrors bannerize's HEAD-without-buffering optimization: the badge only
 * INSERTS bytes, so a HEAD reports `original + EMBED_BADGE_BYTE_LENGTH` without
 * reading the whole body. Non-badge / non-HTML responses pass through untouched.
 */
async function embedBadgeInject(
  route: RouteValue,
  url: URL,
  resolved: ResolvedBlob,
  isHead: boolean,
): Promise<ServeBody> {
  const passthrough: ServeBody = {
    contentType: resolved.contentType,
    body: resolved.body,
    etag: resolved.etag,
    lastModified: resolved.lastModified,
    contentLength: resolved.contentLength,
  };
  if (
    !shouldShowEmbedBadge(route, url) ||
    !isInjectableEmbedBadge(resolved.servedPath, resolved.contentType)
  ) {
    return passthrough;
  }

  if (isHead) {
    return {
      contentType: resolved.contentType,
      body: null,
      contentLength:
        resolved.contentLength === undefined
          ? undefined
          : resolved.contentLength + EMBED_BADGE_BYTE_LENGTH,
    };
  }

  const injected = injectEmbedBadge(
    resolved.body ? await new Response(resolved.body).text() : "",
  );
  return {
    contentType: resolved.contentType,
    body: injected,
    contentLength: new TextEncoder().encode(injected).length,
  };
}

/** Outcome of resolving a request path to its R2 blob (shared public/gated). */
type BlobResolution =
  | {
      kind: "ok";
      servedPath: string;
      contentType: string;
      body: ReadableStream | null;
      etag?: string;
      lastModified?: Date;
      contentLength?: number;
    }
  | { kind: "not-found"; response: Response };

/** The successful (200) arm of BlobResolution. */
type ResolvedBlob = Extract<BlobResolution, { kind: "ok" }>;

/** The body + header inputs for a 200 response, after optional banner injection. */
interface ServeBody {
  contentType: string;
  body: BodyInit | null;
  etag?: string;
  lastModified?: Date;
  contentLength?: number;
}

/**
 * Serve the reserved /__dropway/chat transcript page (Share This Session), or
 * return null when the request isn't for it / the route has no chat attached.
 *
 * ONLY called from inside the access-controlled serving paths (servePublic and
 * servePublicBody — the latter runs after the gated authz), so a gated site's
 * transcript is exactly as gated as the site itself; the pre-auth handleLLMMeta
 * hook never touches this path.
 *
 * The compiled transcript JSON is read from the SAME bucket as the content
 * blobs (chat-transcripts/<org>/<chat_id>.json). It is MUTABLE — the Go API
 * rewrites it on every append/delete, independent of deploys — so the response
 * is `no-store` and never enters the Cache API. A missing or malformed object
 * renders the minimal "conversation unavailable" page (never a throw).
 */
async function serveChatIfRequested(
  env: Env,
  route: RouteValue,
  url: URL,
): Promise<Response | null> {
  if (!route.chat_id) return null;
  if (cleanPath(url.pathname) !== CHAT_RESERVED_PATH) return null;

  let transcript: unknown = null;
  const object = await env.BUCKET.get(chatTranscriptKey(route.org_id, route.chat_id));
  if (object !== null) {
    try {
      transcript = object.json ? await object.json() : await readBodyJson(object);
    } catch {
      transcript = null; // malformed JSON → the "unavailable" page below
    }
  }

  const html = renderChatPage(transcript, url.host, route.plan_tier);
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "no-store",
    ...securityHeaders(),
  });
  // The pill's drawer embeds this page in a SAME-ORIGIN iframe, but the shared
  // content header set forbids ALL framing (X-Frame-Options: DENY + CSP
  // frame-ancestors 'none'). Relax exactly that pair to same-origin here —
  // cross-origin embedding stays blocked; every other header is unchanged.
  headers.set("X-Frame-Options", "SAMEORIGIN");
  headers.set(
    "Content-Security-Policy",
    CONTENT_CSP.replace("frame-ancestors 'none'", "frame-ancestors 'self'"),
  );
  return new Response(html, { status: 200, headers });
}

/**
 * Apply the HTML injections to a resolved blob: the free-tier "Deployed with
 * Dropway" attribution banner (org is free-tier + the feature flag is on) and
 * the "How this was made" chat pill (route carries a chat_id) — both only on
 * injectable (UTF-8) HTML. Only HTML is buffered into memory; every other asset
 * (and every no-injection response, and any non-UTF-8 page) streams through
 * untouched.
 *
 * When injecting we buffer the body to text, insert the markup, recompute
 * Content-Length, and DROP the blob's ETag/Last-Modified — they describe the
 * original (un-injected) bytes and would otherwise mislabel the transformed body.
 *
 * HEAD never returns a body (bodyFor strips it), so we skip the buffer entirely and
 * derive the length arithmetically — the injected length is exactly the original
 * length plus the (fixed, UTF-8-stable) markup, so a HEAD reports the same
 * Content-Length a GET would without reading the whole document.
 */
async function bannerize(
  env: Env,
  route: RouteValue,
  resolved: ResolvedBlob,
  isHead: boolean,
): Promise<ServeBody> {
  const passthrough: ServeBody = {
    contentType: resolved.contentType,
    body: resolved.body,
    etag: resolved.etag,
    lastModified: resolved.lastModified,
    contentLength: resolved.contentLength,
  };
  const wantBanner =
    shouldInjectBanner(env, route, resolved.servedPath) &&
    isInjectableContentType(resolved.contentType);
  const wantPill = shouldInjectChatPill(route, resolved.servedPath, resolved.contentType);
  if (!wantBanner && !wantPill) {
    return passthrough;
  }

  const addedBytes =
    (wantBanner ? BANNER_BYTE_LENGTH : 0) + (wantPill ? CHAT_PILL_BYTE_LENGTH : 0);

  if (isHead) {
    // No body will be sent. Avoid buffering: injected length = original + markup
    // (both injections only insert, and the inject path is UTF-8 so the round-trip
    // is byte-stable). Omit Content-Length only when the original size is unknown.
    return {
      contentType: resolved.contentType,
      body: null,
      contentLength:
        resolved.contentLength === undefined
          ? undefined
          : resolved.contentLength + addedBytes,
    };
  }

  let injected = resolved.body ? await new Response(resolved.body).text() : "";
  if (wantBanner) injected = injectBanner(injected);
  if (wantPill) injected = injectChatPill(injected);
  return {
    contentType: resolved.contentType,
    body: injected,
    contentLength: new TextEncoder().encode(injected).length,
  };
}

/** Build the 200 content Response from a (possibly banner-injected) ServeBody. */
function contentResponse(servedPath: string, out: ServeBody): Response {
  const headers = publicResponseHeaders(servedPath, {
    contentType: out.contentType,
    etag: out.etag,
    lastModified: out.lastModified,
    contentLength: out.contentLength,
  });
  return new Response(out.body, { status: 200, headers });
}

/**
 * Resolve a request to its content-addressed blob via the deploy manifest:
 * sanitize the path, load + validate the manifest, match an entry (with the
 * index/pretty-URL fallbacks), and fetch the blob from R2. Returns the blob
 * stream + metadata, or a ready 404 (custom page when the manifest ships one).
 * This is the shared core of BOTH the public and gated serve paths; only the
 * surrounding Cache-Control / Cache-API behavior differs.
 */
async function resolveBlob(
  request: Request,
  env: Env,
  route: RouteValue,
  url: URL,
  opts: ServeOptions,
): Promise<BlobResolution> {
  // Sanitize the request path before resolving it against the manifest.
  const clean = cleanPath(url.pathname);
  if (clean === null) {
    // Unsafe path (traversal etc.) → 404, never an error that leaks structure.
    return {
      kind: "not-found",
      response: await notFound("unsafe_path", {
        request,
        route,
        env,
        waitUntil: opts.waitUntil,
      }),
    };
  }

  // BLOCK SERVICE-WORKER REGISTRATION on the content origin: refuse
  // to serve a scriptable body at the conventional SW script paths (sw.js,
  // service-worker.js, …). A tenant therefore cannot register a SW that would
  // persist its JS, intercept fetches, or survive a takedown. We 404 (the same
  // fail-closed shape as any unmatched path) rather than leak that the path is
  // special.
  if (isServiceWorkerScript(clean)) {
    return {
      kind: "not-found",
      response: await notFound("service_worker_blocked", {
        request,
        route,
        env,
        waitUntil: opts.waitUntil,
      }),
    };
  }

  const manifest = await loadManifest(env, route);
  if (manifest === null) {
    // Missing/corrupt manifest → fail closed with the default 404.
    return {
      kind: "not-found",
      response: await notFound("manifest_missing", {
        request,
        route,
        env,
        waitUntil: opts.waitUntil,
      }),
    };
  }

  const match = resolveManifestEntry(manifest, clean);
  if (match === null) {
    // No page matched. If the request targets a DIRECTORY that actually contains
    // files (e.g. an upload with no index.html, or any subfolder lacking one),
    // synthesize an autoindex listing from the manifest instead of 404ing, so the
    // files stay browsable. A directory with no descendants (a genuine typo) still
    // returns null below and falls through to the custom/default 404.
    //
    // A site that ships its own 404.html has opted into custom miss handling, so
    // that takes precedence: we only auto-index when there is NO custom 404 page.
    // This keeps a real website's subdirectory misses (e.g. an SPA's /assets/)
    // serving its 404.html rather than a surprise file listing; the autoindex is
    // for plain file-dump uploads, which don't ship a 404.html.
    if (manifest.files[NOT_FOUND_PATH] === undefined) {
      const listing = directoryListing(manifest, clean);
      if (listing !== null) return listing;
    }

    // No served path matched → the version's custom 404 page, else the default.
    return {
      kind: "not-found",
      response: await notFound("no_manifest_entry", {
        request,
        route,
        env,
        manifest,
        waitUntil: opts.waitUntil,
      }),
    };
  }

  const object = await env.BUCKET.get(blobKey(route.org_id, match.entry.sha256));
  if (object === null) {
    // Manifest referenced a blob not in R2 — projection drift. Fail closed.
    return {
      kind: "not-found",
      response: await notFound("blob_missing", {
        request,
        route,
        env,
        manifest,
        waitUntil: opts.waitUntil,
      }),
    };
  }

  // A Markdown upload (.md/.mdx) is rendered into a self-contained viewer page
  // (formatted by default, with a raw toggle + copy button) instead of streaming
  // its raw bytes — which a browser would show as plain text or download. The
  // page is HTML, so it flows through the SAME banner/header/cache path as any
  // served HTML document: servedPath is set to "index.html" (exactly as the
  // autoindex does) so the short-TTL HTML cache policy + free-tier banner apply.
  //
  // Two opt-OUTs of rendering, both serving the original bytes via the normal path
  // below:
  //  - `?raw` (or `?raw=1`): an explicit escape hatch so a deep link can still
  //    fetch the source (the viewer page surfaces this link). Cached under a
  //    distinct key (see cacheKey) so it never collides with the rendered page.
  //  - oversized files: rendering buffers the whole document into memory (twice,
  //    counting the banner pass), and the Worker isolate has a hard memory cap, so
  //    a file larger than MARKDOWN_MAX_RENDER_BYTES is streamed raw instead of
  //    risking an OOM. The size comes from the manifest (no extra read); an
  //    unknown size renders (the deploy always records one in practice).
  const size = object.size ?? match.entry.size;
  const tooBigToRender = size !== undefined && size > MARKDOWN_MAX_RENDER_BYTES;
  if (isMarkdownPath(match.path) && !isRawRequested(url) && !tooBigToRender) {
    const source = object.body ? await new Response(object.body).text() : "";
    const html = renderMarkdownPage(match.path, source);
    const bytes = new TextEncoder().encode(html);
    return {
      kind: "ok",
      servedPath: "index.html",
      contentType: "text/html; charset=utf-8",
      body: new Response(bytes).body,
      contentLength: bytes.length,
    };
  }

  return {
    kind: "ok",
    servedPath: match.path,
    contentType: match.entry.content_type,
    body: object.body,
    etag: object.httpEtag,
    lastModified: object.uploaded,
    contentLength: object.size ?? match.entry.size,
  };
}

/**
 * Synthesize an autoindex listing for a directory request that matched no page.
 * Returns an "ok" BlobResolution carrying generated HTML when `clean` targets a
 * directory with children, or null when it has none (so the caller 404s). The
 * resolution is shaped exactly like a served HTML page — `servedPath` is set to
 * "index.html" so the short-TTL HTML cache policy and the page-view metric apply,
 * and the body flows through the same banner/header path as any HTML document
 * (so a free-tier listing still carries the attribution banner, and a gated
 * site's listing is only reachable after auth, since the gated path calls this
 * same resolver). The body is encoded UTF-8 with an exact Content-Length.
 */
function directoryListing(manifest: Manifest, clean: string): BlobResolution | null {
  const entries = listDirectory(manifest, directoryPrefix(clean));
  if (entries === null) return null;

  const html = renderDirectoryListing(directoryPrefix(clean), entries);
  const bytes = new TextEncoder().encode(html);
  return {
    kind: "ok",
    servedPath: "index.html",
    contentType: "text/html; charset=utf-8",
    body: new Response(bytes).body,
    contentLength: bytes.length,
  };
}

/**
 * Fetch + parse a version's deploy manifest from R2. The result is cached on the
 * route object for the lifetime of this request (`_manifest`), so a path that
 * probes several candidates does not re-fetch. Returns null when the manifest is
 * missing or fails validation (so the caller fails closed).
 */
async function loadManifest(env: Env, route: RouteValue): Promise<Manifest | null> {
  const cached = (route as RouteWithManifest)._manifest;
  if (cached !== undefined) return cached;

  let manifest: Manifest | null = null;
  const object = await env.BUCKET.get(manifestKey(route));
  if (object !== null) {
    // Prefer R2's streaming `.json()` when present; fall back to parsing the body.
    const raw = object.json ? await object.json() : await readBodyJson(object);
    manifest = parseManifest(raw);
  }

  (route as RouteWithManifest)._manifest = manifest;
  return manifest;
}

/** Internal: a route carrying its per-request memoized manifest. */
type RouteWithManifest = RouteValue & { _manifest?: Manifest | null };

/** Read + JSON-parse an R2 object body when `.json()` is unavailable (mocks). */
async function readBodyJson(object: R2ObjectLike): Promise<unknown> {
  if (object.body === null) return null;
  const text = await new Response(object.body).text();
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * The Cache API key for a public response. We key on the ORIGIN + access_mode +
 * version + path so:
 *  - a pointer flip (new version_id) is a fresh key (publish/rollback never serves
 *    stale), and one host's cache can never satisfy another's;
 *  - the access_mode is PART of the key (M2) so a body cached while the route was
 *    `public` can never be matched to satisfy a request the route now resolves as
 *    gated — the cache is partitioned by mode, never reused across an access flip.
 *  - the plan_tier is PART of the key so a plan change (which reprojects the route's
 *    plan_tier in KV but keeps the same version_id) flips the attribution banner
 *    IMMEDIATELY: free and paid resolve to different keys, so an upgraded org never
 *    serves a stale banner-injected body (nor a downgraded org a stale un-bannered
 *    one) — without it the banner would persist until the short HTML TTL expired.
 *
 * Residual (inherent to eventual consistency): during the brief KV propagation
 * window after a public→gated flip, a lagging PoP still reads `public` and serves
 * the public path; the route rewrite + the revocation denylist (gated path) are the
 * authoritative immediate controls. Method is normalized to GET so HEAD reuses the
 * entry.
 *
 * The ONE query parameter that varies the body is `?raw` (it opts a Markdown file
 * out of the viewer page and serves the source bytes), so it — and only it — is
 * folded into the key; every other query string is still ignored. Without this a
 * raw response and a rendered response would collide under one key.
 */
function cacheKey(route: RouteValue, url: URL): Request {
  const raw = isRawRequested(url);
  const keyUrl = new URL(url.toString());
  keyUrl.search = ""; // static content does not vary by query string (except ?raw)
  // Fold access_mode + version + plan_tier + chat_id into the key path so neither
  // an access change, a publish/rollback, a plan change, nor a chat attach/detach
  // can ever serve a stale or cross-mode/cross-tier cache entry. plan_tier and
  // chat_id are optional (older projections) → "_" stands in for "absent"; folding
  // chat_id means attaching a chat (which reprojects the route but keeps the same
  // version_id) flips the "How this was made" pill IMMEDIATELY — cached HTML
  // without the pill can't persist after an attach (nor with it after a detach).
  // The `raw` segment partitions ?raw from the rendered page (see resolveBlob's
  // Markdown branch).
  const tier = route.plan_tier ?? "_";
  const chat = route.chat_id ?? "_";
  const rawSeg = raw ? "/raw" : "";
  keyUrl.pathname = `/${route.access_mode}/${route.version_id}/${tier}/${chat}${rawSeg}${keyUrl.pathname}`;
  return new Request(keyUrl.toString(), { method: "GET" });
}

/** The query parameter that opts a Markdown file out of the rendered viewer. */
const RAW_QUERY_PARAM = "raw";

/**
 * True when the request asks for the raw source instead of the rendered Markdown
 * viewer — the presence of `?raw` (any value, including empty: `?raw`, `?raw=1`).
 */
function isRawRequested(url: URL): boolean {
  return url.searchParams.has(RAW_QUERY_PARAM);
}

/**
 * Schedule a best-effort `site_visit` capture for an HTML page response. Runs
 * past the response via `waitUntil` (so it never adds latency) and is a complete
 * no-op when PostHog isn't configured or the response isn't a page (see
 * captureSiteVisit / isVisit). Failures are swallowed inside captureSiteVisit.
 */
function scheduleVisit(
  env: AnalyticsEnv,
  request: Request,
  route: RouteValue,
  url: URL,
  contentType: string | null,
  opts: ServeOptions,
): void {
  // Avoid even constructing the work when analytics is off.
  if (!env.POSTHOG_KEY) return;
  const p = captureSiteVisit(env, {
    request,
    route,
    url,
    contentType,
    now: opts.now ?? new Date(),
  });
  if (opts.waitUntil) opts.waitUntil(p);
  else void p.catch(() => {});
}

/** Return a response suitable for the request method (HEAD carries no body). */
function bodyFor(request: Request, response: Response): Response {
  if (request.method !== "HEAD") return response;
  return new Response(null, {
    status: response.status,
    headers: response.headers,
  });
}

/**
 * Why a request resolved to 404 — emitted with EVERY notFound() so an operator can
 * tell "a user can't reach the site" cases apart in PostHog
 * (route_not_found vs manifest_missing vs no_manifest_entry vs blob_missing)
 * instead of seeing an unattributed `status: 404`. Stable strings, safe to filter
 * and aggregate on.
 */
type NotFoundReason =
  | "route_not_found" // host has no valid route in KV (unknown host or rejected projection)
  | "service_worker_blocked" // a service-worker script registration was refused
  | "unknown_access_mode" // route carried an access_mode the Worker doesn't handle
  | "unsafe_path" // traversal / bad-encoding path rejected before lookup
  | "manifest_missing" // route resolved but the version's manifest is absent/corrupt in R2
  | "no_manifest_entry" // manifest loaded but no file matched the request path
  | "blob_missing"; // manifest referenced a blob that isn't in R2 (projection drift)

/**
 * Inputs for building + reporting a 404. `request` is required so every 404 records
 * its host + path; `env` + `waitUntil` enable the best-effort PostHog `serve_404`
 * event (off the response path); `route` enriches it with org/site/version (and,
 * with `env` + `manifest`, selects the version's own custom 404.html).
 */
interface NotFoundContext {
  request: Request;
  route?: RouteValue | null;
  env?: Env;
  manifest?: Manifest;
  /** Schedules the PostHog emit past the response (so it never adds latency). */
  waitUntil?: (p: Promise<unknown>) => void;
}

/**
 * 404 response. Emits a best-effort `serve_404` event to PostHog (so "why can't
 * this user reach their site?" is answerable — and graphable — from analytics
 * instead of an unattributed `status: 404`), then: if a route + manifest are
 * known, prefer the version's own `404.html` (resolved through the manifest →
 * blob); otherwise a minimal platform 404. Always carries security headers and
 * short, public cache (a 404 is still safe to cache).
 *
 * The emit is scheduled via `waitUntil` and gated to page navigations
 * (is404Reportable) so it tracks pages users can't load, not every missing asset;
 * it is a custom event, NOT a `$exception`, so it doesn't pollute Error Tracking.
 */
async function notFound(
  reason: NotFoundReason,
  ctx: NotFoundContext,
): Promise<Response> {
  const { route, env, manifest } = ctx;
  if (env?.POSTHOG_KEY && ctx.waitUntil) {
    ctx.waitUntil(
      captureServe404(env, {
        request: ctx.request,
        reason,
        route: route ?? null,
        now: new Date(),
      }),
    );
  }
  if (route && env && manifest) {
    const entry = manifest.files[NOT_FOUND_PATH];
    if (entry !== undefined) {
      const object = await env.BUCKET.get(blobKey(route.org_id, entry.sha256));
      if (object !== null) {
        // A version's CUSTOM 404 page is tenant content → tenant CSP, not the
        // strict platform CSP (it may legitimately load the site's own assets).
        const customHeaders = new Headers({
          "Cache-Control": "public, max-age=30",
          ...securityHeaders(),
        });
        customHeaders.set("Content-Type", entry.content_type);
        return new Response(object.body, { status: 404, headers: customHeaders });
      }
    }
  }

  // The platform default 404 is our own page → strict platform headers. For an
  // UNKNOWN HOST (route_not_found — no site is served on this hostname) we show the
  // Dropway-branded page with a sign-up CTA: the visitor reached a content domain
  // with no site behind it, so they're a candidate to make one. Every other 404 is
  // a miss WITHIN a real tenant's site (a bad path / drift) — that's served the
  // tenant's own custom 404.html above, or this plain platform page; we must NOT
  // advertise Dropway sign-up on a customer's own domain.
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "public, max-age=30",
    ...platformSecurityHeaders(),
  });
  const body = reason === "route_not_found" ? UNKNOWN_HOST_404_HTML : DEFAULT_404_HTML;
  return new Response(body, { status: 404, headers });
}

/**
 * Platform "link expired" page — served when a public/unlisted route carries an
 * `expires_at` (v2 RouteValue) that is now past (edge link-expiry). 410 Gone is
 * the right status (the resource intentionally no
 * longer exists at this URL). Never shared-cached so a future re-publish (new
 * expiry) is visible immediately.
 */
export function linkExpired(): Response {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "no-store",
    ...platformSecurityHeaders(),
  });
  return new Response(LINK_EXPIRED_HTML, { status: 410, headers });
}

/**
 * Platform "Too Many Requests" page (429) — served when the edge rate limiter
 * trips (denial-of-wallet). Carries `Retry-After` (seconds) so a well-behaved
 * client backs off, and `no-store` so the 429 is never cached as if it were the
 * site. Strict platform headers (our own page, no tenant content).
 */
export function tooManyRequests(retryAfterSeconds: number): Response {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "no-store",
    "Retry-After": String(Math.max(1, Math.ceil(retryAfterSeconds))),
    ...platformSecurityHeaders(),
  });
  return new Response(TOO_MANY_REQUESTS_HTML, { status: 429, headers });
}

/**
 * Platform "account suspended / over limit" page — served INSTEAD of any tenant
 * content when the route's org is flagged suspended/over_limit in KV
 * (denial-of-wallet + billing suspension). 503 with a short `Retry-After` (the
 * org may be reinstated) and `no-store` so a reinstatement is visible
 * immediately. The two statuses share one page with a status-specific line.
 */
export function accountSuspended(status: "suspended" | "over_limit"): Response {
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "no-store",
    "Retry-After": "300",
    ...platformSecurityHeaders(),
  });
  const html =
    status === "over_limit" ? ACCOUNT_OVER_LIMIT_HTML : ACCOUNT_SUSPENDED_HTML;
  return new Response(html, { status: 503, headers });
}

const DEFAULT_404_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>404 — Not Found</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; }
  h1 { font-size: 3rem; margin: 0 0 .25rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>404</h1>
    <p>This page could not be found.</p>
  </main>
</body>
</html>
`;

// Unknown-host 404 — the visitor reached a content domain with no site behind it
// (route_not_found). Unlike the plain 404 above (a miss inside a real tenant site),
// this is shown only on our own un-claimed hostnames, so it's safe + useful to
// pitch Dropway with a sign-up CTA to dropway.dev.
const UNKNOWN_HOST_404_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>404 — No site here</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 3rem; margin: 0 0 .25rem; }
  p { opacity: .75; }
  .cta { display: inline-block; margin-top: 1.25rem; padding: .6rem 1.1rem;
         border-radius: .5rem; background: #1d4aff; color: #fff;
         text-decoration: none; font-weight: 600; }
</style>
</head>
<body>
  <main>
    <h1>404</h1>
    <p>There's no site at this address yet.</p>
    <p>Dropway lets you publish a static site in seconds.</p>
    <a class="cta" href="https://dropway.dev">Create your site →</a>
  </main>
</body>
</html>
`;

// Platform 500 — a route value exists for this host but can't be served (bad/too-new
// projection). Generic copy: it must not leak the internal reason to a visitor.
const PROJECTION_ERROR_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>500 — Temporarily Unavailable</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; }
  h1 { font-size: 3rem; margin: 0 0 .25rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>500</h1>
    <p>This site is temporarily unavailable. Please try again shortly.</p>
  </main>
</body>
</html>
`;

/**
 * Platform-controlled "link expired" page (served on a past `expires_at`). Kept
 * static + self-contained (no tenant content, no scripts) — anti-phishing parity
 * with the password gate: an expired link must show a platform page tenant
 * JS can neither render nor script.
 */
const LINK_EXPIRED_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Link expired</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>This link has expired</h1>
    <p>The share link for this site is no longer active. Ask the site owner for a new one.</p>
  </main>
</body>
</html>
`;

/**
 * Platform "Too Many Requests" page (429). Static + self-contained — same
 * anti-phishing posture as the other platform pages (no tenant content/scripts).
 */
const TOO_MANY_REQUESTS_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Too many requests</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>Too many requests</h1>
    <p>You have made too many requests in a short time. Please wait a moment and try again.</p>
  </main>
</body>
</html>
`;

/**
 * Platform "account suspended" page (503) — billing suspension / abuse hold. The
 * page deliberately reveals nothing about the tenant beyond "unavailable".
 */
const ACCOUNT_SUSPENDED_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Site unavailable</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>This site is temporarily unavailable</h1>
    <p>The account for this site has been suspended. If you own this site, sign in to your dashboard to resolve it.</p>
  </main>
</body>
</html>
`;

/**
 * Platform "over limit" page (503) — quota / egress cap reached. Distinct copy
 * from suspension so a site owner knows it is a usage cap, not an abuse hold.
 */
const ACCOUNT_OVER_LIMIT_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Site unavailable</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>This site is temporarily unavailable</h1>
    <p>This account has reached its usage limit. If you own this site, sign in to your dashboard to upgrade or wait for the limit to reset.</p>
  </main>
</body>
</html>
`;
