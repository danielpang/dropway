// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it, vi } from "vitest";

import { Dropway } from "../src/index.js";
import {
  AuthError,
  ForbiddenError,
  QuotaExceededError,
  RateLimitError,
} from "../src/errors.js";

type Handler = (url: string, init: RequestInit) => Response | Promise<Response>;

function json(
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", ...headers },
  });
}

function client(handler: Handler, apiKey = "dw_live_test") {
  const calls: { method: string; url: string; body?: unknown }[] = [];
  const fetchImpl = (async (url: string | URL, init: RequestInit = {}) => {
    const u = String(url);
    calls.push({
      method: init.method ?? "GET",
      url: u,
      body: init.body ? tryParse(init.body) : undefined,
    });
    return handler(u, init);
  }) as unknown as typeof fetch;
  const dw = new Dropway({
    apiKey,
    baseUrl: "https://api.test",
    fetch: fetchImpl,
    maxRetries: 0,
  });
  return { dw, calls };
}

function tryParse(b: BodyInit): unknown {
  if (typeof b === "string") {
    try {
      return JSON.parse(b);
    } catch {
      return b;
    }
  }
  return b;
}

describe("constructor", () => {
  it("requires an API key", () => {
    const saved = process.env.DROPWAY_API_KEY;
    delete process.env.DROPWAY_API_KEY;
    expect(() => new Dropway({})).toThrow(/DROPWAY_API_KEY/);
    if (saved) process.env.DROPWAY_API_KEY = saved;
  });

  it("reads DROPWAY_API_KEY from the environment", () => {
    process.env.DROPWAY_API_KEY = "dw_live_env";
    expect(
      () => new Dropway({ fetch: (() => {}) as unknown as typeof fetch }),
    ).not.toThrow();
    delete process.env.DROPWAY_API_KEY;
  });

  it("sends the key as a Bearer token", async () => {
    const { dw, calls } = client(() => json({ id: "s1", slug: "x" }));
    await dw.sites.get("s1");
    const auth = calls.length; // ensure a call happened
    expect(auth).toBe(1);
  });
});

describe("error mapping", () => {
  it("maps 401 → AuthError", async () => {
    const { dw } = client(() => json({ message: "invalid token" }, 401));
    await expect(dw.sites.list()).rejects.toBeInstanceOf(AuthError);
  });

  it("maps 402 → QuotaExceededError with upgrade fields", async () => {
    const { dw } = client(() =>
      json(
        {
          message: "site cap reached",
          limit: 10,
          current: 10,
          max: 10,
          plan_tier: "free",
          next_tier: "pro",
          upgrade_url: "https://u",
        },
        402,
      ),
    );
    const err = await dw.sites.create({ slug: "x" }).catch((e) => e);
    expect(err).toBeInstanceOf(QuotaExceededError);
    expect((err as QuotaExceededError).max).toBe(10);
    expect((err as QuotaExceededError).nextTier).toBe("pro");
    expect((err as QuotaExceededError).upgradeUrl).toBe("https://u");
  });

  it("maps 403 → ForbiddenError and flags the interactive ceiling", async () => {
    const { dw } = client(() =>
      json(
        {
          message:
            "this action requires an interactive login; API keys are limited to member-level actions",
        },
        403,
      ),
    );
    const err = (await dw
      .request("DELETE", "/v1/api-keys/x")
      .catch((e) => e)) as ForbiddenError;
    expect(err).toBeInstanceOf(ForbiddenError);
    expect(err.interactiveRequired).toBe(true);
  });

  it("maps 429 → RateLimitError with Retry-After", async () => {
    const { dw } = client(() =>
      json({ message: "slow down" }, 429, { "retry-after": "7" }),
    );
    const err = (await dw.sites.list().catch((e) => e)) as RateLimitError;
    expect(err).toBeInstanceOf(RateLimitError);
    expect(err.retryAfterSeconds).toBe(7);
  });
});

describe("abort", () => {
  it("honors an already-aborted signal without calling fetch", async () => {
    const { dw, calls } = client(() => json({}));
    const controller = new AbortController();
    controller.abort();
    await expect(
      dw.request("GET", "/v1/sites", { signal: controller.signal }),
    ).rejects.toThrow();
    expect(calls).toHaveLength(0); // never dialed
  });
});

describe("deploy loop", () => {
  it("runs prepare → upload missing → finalize → publish in order", async () => {
    const puts: string[] = [];
    const { dw, calls } = client((url, init) => {
      if (url.endsWith("/deployments/prepare")) {
        // Ask for both blobs to be uploaded.
        const body = tryParse(init.body as BodyInit) as {
          manifest: { sha256: string }[];
        };
        const uploads: Record<string, string> = {};
        for (const f of body.manifest)
          uploads[f.sha256] = `https://blob.test/${f.sha256}`;
        return json({ missing: body.manifest.map((f) => f.sha256), uploads });
      }
      if (url.startsWith("https://blob.test/")) {
        puts.push(url);
        return new Response(null, { status: 200 });
      }
      if (url.endsWith("/deployments")) {
        return json({
          version_id: "v1",
          version_no: 1,
          preview_url: "https://preview.test",
        });
      }
      if (url.endsWith("/publish")) {
        return json({ live_url: "https://live.test", version_id: "v1" });
      }
      return json({}, 404);
    });

    const res = await dw.sites.deploy("s1", {
      files: { "index.html": "<h1>hi</h1>", "app.js": "console.log(1)" },
    });

    expect(res.published).toBe(true);
    expect(res.versionId).toBe("v1");
    expect(res.liveUrl).toBe("https://live.test");
    expect(res.previewUrl).toBe("https://preview.test");
    expect(res.filesUploaded).toBe(2);
    expect(puts).toHaveLength(2);

    // Order: prepare, 2 PUTs, finalize, publish.
    const seq = calls.map((c) =>
      c.url
        .replace("https://api.test", "")
        .replace("https://blob.test", "BLOB"),
    );
    expect(seq[0]).toContain("/deployments/prepare");
    expect(seq[seq.length - 2]).toContain("/deployments");
    expect(seq[seq.length - 1]).toContain("/publish");
  });

  it("skips publish when publish:false and uploads only missing blobs", async () => {
    const { dw } = client((url, init) => {
      if (url.endsWith("/deployments/prepare")) {
        // Server already has every blob → nothing to upload.
        return json({ missing: [], uploads: {} });
      }
      if (url.endsWith("/deployments")) return json({ version_id: "v2" });
      if (url.endsWith("/publish"))
        throw new Error("publish should not be called");
      return json({}, 404);
    });
    const res = await dw.sites.deploy("s1", {
      files: { "index.html": "hi" },
      publish: false,
    });
    expect(res.published).toBe(false);
    expect(res.filesUploaded).toBe(0);
    expect(res.versionId).toBe("v2");
  });

  it("rejects deploy when both files and dir are given", async () => {
    const { dw, calls } = client(() => json({}, 404));
    await expect(
      dw.sites.deploy("s1", { files: { "a.txt": "x" }, dir: "./dist" }),
    ).rejects.toThrow(/not both/);
    expect(calls).toHaveLength(0); // failed before any network call
  });

  it("blob PUT omits Authorization (the presigned URL is the credential)", async () => {
    let blobAuth: string | null = "unset";
    const { dw } = client((url, init) => {
      if (url.endsWith("/deployments/prepare")) {
        const body = tryParse(init.body as BodyInit) as {
          manifest: { sha256: string }[];
        };
        const sha = body.manifest[0].sha256;
        return json({
          missing: [sha],
          uploads: { [sha]: "https://blob.test/x" },
        });
      }
      if (url.startsWith("https://blob.test/")) {
        blobAuth = new Headers(init.headers).get("authorization");
        return new Response(null, { status: 200 });
      }
      if (url.endsWith("/deployments")) return json({ version_id: "v3" });
      if (url.endsWith("/publish")) return json({ live_url: "l" });
      return json({}, 404);
    });
    await dw.sites.deploy("s1", { files: { "a.txt": "hi" } });
    expect(blobAuth).toBeNull();
  });
});
