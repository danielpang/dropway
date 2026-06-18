import { describe, expect, it } from "vitest";

import {
  buildManifest,
  computeDigest,
  contentTypeFor,
  type DroppedFile,
} from "@/lib/deploy-manifest";

/**
 * Canonical whole-deploy digest from Go's internal/manifest.Digest for the exact
 * fixture below (computed by running manifest.Digest over the same path+content
 * set). The browser deploy's finalize sends this digest and the server recomputes
 * + rejects a mismatch with 400 — so if this assertion ever fails, drag-and-drop
 * deploy is silently broken. Keep the two byte-identical.
 */
const GO_DIGEST =
  "c3771f748ca72eb468b8a56e0487788d559f69af61c4be90f3f9f504d3ae0129";

function dropped(path: string, content: string): DroppedFile {
  const name = path.split("/").pop() ?? path;
  return { path, file: new File([content], name) };
}

describe("deploy manifest (parity with Go internal/manifest.Digest)", () => {
  it("matches Go's canonical digest, sorting before hashing", async () => {
    // Intentionally UNSORTED + a dotfile, to exercise the sort + dotfile inclusion.
    const files = [
      dropped("index.html", "<h1>hi</h1>"),
      dropped(".well-known/x", "y"),
      dropped("assets/app.js", "console.log(1)"),
    ];
    const { manifest, digest } = await buildManifest(files);

    expect(digest).toBe(GO_DIGEST);
    expect(manifest.map((m) => m.path)).toEqual([
      ".well-known/x",
      "assets/app.js",
      "index.html",
    ]);
  });

  it("is order-independent (digest is over sorted paths)", async () => {
    const a = await computeDigest([
      { path: "b.txt", sha256: "11" },
      { path: "a.txt", sha256: "22" },
    ]);
    const b = await computeDigest([
      { path: "a.txt", sha256: "22" },
      { path: "b.txt", sha256: "11" },
    ]);
    expect(a).toBe(b);
  });

  it("records exact byte sizes and dedups identical content by hash", async () => {
    const { manifest, byHash } = await buildManifest([
      dropped("a.html", "same"),
      dropped("b.html", "same"),
    ]);
    expect(manifest[0]?.size).toBe(4); // "same" = 4 bytes
    expect(byHash.size).toBe(1); // identical content → a single blob
  });

  it("guesses sensible content types by extension", () => {
    expect(contentTypeFor("index.html")).toBe("text/html; charset=utf-8");
    expect(contentTypeFor("a/b.css")).toBe("text/css; charset=utf-8");
    expect(contentTypeFor("app.js")).toBe("text/javascript; charset=utf-8");
    expect(contentTypeFor("x.unknownext", "image/png")).toBe("image/png");
    expect(contentTypeFor("noext")).toBe("application/octet-stream");
  });
});
