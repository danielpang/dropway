// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Dropway } from "../src/index.js";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

let dir: string;

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), "dw-sdk-"));
  await writeFile(join(dir, "index.html"), "<h1>root</h1>");
  await mkdir(join(dir, "assets"), { recursive: true });
  await writeFile(join(dir, "assets", "app.js"), "console.log(1)");
});

afterEach(() => rm(dir, { recursive: true, force: true }));

describe("deploy from a directory", () => {
  it("walks nested files and deploys them under normalized paths", async () => {
    const prepared: string[] = [];
    const dw = new Dropway({
      apiKey: "dw_live_test",
      baseUrl: "https://api.test",
      maxRetries: 0,
      fetch: (async (url: string | URL, init: RequestInit = {}) => {
        const u = String(url);
        if (u.endsWith("/deployments/prepare")) {
          const body = JSON.parse(init.body as string) as {
            manifest: { path: string }[];
          };
          prepared.push(...body.manifest.map((f) => f.path));
          return json({ missing: [], uploads: {} });
        }
        if (u.endsWith("/deployments")) return json({ version_id: "v1" });
        if (u.endsWith("/publish")) return json({ live_url: "https://live.test" });
        return json({}, 404);
      }) as unknown as typeof fetch,
    });

    const res = await dw.sites.deploy("s1", { dir });

    expect(res.published).toBe(true);
    expect(prepared.sort()).toEqual(["assets/app.js", "index.html"]);
  });
});
