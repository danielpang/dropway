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
  cleanPath,
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
import { publicResponseHeaders, securityHeaders } from "./http";
import {
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
 */
export async function serve(
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
    // Unknown host or malformed/old projection → fail closed. The contract
    // validator accepts schema_version 1 AND 2 (v2 adds optional expires_at);
    // anything else (or a malformed shape) parses to null here.
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
 * Apply the free-tier "Deployed with Dropway" attribution banner to a resolved
 * blob when the org is free-tier, the feature is enabled, and the document is
 * injectable HTML. Only HTML is buffered into memory; every other asset (and every
 * paid/unknown-tier response, and any non-UTF-8 page) streams through untouched.
 *
 * When injecting we buffer the body to text, insert the banner, recompute
 * Content-Length, and DROP the blob's ETag/Last-Modified — they describe the
 * original (un-bannered) bytes and would otherwise mislabel the transformed body.
 *
 * HEAD never returns a body (bodyFor strips it), so we skip the buffer entirely and
 * derive the length arithmetically — the injected length is exactly the original
 * length plus the (fixed, UTF-8-stable) banner, so a HEAD reports the same
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
  if (
    !shouldInjectBanner(env, route, resolved.servedPath) ||
    !isInjectableContentType(resolved.contentType)
  ) {
    return passthrough;
  }

  if (isHead) {
    // No body will be sent. Avoid buffering: injected length = original + banner
    // (injectBanner only inserts, and the inject path is UTF-8 so the round-trip is
    // byte-stable). Omit Content-Length only when the original size is unknown.
    return {
      contentType: resolved.contentType,
      body: null,
      contentLength:
        resolved.contentLength === undefined
          ? undefined
          : resolved.contentLength + BANNER_BYTE_LENGTH,
    };
  }

  const original = resolved.body ? await new Response(resolved.body).text() : "";
  const injected = injectBanner(original);
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
 */
function cacheKey(route: RouteValue, url: URL): Request {
  const keyUrl = new URL(url.toString());
  keyUrl.search = ""; // static content does not vary by query string
  // Fold access_mode + version + plan_tier into the key path so neither an access
  // change, a publish/rollback, nor a plan change can ever serve a stale or
  // cross-mode/cross-tier cache entry. plan_tier is optional (older projections) →
  // "_" stands in for "absent".
  const tier = route.plan_tier ?? "_";
  keyUrl.pathname = `/${route.access_mode}/${route.version_id}/${tier}${keyUrl.pathname}`;
  return new Request(keyUrl.toString(), { method: "GET" });
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

  // The platform default 404 is our own page → strict platform headers.
  const headers = new Headers({
    "Content-Type": "text/html; charset=utf-8",
    "Cache-Control": "public, max-age=30",
    ...platformSecurityHeaders(),
  });
  return new Response(DEFAULT_404_HTML, { status: 404, headers });
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
