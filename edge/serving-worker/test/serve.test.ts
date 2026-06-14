// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the public serving path's routing + path-resolution logic.
// Everything is exercised through in-memory KV + R2 mocks, so the suite runs
// without a live edge (no Miniflare/Wrangler required for these pure-ish paths).

import { describe, expect, it } from "vitest";

import {
  cleanPath,
  normalizeHost,
  parseRouteValue,
  resolveObjectKeys,
  routeKey,
  type RouteValue,
} from "../src/route";
import {
  cacheControlFor,
  contentTypeFor,
  isHashedAsset,
} from "../src/http";
import { serve, type BucketLike, type Env, type R2ObjectLike } from "../src/index";

// --- Mocks ------------------------------------------------------------------

/** In-memory KV: route:<host> → RouteValue. */
function mockRoutes(routes: Record<string, RouteValue>) {
  return {
    async get(key: string, _type: "json"): Promise<unknown> {
      return key in routes ? routes[key] : null;
    },
  };
}

/** In-memory R2: key → text body. */
function mockBucket(objects: Record<string, string>): BucketLike {
  return {
    async get(key: string): Promise<R2ObjectLike | null> {
      if (!(key in objects)) return null;
      const text = objects[key]!;
      return {
        body: new ReadableStream({
          start(controller) {
            controller.enqueue(new TextEncoder().encode(text));
            controller.close();
          },
        }),
        httpEtag: `"etag-${key.length}"`,
        uploaded: new Date("2026-01-01T00:00:00Z"),
        size: text.length,
      };
    },
  };
}

const PUBLIC_ROUTE: RouteValue = {
  org_id: "org_1",
  site_id: "site_1",
  version_id: "v_abc",
  access_mode: "public",
  schema_version: 1,
};

function envFor(
  route: RouteValue,
  host: string,
  objects: Record<string, string>,
): Env {
  return {
    ROUTES: mockRoutes({ [routeKey(host)]: route }),
    BUCKET: mockBucket(objects),
  };
}

function get(host: string, path: string): Request {
  return new Request(`https://${host}${path}`, { method: "GET" });
}

// --- normalizeHost / routeKey ----------------------------------------------

describe("normalizeHost", () => {
  it("lowercases, strips port and trailing dot", () => {
    expect(normalizeHost("Acme.ShippedUserContent.com")).toBe(
      "acme.shippedusercontent.com",
    );
    expect(normalizeHost("acme.shippedusercontent.com:8787")).toBe(
      "acme.shippedusercontent.com",
    );
    expect(normalizeHost("acme.shippedusercontent.com.")).toBe(
      "acme.shippedusercontent.com",
    );
  });

  it("builds the route key", () => {
    expect(routeKey("Acme.shippedusercontent.com")).toBe(
      "route:acme.shippedusercontent.com",
    );
  });
});

// --- parseRouteValue --------------------------------------------------------

describe("parseRouteValue", () => {
  it("accepts a well-formed public route", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE })).toEqual(PUBLIC_ROUTE);
  });

  it("rejects unknown / mismatched schema_version", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: 2 })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, schema_version: "1" })).toBeNull();
  });

  it("rejects missing fields and bad access_mode", () => {
    expect(parseRouteValue({ ...PUBLIC_ROUTE, org_id: "" })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, version_id: undefined })).toBeNull();
    expect(parseRouteValue({ ...PUBLIC_ROUTE, access_mode: "secret" })).toBeNull();
    expect(parseRouteValue(null)).toBeNull();
    expect(parseRouteValue("nope")).toBeNull();
  });

  it("accepts each valid access mode", () => {
    for (const mode of ["public", "password", "allowlist", "org_only"] as const) {
      expect(parseRouteValue({ ...PUBLIC_ROUTE, access_mode: mode }))
        .not.toBeNull();
    }
  });
});

// --- cleanPath (traversal safety) ------------------------------------------

describe("cleanPath", () => {
  it("strips leading slash and collapses dot segments", () => {
    expect(cleanPath("/a/./b/c")).toBe("a/b/c");
    expect(cleanPath("/")).toBe("");
    expect(cleanPath("/dir/")).toBe("dir/");
  });

  it("rejects parent traversal", () => {
    expect(cleanPath("/../etc/passwd")).toBeNull();
    expect(cleanPath("/a/../../b")).toBeNull();
    expect(cleanPath("/foo/..%2f..%2fbar".replace("%2f", "/"))).toBeNull();
  });

  it("rejects encoded traversal and NUL/backslash", () => {
    expect(cleanPath("/%2e%2e/secret")).toBeNull(); // decodes to ../secret
    expect(cleanPath("/a%00b")).toBeNull();
    expect(cleanPath("/a\\b")).toBeNull();
  });

  it("rejects malformed percent-encoding", () => {
    expect(cleanPath("/%zz")).toBeNull();
  });
});

// --- resolveObjectKeys ------------------------------------------------------

describe("resolveObjectKeys", () => {
  const prefix = "sites/org_1/site_1/v_abc/";

  it("maps root to index.html", () => {
    expect(resolveObjectKeys(PUBLIC_ROUTE, "/")).toEqual([`${prefix}index.html`]);
  });

  it("maps a trailing-slash directory to its index.html", () => {
    expect(resolveObjectKeys(PUBLIC_ROUTE, "/blog/")).toEqual([
      `${prefix}blog/index.html`,
    ]);
  });

  it("serves an explicit asset path directly", () => {
    expect(resolveObjectKeys(PUBLIC_ROUTE, "/assets/app.css")).toEqual([
      `${prefix}assets/app.css`,
    ]);
  });

  it("offers index.html and .html fallbacks for extension-less paths", () => {
    expect(resolveObjectKeys(PUBLIC_ROUTE, "/about")).toEqual([
      `${prefix}about`,
      `${prefix}about/index.html`,
      `${prefix}about.html`,
    ]);
  });

  it("returns no candidates for an unsafe path", () => {
    expect(resolveObjectKeys(PUBLIC_ROUTE, "/../../other-org/secret")).toEqual([]);
  });
});

// --- Content-Type + Cache-Control ------------------------------------------

describe("content type + cache policy", () => {
  it("derives content types", () => {
    expect(contentTypeFor("a/index.html")).toBe("text/html; charset=utf-8");
    expect(contentTypeFor("a/app.css")).toBe("text/css; charset=utf-8");
    expect(contentTypeFor("a/app.js")).toBe("text/javascript; charset=utf-8");
    expect(contentTypeFor("a/logo.svg")).toBe("image/svg+xml");
    expect(contentTypeFor("a/blob")).toBe("application/octet-stream");
  });

  it("flags hashed assets as immutable and HTML as short-lived", () => {
    expect(isHashedAsset("assets/app.4f3a9c2b.js")).toBe(true);
    expect(isHashedAsset("assets/main-9Hs2Kdxx.css")).toBe(true);
    expect(isHashedAsset("index.html")).toBe(false);
    expect(isHashedAsset("assets/logo.svg")).toBe(false);

    expect(cacheControlFor("assets/app.4f3a9c2b.js")).toBe(
      "public, max-age=31536000, immutable",
    );
    expect(cacheControlFor("index.html")).toBe(
      "public, max-age=60, must-revalidate",
    );
  });
});

// --- End-to-end serve() with mocks -----------------------------------------

describe("serve() public path", () => {
  const host = "acme.shippedusercontent.com";

  it("serves index.html at the root with html headers", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {
      "sites/org_1/site_1/v_abc/index.html": "<h1>home</h1>",
    });
    const res = await serve(get(host, "/"), env);
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    expect(res.headers.get("Cache-Control")).toBe(
      "public, max-age=60, must-revalidate",
    );
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
    expect(res.headers.get("Referrer-Policy")).toBe("no-referrer");
    expect(await res.text()).toBe("<h1>home</h1>");
  });

  it("serves a hashed asset immutably", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {
      "sites/org_1/site_1/v_abc/assets/app.4f3a9c2b.js": "console.log(1)",
    });
    const res = await serve(get(host, "/assets/app.4f3a9c2b.js"), env);
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("text/javascript; charset=utf-8");
    expect(res.headers.get("Cache-Control")).toBe(
      "public, max-age=31536000, immutable",
    );
  });

  it("falls back to about/index.html for a pretty path", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {
      "sites/org_1/site_1/v_abc/about/index.html": "<h1>about</h1>",
    });
    const res = await serve(get(host, "/about"), env);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("<h1>about</h1>");
  });

  it("serves the version's custom 404 page when nothing matches", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {
      "sites/org_1/site_1/v_abc/404.html": "<h1>custom missing</h1>",
    });
    const res = await serve(get(host, "/nope"), env);
    expect(res.status).toBe(404);
    expect(await res.text()).toBe("<h1>custom missing</h1>");
  });

  it("serves the default 404 when the site ships none", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {
      "sites/org_1/site_1/v_abc/index.html": "<h1>home</h1>",
    });
    const res = await serve(get(host, "/nope"), env);
    expect(res.status).toBe(404);
    expect(res.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    expect(await res.text()).toContain("404");
  });

  it("404s an unknown host (no route in KV)", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {});
    const res = await serve(get("ghost.shippedusercontent.com", "/"), env);
    expect(res.status).toBe(404);
  });

  it("404s a traversal attempt rather than escaping the prefix", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {
      "sites/org_1/site_1/v_abc/index.html": "<h1>home</h1>",
    });
    const res = await serve(get(host, "/../../other/secret"), env);
    expect(res.status).toBe(404);
  });

  it("never reads a JWT — Authorization header is ignored on the public path",
    async () => {
      const env = envFor(PUBLIC_ROUTE, host, {
        "sites/org_1/site_1/v_abc/index.html": "<h1>home</h1>",
      });
      const req = new Request(`https://${host}/`, {
        method: "GET",
        headers: { Authorization: "Bearer should-be-ignored" },
      });
      const res = await serve(req, env);
      expect(res.status).toBe(200);
      expect(await res.text()).toBe("<h1>home</h1>");
    });

  it("returns an empty body for HEAD but keeps headers", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {
      "sites/org_1/site_1/v_abc/index.html": "<h1>home</h1>",
    });
    const req = new Request(`https://${host}/`, { method: "HEAD" });
    const res = await serve(req, env);
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    expect(await res.text()).toBe("");
  });

  it("405s a non-GET/HEAD method", async () => {
    const env = envFor(PUBLIC_ROUTE, host, {});
    const req = new Request(`https://${host}/`, { method: "POST" });
    const res = await serve(req, env);
    expect(res.status).toBe(405);
    expect(res.headers.get("Allow")).toBe("GET, HEAD");
  });
});

// --- Phase-2 gated stubs ----------------------------------------------------

describe("serve() gated modes are Phase-2 stubs", () => {
  const host = "private.shippedusercontent.com";

  for (const mode of ["password", "allowlist", "org_only"] as const) {
    it(`returns a 501 Phase-2 stub for access_mode=${mode}`, async () => {
      const route: RouteValue = { ...PUBLIC_ROUTE, access_mode: mode };
      const env = envFor(route, host, {
        "sites/org_1/site_1/v_abc/index.html": "<h1>secret</h1>",
      });
      const res = await serve(get(host, "/"), env);
      expect(res.status).toBe(501);
      expect(res.headers.get("X-Shipped-Phase")).toBe("2");
      expect(res.headers.get("X-Shipped-Access-Mode")).toBe(mode);
      // Gated responses must never land in the shared public cache (§10).
      expect(res.headers.get("Cache-Control")).toBe("private, no-store");
      const body = await res.text();
      expect(body).toContain("/authz exchange");
      // It must NOT have streamed the protected object.
      expect(body).not.toContain("secret");
    });
  }
});
