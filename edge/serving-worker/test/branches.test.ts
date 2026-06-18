// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Branch-coverage companion to serve.test.ts. The main suite drives the serving
// flow end-to-end; this file pins the per-branch error/edge behavior of the PURE
// helpers that the flow leans on but only exercises on the happy path:
//   - security headers / service-worker-script matching edge cases
//   - rate-limit boundaries (garbled counter, fail-open on KV error, no-binding)
//   - revocation parse edge cases ({min_iat} envelope variants, boundary `==`)
//   - org-status parse edge cases (malformed JSON, non-string status, KV throw)
//   - manifest / path-resolution edge cases (size field, dotfiles, candidate set)
//   - schema_version handling (route + manifest), content-type / cache policy
//   - gatedConfig env-var resolution + verifyEdgeToken claim validation
//
// All assertions check REAL behavior + the off-happy-path branches; nothing here
// is a coverage-only call. Everything is pure, so no live KV/R2/clock is needed.

import { describe, expect, it } from "vitest";

import {
  cacheControlFor,
  contentTypeFor,
  extensionOf,
  isHashedAsset,
  isHtml,
  publicResponseHeaders,
} from "../src/http";
import {
  applyHeaders,
  CONTENT_CSP,
  PLATFORM_CSP,
  isServiceWorkerRequest,
  isServiceWorkerScript,
} from "../src/security";
import {
  candidatePaths,
  parseManifest,
  resolveManifestEntry,
  type Manifest,
} from "../src/manifest";
import { cleanPath, parseRouteValue, type RouteValue } from "../src/route";
import {
  type CounterKVLike,
  type StatusKVLike,
  isBlockingStatus,
  rateLimitDecision,
  rateLimitIdentity,
  readOrgStatus,
  windowKey,
} from "../src/ratelimit";
import {
  type RevokedKVLike,
  denylistKeys,
  isRevoked,
  parseRevokedEntry,
} from "../src/revoke";
import { EDGE_COOKIE_NAME, EDGE_TOKEN_ISSUER, gatedConfig } from "../src/config";
import { __resetJwksCacheForTests, verifyEdgeToken, type FetchLike } from "../src/edgetoken";
import { SignJWT, exportJWK, generateKeyPair } from "jose";

const ORG_ID = "11111111-1111-1111-1111-111111111111";
const SITE_ID = "22222222-2222-2222-2222-222222222222";
const VERSION_ID = "33333333-3333-3333-3333-333333333333";

const PUBLIC_ROUTE: RouteValue = {
  org_id: ORG_ID,
  site_id: SITE_ID,
  version_id: VERSION_ID,
  access_mode: "public",
  schema_version: 1,
};

// ============================================================================
// http: extension / content-type / cache policy edge branches
// ============================================================================

describe("extensionOf (path → lowercased extension)", () => {
  it("returns '' for a dotfile (leading dot is not an extension)", () => {
    // `.gitignore` / `.env` — the dot is at index 0, so `dot <= 0` → no ext.
    expect(extensionOf(".gitignore")).toBe("");
    expect(extensionOf("dir/.env")).toBe("");
  });

  it("returns '' for a name with no dot, or a trailing-dot name", () => {
    expect(extensionOf("README")).toBe("");
    expect(extensionOf("assets/Makefile")).toBe("");
    // Trailing dot → `dot === last.length - 1` → no extension.
    expect(extensionOf("archive.")).toBe("");
  });

  it("lowercases the extension and uses only the final path segment", () => {
    expect(extensionOf("IMG.PNG")).toBe("png");
    // A dot in a directory name must not be mistaken for the file extension.
    expect(extensionOf("v1.2/index")).toBe("");
    expect(extensionOf("v1.2/app.JS")).toBe("js");
  });
});

describe("contentTypeFor (MIME table coverage + fallback)", () => {
  it("maps the documented media classes to their recorded types", () => {
    const cases: Array<[string, string]> = [
      ["a.svg", "image/svg+xml"],
      ["a.webp", "image/webp"],
      ["a.avif", "image/avif"],
      ["a.woff2", "font/woff2"],
      ["a.woff", "font/woff"],
      ["a.wasm", "application/wasm"],
      ["a.json", "application/json; charset=utf-8"],
      ["a.map", "application/json; charset=utf-8"],
      ["a.mjs", "text/javascript; charset=utf-8"],
      ["a.md", "text/markdown; charset=utf-8"],
      ["a.webmanifest", "application/manifest+json"],
      ["a.ico", "image/x-icon"],
    ];
    for (const [key, type] of cases) {
      expect(contentTypeFor(key)).toBe(type);
    }
  });

  it("falls back to octet-stream for an unknown or absent extension", () => {
    expect(contentTypeFor("file.unknownext")).toBe("application/octet-stream");
    expect(contentTypeFor("LICENSE")).toBe("application/octet-stream");
  });
});

describe("isHtml + isHashedAsset (cache-class heuristics)", () => {
  it("treats .html/.htm as HTML, everything else as not", () => {
    expect(isHtml("index.html")).toBe(true);
    expect(isHtml("page.htm")).toBe(true);
    expect(isHtml("app.js")).toBe(false);
  });

  it("never flags HTML as an immutable hashed asset", () => {
    // Even a fingerprint-looking HTML name stays short-TTL (entry docs flip fast).
    expect(isHashedAsset("page.4f3a9c2b.html")).toBe(false);
  });

  it("requires a >=8-char fingerprint token before the extension", () => {
    // 7 hex chars → not long enough → not immutable.
    expect(isHashedAsset("app.4f3a9c2.js")).toBe(false);
    // 8 chars → immutable. Delimiters: '.', '-', and '_' all qualify.
    expect(isHashedAsset("app.4f3a9c2b.js")).toBe(true);
    expect(isHashedAsset("main-9Hs2KdQ1.css")).toBe(true);
    expect(isHashedAsset("chunk_abcdef0123456789.mjs")).toBe(true);
  });

  it("does not flag a plain, un-fingerprinted asset", () => {
    expect(isHashedAsset("styles.css")).toBe(false);
    expect(isHashedAsset("logo.png")).toBe(false);
  });
});

describe("cacheControlFor + publicResponseHeaders", () => {
  it("immutable for hashed assets, short+revalidate for HTML/plain", () => {
    expect(cacheControlFor("assets/app.4f3a9c2b.js")).toBe(
      "public, max-age=31536000, immutable",
    );
    expect(cacheControlFor("styles.css")).toBe("public, max-age=60, must-revalidate");
    expect(cacheControlFor("index.html")).toBe("public, max-age=60, must-revalidate");
  });

  it("derives Content-Type from the path when none is given, and attaches optional fields", () => {
    const h = publicResponseHeaders("assets/app.4f3a9c2b.js", {
      etag: '"abc"',
      lastModified: new Date("2026-01-02T03:04:05Z"),
      contentLength: 42,
    });
    expect(h.get("Content-Type")).toBe("text/javascript; charset=utf-8");
    expect(h.get("Cache-Control")).toBe("public, max-age=31536000, immutable");
    expect(h.get("ETag")).toBe('"abc"');
    expect(h.get("Last-Modified")).toBe(new Date("2026-01-02T03:04:05Z").toUTCString());
    expect(h.get("Content-Length")).toBe("42");
    // Security headers are always present.
    expect(h.get("X-Content-Type-Options")).toBe("nosniff");
  });

  it("prefers the explicit content type and omits absent optional fields", () => {
    const h = publicResponseHeaders("blob", { contentType: "image/png" });
    expect(h.get("Content-Type")).toBe("image/png");
    expect(h.get("ETag")).toBeNull();
    expect(h.get("Last-Modified")).toBeNull();
    expect(h.get("Content-Length")).toBeNull();
  });
});

// ============================================================================
// security: service-worker matching + applyHeaders empty-value skip
// ============================================================================

describe("isServiceWorkerScript (more conventional names + negatives)", () => {
  it("matches every conventional SW filename case-insensitively, anywhere in the path", () => {
    for (const p of [
      "firebase-messaging-sw.js",
      "workbox-sw.js",
      "sw.min.js",
      "service-worker.min.js",
      "SERVICEWORKER.JS",
      "deeply/nested/dir/SW.JS",
    ]) {
      expect(isServiceWorkerScript(p)).toBe(true);
    }
  });

  it("does NOT match ordinary scripts that merely contain 'sw'", () => {
    for (const p of ["sworld.js", "answers.js", "my-sw-helper.js", "", "sw.js.map"]) {
      expect(isServiceWorkerScript(p)).toBe(false);
    }
  });
});

describe("isServiceWorkerRequest + applyHeaders", () => {
  it("flags a SW-script fetch (Service-Worker: script) regardless of path", () => {
    const sw = new Request("https://acme.dropwaycontent.com/assets/app-worker.js", {
      headers: { "Service-Worker": "script" },
    });
    expect(isServiceWorkerRequest(sw)).toBe(true);
    const normal = new Request("https://acme.dropwaycontent.com/assets/app-worker.js");
    expect(isServiceWorkerRequest(normal)).toBe(false);
  });

  it("applyHeaders sets non-empty values and SKIPS empty-string values", () => {
    const h = new Headers();
    applyHeaders(h, { "X-A": "1", "X-Empty": "", "X-B": "2" });
    expect(h.get("X-A")).toBe("1");
    expect(h.get("X-B")).toBe("2");
    // Empty values are deliberately not set (so an absent header stays absent).
    expect(h.has("X-Empty")).toBe(false);
  });

  it("the two CSPs are distinct: content permits scripts, platform denies all", () => {
    expect(CONTENT_CSP).toContain("script-src");
    expect(PLATFORM_CSP).toContain("default-src 'none'");
    expect(PLATFORM_CSP).not.toContain("script-src");
  });
});

// ============================================================================
// manifest: size field handling + candidate-path branches
// ============================================================================

describe("parseManifest (size field + entry edge cases)", () => {
  it("keeps a valid non-negative size and drops a negative / non-numeric size", () => {
    const ok = parseManifest({
      schema_version: 1,
      files: { "a.js": { sha256: "a".repeat(64), content_type: "text/javascript", size: 10 } },
    });
    expect(ok!.files["a.js"]!.size).toBe(10);

    // Negative size is ignored (entry still valid, just no size).
    const neg = parseManifest({
      schema_version: 1,
      files: { "a.js": { sha256: "a".repeat(64), content_type: "text/javascript", size: -1 } },
    });
    expect(neg!.files["a.js"]!.size).toBeUndefined();

    // Non-number size is ignored too.
    const str = parseManifest({
      schema_version: 1,
      files: { "a.js": { sha256: "a".repeat(64), content_type: "text/javascript", size: "10" } },
    });
    expect(str!.files["a.js"]!.size).toBeUndefined();
  });

  it("rejects an empty content_type and a non-string sha", () => {
    expect(
      parseManifest({
        schema_version: 1,
        files: { x: { sha256: "a".repeat(64), content_type: "" } },
      }),
    ).toBeNull();
    expect(
      parseManifest({
        schema_version: 1,
        files: { x: { sha256: 123, content_type: "text/html" } },
      }),
    ).toBeNull();
  });

  it("rejects a non-object / array entry, and a missing files map", () => {
    expect(parseManifest({ schema_version: 1, files: { x: null } })).toBeNull();
    expect(parseManifest({ schema_version: 1, files: { x: [] } })).toBeNull();
    expect(parseManifest({ schema_version: 1 })).toBeNull();
    expect(parseManifest({ schema_version: 1, files: null })).toBeNull();
  });

  it("accepts an empty (but present) files map", () => {
    const m = parseManifest({ schema_version: 1, files: {} });
    expect(m).not.toBeNull();
    expect(Object.keys(m!.files)).toHaveLength(0);
  });
});

describe("candidatePaths (resolution-order branches)", () => {
  it("a path WITH an extension gets no pretty fallbacks", () => {
    expect(candidatePaths("assets/app.css")).toEqual(["assets/app.css"]);
    expect(candidatePaths("favicon.ico")).toEqual(["favicon.ico"]);
  });

  it("an extension-less nested path offers index.html then .html", () => {
    expect(candidatePaths("docs/guide")).toEqual([
      "docs/guide",
      "docs/guide/index.html",
      "docs/guide.html",
    ]);
  });

  it("a trailing-slash subdir maps only to its index.html", () => {
    expect(candidatePaths("docs/")).toEqual(["docs/index.html"]);
  });

  it("a dotfile-looking last segment counts as having an extension (no fallback)", () => {
    // `.well-known/security.txt` has an extension on the last segment.
    expect(candidatePaths(".well-known/security.txt")).toEqual([
      ".well-known/security.txt",
    ]);
  });
});

describe("resolveManifestEntry (fallback precedence)", () => {
  const manifest: Manifest = {
    schema_version: 1,
    files: {
      "about.html": { sha256: "a".repeat(64), content_type: "text/html" },
      "guide/index.html": { sha256: "b".repeat(64), content_type: "text/html" },
    },
  };

  it("prefers <path>/index.html over <path>.html when both could match", () => {
    // `guide` → tries `guide` (miss), then `guide/index.html` (HIT) before `.html`.
    expect(resolveManifestEntry(manifest, "guide")?.path).toBe("guide/index.html");
  });

  it("falls through to <path>.html when there is no directory index", () => {
    expect(resolveManifestEntry(manifest, "about")?.path).toBe("about.html");
  });

  it("returns null when the request has an extension and no exact match", () => {
    expect(resolveManifestEntry(manifest, "missing.css")).toBeNull();
  });
});

// ============================================================================
// route: cleanPath + schema_version branches
// ============================================================================

describe("cleanPath (additional sanitization branches)", () => {
  it("collapses multiple leading slashes and strips a trailing query/hash", () => {
    expect(cleanPath("///a/b")).toBe("a/b");
    expect(cleanPath("/a/b?x=1")).toBe("a/b");
    expect(cleanPath("/a/b#frag")).toBe("a/b");
  });

  it("drops empty and '.' segments but keeps the trailing slash only for non-root", () => {
    expect(cleanPath("/a//b/./c")).toBe("a/b/c");
    expect(cleanPath("/a/b/")).toBe("a/b/");
    // Root collapses to "" (no trailing slash retained for the empty rel).
    expect(cleanPath("/")).toBe("");
  });

  it("rejects a '..' segment anywhere, even mid-path", () => {
    expect(cleanPath("/a/../b")).toBeNull();
    expect(cleanPath("/a/b/..")).toBeNull();
  });

  it("rejects decoded NUL and backslash; rejects bad percent-encoding", () => {
    expect(cleanPath("/a%00")).toBeNull();
    expect(cleanPath("/a%5Cb")).toBeNull(); // %5C decodes to backslash
    expect(cleanPath("/%")).toBeNull();
  });
});

describe("parseRouteValue (schema_version + access_mode boundaries)", () => {
  it("accepts MIN (1) and MAX (2) schema versions, rejects below/above", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 1 })).not.toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 2 })).not.toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 0 })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 3 })).toBeNull();
  });

  it("parses expires_at independent of schema_version, but rejects a malformed timestamp", () => {
    // The contract validates expires_at as an optional RFC3339 field regardless of
    // schema_version; a valid timestamp parses through, a malformed one fails closed.
    const parsed = parseRouteValue({
      ...PUBLIC_ROUTE,
      schema_version: 2,
      expires_at: "2030-01-01T00:00:00Z",
    });
    expect(parsed?.expires_at).toBe("2030-01-01T00:00:00Z");
    expect(
      parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 2, expires_at: "yesterday" }),
    ).toBeNull();
  });

  it("rejects a genuinely unknown field (additionalProperties: false)", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE, password_hash: "x" })).toBeNull();
  });

  it("round-trips every access mode and rejects an unknown one", () => {
    for (const mode of ["public", "password", "allowlist", "org_only"] as const) {
      expect(parseRouteValue({ ...PUBLIC_ROUTE, access_mode: mode })?.access_mode).toBe(mode);
    }
    expect(parseRouteValue({ ...PUBLIC_ROUTE, access_mode: "internal" })).toBeNull();
  });
});

// ============================================================================
// ratelimit: garbled counter, fail-open, identity, window key, org-status parse
// ============================================================================

/** In-memory counter KV (get/put). */
function mockCounterKV(seed: Record<string, string> = {}): CounterKVLike & {
  store: Map<string, string>;
} {
  const store = new Map<string, string>(Object.entries(seed));
  return {
    store,
    async get(key) {
      return store.has(key) ? store.get(key)! : null;
    },
    async put(key, value) {
      store.set(key, value);
    },
  };
}

describe("rateLimitDecision (boundary + defensive branches)", () => {
  const NOW = 1_700_000_000_000;

  it("treats a garbled stored counter as 0 (parseCount defensiveness)", async () => {
    const policy = { limit: 1, windowSeconds: 60 };
    const id = "ip:5.5.5.5";
    // Seed the CURRENT window key with garbage; the next read must treat it as 0,
    // so the first request is the 1st (allowed), not "garbage+1".
    const kv = mockCounterKV({ [windowKey(id, NOW, policy)]: "not-a-number" });
    const r = await rateLimitDecision(kv, id, NOW, policy);
    expect(r.allowed).toBe(true);
    expect(r.count).toBe(1);
  });

  it("fails OPEN when the counter KV get throws", async () => {
    const throwing: CounterKVLike = {
      async get() {
        throw new Error("KV down");
      },
      async put() {},
    };
    const r = await rateLimitDecision(throwing, "ip:1.2.3.4", NOW, { limit: 1, windowSeconds: 60 });
    expect(r.allowed).toBe(true);
  });

  it("swallows a failing put (best-effort) and still allows the request", async () => {
    const flakyPut: CounterKVLike = {
      async get() {
        return null;
      },
      async put() {
        throw new Error("put failed");
      },
    };
    const r = await rateLimitDecision(flakyPut, "ip:1.1.1.1", NOW, { limit: 5, windowSeconds: 60 });
    expect(r.allowed).toBe(true);
    expect(r.count).toBe(1);
  });

  it("computes retryAfter within (0, windowSeconds] when over the limit", async () => {
    const policy = { limit: 1, windowSeconds: 30 };
    const kv = mockCounterKV();
    const id = "ip:7.7.7.7";
    await rateLimitDecision(kv, id, NOW, policy); // count 1 (allowed)
    const over = await rateLimitDecision(kv, id, NOW, policy); // count 2 (denied)
    expect(over.allowed).toBe(false);
    expect(over.retryAfterSeconds).toBeGreaterThan(0);
    expect(over.retryAfterSeconds).toBeLessThanOrEqual(30);
  });

  it("X-Real-IP is used when CF-Connecting-IP is absent; whitespace-only IP falls back to host", () => {
    const realIp = new Request("https://h/", { headers: { "X-Real-IP": "192.0.2.5" } });
    expect(rateLimitIdentity(realIp, "h")).toBe("ip:192.0.2.5");
    const blankIp = new Request("https://h/", { headers: { "CF-Connecting-IP": "   " } });
    expect(rateLimitIdentity(blankIp, "acme.example")).toBe("host:acme.example");
  });
});

describe("readOrgStatus (parse edge cases) + isBlockingStatus", () => {
  it("isBlockingStatus only blocks suspended/over_limit", () => {
    expect(isBlockingStatus("suspended")).toBe(true);
    expect(isBlockingStatus("over_limit")).toBe(true);
    expect(isBlockingStatus("active")).toBe(false);
    expect(isBlockingStatus("")).toBe(false);
    expect(isBlockingStatus("paused")).toBe(false);
  });

  it("returns null for an absent binding and for a clean miss", async () => {
    expect(await readOrgStatus(undefined, ORG_ID)).toBeNull();
    expect(await readOrgStatus(mockCounterKV(), ORG_ID)).toBeNull();
  });

  it("reads a bare status; trims surrounding whitespace", async () => {
    const kv = mockCounterKV({ [`org_status:${ORG_ID}`]: "  suspended  " });
    expect(await readOrgStatus(kv, ORG_ID)).toBe("suspended");
  });

  it("reads {status} from a JSON envelope but null when status is non-string or absent", async () => {
    const ok = mockCounterKV({ [`org_status:${ORG_ID}`]: JSON.stringify({ status: "over_limit" }) });
    expect(await readOrgStatus(ok, ORG_ID)).toBe("over_limit");

    const nonString = mockCounterKV({ [`org_status:${ORG_ID}`]: JSON.stringify({ status: 7 }) });
    expect(await readOrgStatus(nonString, ORG_ID)).toBeNull();

    const noStatus = mockCounterKV({ [`org_status:${ORG_ID}`]: JSON.stringify({ reason: "x" }) });
    expect(await readOrgStatus(noStatus, ORG_ID)).toBeNull();
  });

  it("returns null for a malformed JSON envelope (open brace, bad json)", async () => {
    const kv = mockCounterKV({ [`org_status:${ORG_ID}`]: "{not json" });
    expect(await readOrgStatus(kv, ORG_ID)).toBeNull();
  });

  it("fails OPEN (null) when the status KV get throws", async () => {
    const throwing: StatusKVLike = {
      async get() {
        throw new Error("KV down");
      },
    };
    expect(await readOrgStatus(throwing, ORG_ID)).toBeNull();
  });
});

// ============================================================================
// revoke: parse envelope variants + the iat == min_iat boundary, per dimension
// ============================================================================

function mockRevokedKV(seed: Record<string, string> = {}): RevokedKVLike {
  const store = new Map<string, string>(Object.entries(seed));
  return {
    async get(key) {
      return store.has(key) ? store.get(key)! : null;
    },
  };
}

describe("parseRevokedEntry (envelope + bare + malformed)", () => {
  it("accepts {min_iat} and a bare numeric string", () => {
    expect(parseRevokedEntry(JSON.stringify({ min_iat: 1700 }))).toEqual({ min_iat: 1700 });
    expect(parseRevokedEntry("1700")).toEqual({ min_iat: 1700 });
    expect(parseRevokedEntry("0")).toEqual({ min_iat: 0 });
  });

  it("returns null for absent / empty / whitespace-only values", () => {
    expect(parseRevokedEntry(null)).toBeNull();
    expect(parseRevokedEntry("")).toBeNull();
    expect(parseRevokedEntry("   ")).toBeNull();
  });

  it("returns null for malformed JSON and a missing min_iat field", () => {
    expect(parseRevokedEntry("{ broken")).toBeNull();
    expect(parseRevokedEntry(JSON.stringify({ other: 1 }))).toBeNull();
  });

  it("rejects a non-numeric / negative / non-finite min_iat", () => {
    expect(parseRevokedEntry(JSON.stringify({ min_iat: "1700" }))).toBeNull();
    expect(parseRevokedEntry(JSON.stringify({ min_iat: -1 }))).toBeNull();
    // A bare non-numeric string parses to NaN → null.
    expect(parseRevokedEntry("abc")).toBeNull();
  });
});

describe("isRevoked (boundary semantics, all three dimensions)", () => {
  const subject = { sub: "u1", siteId: "s1", orgId: "o1", iat: 1000 };

  it("treats iat == min_iat as NOT revoked (min_iat is the first valid second)", async () => {
    // The strict `>` means a token minted exactly at min_iat survives.
    for (const key of ["revoked:user:u1", "revoked:site:s1", "revoked:org:o1"]) {
      expect(await isRevoked(mockRevokedKV({ [key]: JSON.stringify({ min_iat: 1000 }) }), subject)).toBe(
        false,
      );
    }
  });

  it("revokes when min_iat is one second past iat, in any single dimension", async () => {
    for (const key of ["revoked:user:u1", "revoked:site:s1", "revoked:org:o1"]) {
      expect(await isRevoked(mockRevokedKV({ [key]: JSON.stringify({ min_iat: 1001 }) }), subject)).toBe(
        true,
      );
    }
  });

  it("a token with iat=0 (missing) is revoked by any positive min_iat", async () => {
    expect(
      await isRevoked(mockRevokedKV({ "revoked:user:u1": "1" }), { ...subject, iat: 0 }),
    ).toBe(true);
  });

  it("an unparseable present denylist key is treated as no-constraint for that dimension", async () => {
    // Garbled user entry → parsed as null → not revoked (a clean denylist serves).
    expect(await isRevoked(mockRevokedKV({ "revoked:user:u1": "garbage" }), subject)).toBe(false);
  });

  it("builds the three keys from the contract prefixes", () => {
    const k = denylistKeys("U", "S", "O");
    expect(k).toEqual({ user: "revoked:user:U", site: "revoked:site:S", org: "revoked:org:O" });
  });
});

// ============================================================================
// config: gatedConfig env-var resolution (defaults / override / blank)
// ============================================================================

describe("gatedConfig (env-var resolution)", () => {
  const baseEnv = {} as Parameters<typeof gatedConfig>[0];

  it("falls back to the production defaults when both vars are unset", () => {
    const cfg = gatedConfig(baseEnv);
    expect(cfg.jwksUrl).toBe("https://api.dropway.dev/.well-known/edge-jwks");
    expect(cfg.appAuthzUrl).toBe("https://app.dropway.dev/authz");
    expect(cfg.issuer).toBe(EDGE_TOKEN_ISSUER);
  });

  it("uses explicit overrides and trims them", () => {
    const cfg = gatedConfig({
      EDGE_JWKS_URL: "  https://api.test/jwks  ",
      APP_AUTHZ_URL: "https://app.test/authz",
    } as Parameters<typeof gatedConfig>[0]);
    expect(cfg.jwksUrl).toBe("https://api.test/jwks");
    expect(cfg.appAuthzUrl).toBe("https://app.test/authz");
  });

  it("treats a blank / whitespace-only var as unset (default)", () => {
    const cfg = gatedConfig({
      EDGE_JWKS_URL: "   ",
      APP_AUTHZ_URL: "",
    } as Parameters<typeof gatedConfig>[0]);
    expect(cfg.jwksUrl).toBe("https://api.dropway.dev/.well-known/edge-jwks");
    expect(cfg.appAuthzUrl).toBe("https://app.dropway.dev/authz");
  });

  it("the edge cookie name is the host-locked __Host- prefix", () => {
    expect(EDGE_COOKIE_NAME).toBe("__Host-edge");
  });
});

// ============================================================================
// edgetoken: verifyEdgeToken claim-level validation, called directly (unit)
// ============================================================================

const GATED_HOST = "private.dropwaycontent.com";
const JWKS_URL = "https://api.test/.well-known/edge-jwks";

async function makeSigner(kid = "edge-kid") {
  const { publicKey, privateKey } = await generateKeyPair("EdDSA", { extractable: true });
  const pubJwk = await exportJWK(publicKey);
  const jwks = { keys: [{ ...pubJwk, kid, use: "sig", alg: "EdDSA" }] };
  const fetchImpl: FetchLike = async (input) =>
    input === JWKS_URL
      ? { ok: true, status: 200, json: async () => jwks }
      : { ok: false, status: 404, json: async () => ({}) };

  async function mint(claims: Record<string, unknown>, opts: { omitIat?: boolean } = {}) {
    const now = Math.floor(Date.now() / 1000);
    const b = new SignJWT(claims)
      .setProtectedHeader({ alg: "EdDSA", kid })
      .setIssuer(EDGE_TOKEN_ISSUER)
      .setAudience(GATED_HOST)
      .setExpirationTime(now + 900);
    if (!opts.omitIat) b.setIssuedAt(now);
    return b.sign(privateKey);
  }
  return { jwks, fetchImpl, mint };
}

describe("verifyEdgeToken (claim validation branches)", () => {
  const validClaims = { sub: "44444444-4444-4444-4444-444444444444", site_id: SITE_ID, mode: "org_only" };

  it("returns null immediately for an empty token / host / siteId (no JWKS fetch)", async () => {
    __resetJwksCacheForTests();
    let fetched = false;
    const fetchImpl: FetchLike = async () => {
      fetched = true;
      return { ok: false, status: 500, json: async () => ({}) };
    };
    expect(
      await verifyEdgeToken({ token: "", host: GATED_HOST, siteId: SITE_ID, jwksUrl: JWKS_URL, fetchImpl }),
    ).toBeNull();
    expect(
      await verifyEdgeToken({ token: "x", host: "", siteId: SITE_ID, jwksUrl: JWKS_URL, fetchImpl }),
    ).toBeNull();
    expect(
      await verifyEdgeToken({ token: "x", host: GATED_HOST, siteId: "", jwksUrl: JWKS_URL, fetchImpl }),
    ).toBeNull();
    expect(fetched).toBe(false);
  });

  it("accepts a fully valid token and surfaces sub/aud/site_id/mode/iat", async () => {
    __resetJwksCacheForTests();
    const s = await makeSigner();
    const token = await s.mint(validClaims);
    const claims = await verifyEdgeToken({
      token,
      host: GATED_HOST,
      siteId: SITE_ID,
      jwksUrl: JWKS_URL,
      fetchImpl: s.fetchImpl,
    });
    expect(claims).not.toBeNull();
    expect(claims!.sub).toBe(validClaims.sub);
    expect(claims!.aud).toBe(GATED_HOST);
    expect(claims!.site_id).toBe(SITE_ID);
    expect(claims!.mode).toBe("org_only");
    expect(claims!.iat).toBeGreaterThan(0);
  });

  it("rejects a token whose site_id claim mismatches the route's site", async () => {
    __resetJwksCacheForTests();
    const s = await makeSigner();
    const token = await s.mint({ ...validClaims, site_id: "99999999-9999-9999-9999-999999999999" });
    expect(
      await verifyEdgeToken({ token, host: GATED_HOST, siteId: SITE_ID, jwksUrl: JWKS_URL, fetchImpl: s.fetchImpl }),
    ).toBeNull();
  });

  it("rejects an unknown mode claim and a missing site_id claim", async () => {
    __resetJwksCacheForTests();
    const s = await makeSigner();
    const badMode = await s.mint({ ...validClaims, mode: "superuser" });
    expect(
      await verifyEdgeToken({ token: badMode, host: GATED_HOST, siteId: SITE_ID, jwksUrl: JWKS_URL, fetchImpl: s.fetchImpl }),
    ).toBeNull();

    __resetJwksCacheForTests();
    const s2 = await makeSigner();
    const noSite = await s2.mint({ sub: validClaims.sub, mode: "org_only" });
    expect(
      await verifyEdgeToken({ token: noSite, host: GATED_HOST, siteId: SITE_ID, jwksUrl: JWKS_URL, fetchImpl: s2.fetchImpl }),
    ).toBeNull();
  });

  it("reads iat=0 when the token omits iat (so any positive min_iat revokes it)", async () => {
    __resetJwksCacheForTests();
    const s = await makeSigner();
    // requiredClaims forces exp+sub but NOT iat, so an iat-less token still verifies.
    const token = await s.mint(validClaims, { omitIat: true });
    const claims = await verifyEdgeToken({
      token,
      host: GATED_HOST,
      siteId: SITE_ID,
      jwksUrl: JWKS_URL,
      fetchImpl: s.fetchImpl,
    });
    expect(claims).not.toBeNull();
    expect(claims!.iat).toBe(0);
  });

  it("throws on a cold-cache JWKS outage (caller fails closed by 302)", async () => {
    __resetJwksCacheForTests();
    const s = await makeSigner();
    const token = await s.mint(validClaims);
    const downFetch: FetchLike = async () => ({ ok: false, status: 503, json: async () => ({}) });
    await expect(
      verifyEdgeToken({ token, host: GATED_HOST, siteId: SITE_ID, jwksUrl: JWKS_URL, fetchImpl: downFetch }),
    ).rejects.toThrow();
  });

  it("skips non-OKP keys in the JWKS, then throws when no usable key remains (cold cache)", async () => {
    __resetJwksCacheForTests();
    const s = await makeSigner();
    const token = await s.mint(validClaims);
    // A JWKS that carries only an RSA key (wrong kty) → no usable Ed25519 key.
    const rsaOnlyFetch: FetchLike = async () => ({
      ok: true,
      status: 200,
      json: async () => ({ keys: [{ kty: "RSA", kid: "rsa-1", n: "x", e: "AQAB" }] }),
    });
    await expect(
      verifyEdgeToken({ token, host: GATED_HOST, siteId: SITE_ID, jwksUrl: JWKS_URL, fetchImpl: rsaOnlyFetch }),
    ).rejects.toThrow();
  });
});
