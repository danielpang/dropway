// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Tests for the embed surface (?embed=1): the framable, chrome-stripped rendering
// used to iframe a Dropway site into Notion/Linear/etc. Covers the pure helpers in
// src/embed.ts and the end-to-end serve() behavior through in-memory KV + R2 mocks
// (mirrors serve.test.ts's harness).

import { createHash } from "node:crypto";
import { describe, expect, it } from "vitest";

import { blobKey, type Manifest } from "../src/manifest";
import { routeKey, type RouteValue } from "../src/route";
import { serve, type BucketLike, type Env, type R2ObjectLike } from "../src/index";
import {
  badgeRemovable,
  EMBED_BADGE_BYTE_LENGTH,
  isEmbedRequested,
  shouldShowEmbedBadge,
} from "../src/embed";

// --- Fixtures ---------------------------------------------------------------

const ORG_ID = "11111111-1111-1111-1111-111111111111";
const SITE_ID = "22222222-2222-2222-2222-222222222222";
const VERSION_ID = "33333333-3333-3333-3333-333333333333";
const HOST = "acme.dropwaycontent.com";
const MANIFEST_KEY = `manifests/${ORG_ID}/${SITE_ID}/${VERSION_ID}.json`;

function publicRoute(planTier?: string): RouteValue {
  return {
    org_id: ORG_ID,
    site_id: SITE_ID,
    version_id: VERSION_ID,
    access_mode: "public",
    schema_version: planTier ? 3 : 1,
    ...(planTier ? { plan_tier: planTier } : {}),
  };
}

function gatedRoute(): RouteValue {
  return {
    org_id: ORG_ID,
    site_id: SITE_ID,
    version_id: VERSION_ID,
    access_mode: "org_only",
    schema_version: 1,
  };
}

function sha256(text: string): string {
  return createHash("sha256").update(text).digest("hex");
}

function mockRoutes(routes: Record<string, RouteValue>) {
  function get(key: string, type: "json"): Promise<unknown>;
  function get(key: string): Promise<string | null>;
  async function get(key: string, type?: "json"): Promise<unknown> {
    if (type === "json") return key in routes ? routes[key] : null;
    return null;
  }
  return { get };
}

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
        async json() {
          return JSON.parse(text);
        },
      };
    },
  };
}

/** A one-page deploy: index.html + an optional extra asset. */
function deploy(
  files: Record<string, { body: string; content_type: string }>,
): Record<string, string> {
  const objects: Record<string, string> = {};
  const manifestFiles: Manifest["files"] = {};
  for (const [path, { body, content_type }] of Object.entries(files)) {
    const hash = sha256(body);
    manifestFiles[path] = { sha256: hash, content_type, size: body.length };
    objects[blobKey(ORG_ID, hash)] = body;
  }
  const manifest: Manifest = { schema_version: 1, files: manifestFiles };
  objects[MANIFEST_KEY] = JSON.stringify(manifest);
  return objects;
}

const HTML_BODY = "<!doctype html><html><body><h1>Hello</h1></body></html>";

function envFor(route: RouteValue, objects: Record<string, string>): Env {
  return { ROUTES: mockRoutes({ [routeKey(HOST)]: route }), BUCKET: mockBucket(objects) };
}

function embedReq(path = "/?embed=1"): Request {
  return new Request(`https://${HOST}${path}`, { method: "GET" });
}

function serveNoCache(req: Request, env: Env) {
  return serve(req, env, { cache: null });
}

// --- Pure helpers -----------------------------------------------------------

describe("isEmbedRequested", () => {
  it("detects ?embed (any value)", () => {
    expect(isEmbedRequested(new URL("https://x/?embed=1"))).toBe(true);
    expect(isEmbedRequested(new URL("https://x/?embed"))).toBe(true);
    expect(isEmbedRequested(new URL("https://x/?embed=0"))).toBe(true); // presence, not value
    expect(isEmbedRequested(new URL("https://x/"))).toBe(false);
    expect(isEmbedRequested(new URL("https://x/?other=1"))).toBe(false);
  });
});

describe("badgeRemovable", () => {
  it("is true only for the paid (Pro+) tiers", () => {
    expect(badgeRemovable("pro")).toBe(true);
    expect(badgeRemovable("business")).toBe(true);
    expect(badgeRemovable("enterprise")).toBe(true);
    expect(badgeRemovable(" PRO ")).toBe(true); // normalized
    expect(badgeRemovable("free")).toBe(false);
    expect(badgeRemovable(undefined)).toBe(false);
  });
});

describe("shouldShowEmbedBadge", () => {
  const url = (q: string) => new URL(`https://${HOST}/${q}`);

  it("shows for free tier and ignores badge=0 (not entitled)", () => {
    expect(shouldShowEmbedBadge(publicRoute("free"), url("?embed=1"))).toBe(true);
    expect(shouldShowEmbedBadge(publicRoute("free"), url("?embed=1&badge=0"))).toBe(true);
  });

  it("lets Pro+ suppress it with badge=0, shows otherwise", () => {
    expect(shouldShowEmbedBadge(publicRoute("pro"), url("?embed=1"))).toBe(true);
    expect(shouldShowEmbedBadge(publicRoute("pro"), url("?embed=1&badge=0"))).toBe(false);
    expect(shouldShowEmbedBadge(publicRoute("business"), url("?embed=1&badge=false"))).toBe(false);
  });

  it("never shows without a plan_tier (self-host / legacy projection)", () => {
    expect(shouldShowEmbedBadge(publicRoute(), url("?embed=1"))).toBe(false);
  });
});

// --- End-to-end serve() -----------------------------------------------------

describe("serve() embed — framing headers", () => {
  it("public embed drops X-Frame-Options and widens frame-ancestors", async () => {
    const env = envFor(
      publicRoute("free"),
      deploy({ "index.html": { body: HTML_BODY, content_type: "text/html; charset=utf-8" } }),
    );
    const res = await serveNoCache(embedReq(), env);
    expect(res.status).toBe(200);
    expect(res.headers.get("X-Frame-Options")).toBeNull();
    const csp = res.headers.get("Content-Security-Policy") ?? "";
    expect(csp).toContain("frame-ancestors *");
    expect(csp).not.toContain("frame-ancestors 'none'");
    // CORP widened so a COEP-enforcing parent can still frame it.
    expect(res.headers.get("Cross-Origin-Resource-Policy")).toBe("cross-origin");
    // Other hardening headers survive.
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
  });

  it("a NON-embed request of the same page stays unframable", async () => {
    const env = envFor(
      publicRoute("free"),
      deploy({ "index.html": { body: HTML_BODY, content_type: "text/html; charset=utf-8" } }),
    );
    const res = await serveNoCache(new Request(`https://${HOST}/`), env);
    expect(res.headers.get("X-Frame-Options")).toBe("DENY");
    expect(res.headers.get("Content-Security-Policy")).toContain("frame-ancestors 'none'");
  });
});

describe("serve() embed — badge injection", () => {
  const deployHtml = () =>
    deploy({ "index.html": { body: HTML_BODY, content_type: "text/html; charset=utf-8" } });

  it("injects the badge for a free-tier public embed", async () => {
    const res = await serveNoCache(embedReq(), envFor(publicRoute("free"), deployHtml()));
    const body = await res.text();
    expect(body).toContain("Powered by Dropway");
    expect(body).toContain("dropway-embed-badge");
  });

  it("omits the badge for a Pro+ embed with badge=0", async () => {
    const res = await serveNoCache(
      embedReq("/?embed=1&badge=0"),
      envFor(publicRoute("pro"), deployHtml()),
    );
    const body = await res.text();
    expect(body).not.toContain("Powered by Dropway");
  });

  it("keeps the badge for a free-tier embed even with badge=0", async () => {
    const res = await serveNoCache(
      embedReq("/?embed=1&badge=0"),
      envFor(publicRoute("free"), deployHtml()),
    );
    expect(await res.text()).toContain("Powered by Dropway");
  });

  it("omits the badge when the route has no plan_tier", async () => {
    const res = await serveNoCache(embedReq(), envFor(publicRoute(), deployHtml()));
    expect(await res.text()).not.toContain("Powered by Dropway");
  });

  it("does not inject the badge into a non-HTML asset embed", async () => {
    const css = "body{color:red}";
    const env = envFor(
      publicRoute("free"),
      deploy({ "style.css": { body: css, content_type: "text/css; charset=utf-8" } }),
    );
    const res = await serveNoCache(embedReq("/style.css?embed=1"), env);
    const body = await res.text();
    expect(body).toBe(css);
    expect(body).not.toContain("Powered by Dropway");
    // still framable (defense in depth — the asset is same-origin to the framed doc)
    expect(res.headers.get("X-Frame-Options")).toBeNull();
  });

  it("HEAD reports the badge-inflated Content-Length without a body", async () => {
    const env = envFor(publicRoute("free"), deployHtml());
    const head = await serve(new Request(`https://${HOST}/?embed=1`, { method: "HEAD" }), env, {
      cache: null,
    });
    expect(head.headers.get("Content-Length")).toBe(
      String(HTML_BODY.length + EMBED_BADGE_BYTE_LENGTH),
    );
    expect(await head.text()).toBe("");
  });
});

describe("serve() embed — gated placeholder", () => {
  it("shows a framable 'Sign in to view' placeholder and never the bytes", async () => {
    const env = envFor(
      gatedRoute(),
      deploy({ "index.html": { body: "SECRET CONTENT", content_type: "text/html; charset=utf-8" } }),
    );
    const res = await serveNoCache(embedReq(), env);
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain("Sign in to view");
    expect(body).not.toContain("SECRET CONTENT");
    // Framable so it renders inside the parent iframe.
    expect(res.headers.get("X-Frame-Options")).toBeNull();
    expect(res.headers.get("Content-Security-Policy")).toContain("frame-ancestors *");
    // Never cached (a later access change must be visible immediately).
    expect(res.headers.get("Cache-Control")).toContain("no-store");
    // No 302 to /authz — the embed must not bounce a login through the frame.
    expect(res.headers.get("Location")).toBeNull();
  });

  it("the placeholder links back to the site root without ?embed=1", async () => {
    const env = envFor(gatedRoute(), deploy({}));
    const res = await serveNoCache(embedReq("/deep/path?embed=1"), env);
    const body = await res.text();
    expect(body).toContain(`href="https://${HOST}/"`);
    expect(body).not.toContain("embed=1");
  });
});
