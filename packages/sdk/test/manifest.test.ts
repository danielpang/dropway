// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
  buildManifest,
  contentTypeForPath,
  digest,
  normalizePath,
  sha256Hex,
} from "../src/manifest.js";

const vector = JSON.parse(
  readFileSync(
    fileURLToPath(new URL("../testdata/manifest-digest.json", import.meta.url)),
    "utf8",
  ),
) as { files: { path: string; sha256: string }[]; digest: string };

describe("digest parity with the Go server", () => {
  it("reproduces internal/manifest.Digest for the shared vector", () => {
    expect(digest(vector.files)).toBe(vector.digest);
  });

  it("is order-independent (sorted by path)", () => {
    const reversed = [...vector.files].reverse();
    expect(digest(reversed)).toBe(vector.digest);
  });
});

describe("sha256Hex", () => {
  it("hashes empty input to the known SHA-256 of empty", () => {
    expect(sha256Hex(new Uint8Array())).toBe(
      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    );
  });
});

describe("contentTypeForPath", () => {
  it("maps known extensions and defaults otherwise", () => {
    expect(contentTypeForPath("index.html")).toBe("text/html; charset=utf-8");
    expect(contentTypeForPath("a/b/app.js")).toBe("text/javascript; charset=utf-8");
    expect(contentTypeForPath("logo.PNG")).toBe("image/png");
    expect(contentTypeForPath("data.bin")).toBe("application/octet-stream");
    expect(contentTypeForPath("noext")).toBe("application/octet-stream");
  });
});

describe("normalizePath", () => {
  it("strips leading ./ and /, and rejects traversal", () => {
    expect(normalizePath("./index.html")).toBe("index.html");
    expect(normalizePath("/a/b.css")).toBe("a/b.css");
    expect(normalizePath("a\\b.js")).toBe("a/b.js");
    expect(() => normalizePath("../secret")).toThrow();
    expect(() => normalizePath("")).toThrow();
  });
});

describe("buildManifest", () => {
  it("produces a manifest, byte map, and digest", () => {
    const { manifest, bytesBySha, digest: d } = buildManifest({
      "index.html": "<h1>hi</h1>",
      "app.js": new Uint8Array([1, 2, 3]),
    });
    expect(manifest).toHaveLength(2);
    expect(bytesBySha.size).toBe(2);
    expect(d).toMatch(/^[0-9a-f]{64}$/);
    const html = manifest.find((f) => f.path === "index.html")!;
    expect(html.contentType).toBe("text/html; charset=utf-8");
    expect(html.sha256).toBe(sha256Hex(new TextEncoder().encode("<h1>hi</h1>")));
  });
});
