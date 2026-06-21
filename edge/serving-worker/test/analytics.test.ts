// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the per-site visit analytics helpers (src/analytics.ts):
// - isVisit:         only GET-of-HTML counts (assets/HEAD excluded)
// - dailyVisitorId:  deterministic within a UTC day, rotates daily, no PII leak
// - buildVisitPayload: the PostHog /capture shape carries site/org/env props
// - captureSiteVisit:  no-ops without a key / for non-pages; POSTs otherwise
//
// Runs on the node pool; crypto.subtle + TextEncoder are Node 20 globals.

import { describe, expect, it, vi } from "vitest";

import {
  buildVisitPayload,
  captureSiteVisit,
  dailyVisitorId,
  isVisit,
  type CaptureFetch,
} from "../src/analytics";
import type { RouteValue } from "../src/route";

const ROUTE: RouteValue = {
  org_id: "11111111-1111-1111-1111-111111111111",
  site_id: "22222222-2222-2222-2222-222222222222",
  version_id: "33333333-3333-3333-3333-333333333333",
  access_mode: "public",
  schema_version: 1,
} as RouteValue;

describe("isVisit", () => {
  it("counts only GET requests for HTML documents", () => {
    expect(isVisit("GET", "text/html; charset=utf-8")).toBe(true);
    expect(isVisit("GET", "text/html")).toBe(true);
  });

  it("excludes non-GET methods", () => {
    expect(isVisit("HEAD", "text/html")).toBe(false);
    expect(isVisit("POST", "text/html")).toBe(false);
  });

  it("excludes non-HTML assets and missing content types", () => {
    expect(isVisit("GET", "text/css")).toBe(false);
    expect(isVisit("GET", "application/javascript")).toBe(false);
    expect(isVisit("GET", "image/png")).toBe(false);
    expect(isVisit("GET", null)).toBe(false);
  });
});

describe("dailyVisitorId", () => {
  const base = { ip: "203.0.113.7", ua: "Mozilla/5.0", salt: "s3cr3t" };

  it("is a 32-char hex string", async () => {
    const id = await dailyVisitorId({ ...base, now: new Date("2026-06-21T10:00:00Z") });
    expect(id).toMatch(/^[0-9a-f]{32}$/);
  });

  it("is stable for the same visitor within a UTC day", async () => {
    const morning = await dailyVisitorId({ ...base, now: new Date("2026-06-21T01:00:00Z") });
    const evening = await dailyVisitorId({ ...base, now: new Date("2026-06-21T23:59:00Z") });
    expect(morning).toBe(evening);
  });

  it("rotates across days (no stable cross-day identifier)", async () => {
    const day1 = await dailyVisitorId({ ...base, now: new Date("2026-06-21T10:00:00Z") });
    const day2 = await dailyVisitorId({ ...base, now: new Date("2026-06-22T10:00:00Z") });
    expect(day1).not.toBe(day2);
  });

  it("differs by IP, by UA, and by salt", async () => {
    const now = new Date("2026-06-21T10:00:00Z");
    const id = await dailyVisitorId({ ...base, now });
    expect(await dailyVisitorId({ ...base, ip: "198.51.100.1", now })).not.toBe(id);
    expect(await dailyVisitorId({ ...base, ua: "curl/8", now })).not.toBe(id);
    expect(await dailyVisitorId({ ...base, salt: "other", now })).not.toBe(id);
  });

  it("does not embed the raw IP or UA in the output", async () => {
    const id = await dailyVisitorId({ ...base, now: new Date("2026-06-21T10:00:00Z") });
    expect(id).not.toContain("203.0.113.7");
    expect(id.toLowerCase()).not.toContain("mozilla");
  });
});

describe("buildVisitPayload", () => {
  it("carries the site/org/version + environment as event properties", () => {
    const payload = buildVisitPayload({
      apiKey: "phc_test",
      distinctId: "abc123",
      host: "acme--docs.dropwaycontent.com",
      path: "/guide/",
      route: ROUTE,
      environment: "production",
      now: new Date("2026-06-21T10:00:00Z"),
    });
    expect(payload).toMatchObject({
      api_key: "phc_test",
      event: "site_visit",
      distinct_id: "abc123",
      timestamp: "2026-06-21T10:00:00.000Z",
      properties: {
        $host: "acme--docs.dropwaycontent.com",
        site_id: ROUTE.site_id,
        org_id: ROUTE.org_id,
        version_id: ROUTE.version_id,
        access_mode: "public",
        path: "/guide/",
        environment: "production",
      },
    });
  });
});

describe("captureSiteVisit", () => {
  function htmlGet(): Request {
    return new Request("https://acme--docs.dropwaycontent.com/guide/", {
      method: "GET",
      headers: { "CF-Connecting-IP": "203.0.113.7", "User-Agent": "Mozilla/5.0" },
    });
  }

  const ctx = (contentType: string | null, request = htmlGet()) => ({
    request,
    route: ROUTE,
    url: new URL(request.url),
    contentType,
    now: new Date("2026-06-21T10:00:00Z"),
  });

  it("no-ops when no POSTHOG_KEY is configured", async () => {
    const fetchImpl = vi.fn<CaptureFetch>();
    await captureSiteVisit({}, ctx("text/html"), fetchImpl);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("no-ops for non-page responses even with a key", async () => {
    const fetchImpl = vi.fn<CaptureFetch>().mockResolvedValue(undefined);
    await captureSiteVisit({ POSTHOG_KEY: "phc_test" }, ctx("text/css"), fetchImpl);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("POSTs a site_visit to the capture endpoint for an HTML page", async () => {
    const fetchImpl = vi.fn<CaptureFetch>().mockResolvedValue(undefined);
    await captureSiteVisit(
      { POSTHOG_KEY: "phc_test", POSTHOG_HOST: "https://eu.posthog.com", DROPWAY_ENV: "staging" },
      ctx("text/html; charset=utf-8"),
      fetchImpl,
    );

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    const [endpoint, init] = fetchImpl.mock.calls[0]!;
    expect(endpoint).toBe("https://eu.posthog.com/capture/");
    expect(init.method).toBe("POST");
    const body = JSON.parse(init.body);
    expect(body.event).toBe("site_visit");
    expect(body.api_key).toBe("phc_test");
    expect(body.properties.environment).toBe("staging");
    expect(body.properties.site_id).toBe(ROUTE.site_id);
    expect(body.distinct_id).toMatch(/^[0-9a-f]{32}$/);
  });

  it("never throws when the capture request fails", async () => {
    const fetchImpl = vi.fn<CaptureFetch>().mockRejectedValue(new Error("network"));
    await expect(
      captureSiteVisit({ POSTHOG_KEY: "phc_test" }, ctx("text/html"), fetchImpl),
    ).resolves.toBeUndefined();
  });
});
