// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the site-download helpers (lib/site-download.ts): completing a
// truncated bulk payload by fetching omitted paths, and decoding inline files
// into zip entries while refusing traversal.

import { describe, expect, it, vi } from "vitest";

import {
  completeTruncatedSiteDownload,
  decodeSiteFiles,
  type SiteFilePayload,
} from "@/lib/site-download";
import { crc32 } from "@/lib/zip";

const utf8 = (path: string, content: string): SiteFilePayload => ({
  path,
  content,
  encoding: "utf8",
  size: content.length,
});

describe("completeTruncatedSiteDownload", () => {
  it("returns the bulk files unchanged when not truncated", async () => {
    const readFile = vi.fn();
    const download = { files: [utf8("index.html", "<h1>")], truncated: false };
    const out = await completeTruncatedSiteDownload(download, [{ path: "index.html" }], readFile);
    expect(out).toEqual({ files: download.files, skipped: [] });
    expect(readFile).not.toHaveBeenCalled();
  });

  it("fetches listed paths that the bulk payload omitted", async () => {
    const readFile = vi.fn(async (path: string) => utf8(path, `body of ${path}`));
    const download = {
      truncated: true,
      files: [utf8("index.html", "<h1>")],
    };
    const out = await completeTruncatedSiteDownload(
      download,
      [{ path: "index.html" }, { path: "style.css" }, { path: "app.js" }],
      readFile,
    );
    expect(readFile).toHaveBeenCalledTimes(2);
    expect(readFile).toHaveBeenCalledWith("style.css");
    expect(readFile).toHaveBeenCalledWith("app.js");
    expect(out.skipped).toEqual([]);
    expect(out.files.map((f) => f.path)).toEqual(["index.html", "style.css", "app.js"]);
  });

  it("records paths whose individual fetch fails instead of aborting", async () => {
    const readFile = vi.fn(async (path: string) => {
      if (path === "huge.bin") throw new Error("too large");
      return utf8(path, "ok");
    });
    const out = await completeTruncatedSiteDownload(
      { truncated: true, files: [utf8("index.html", "x")] },
      [{ path: "index.html" }, { path: "huge.bin" }, { path: "ok.txt" }],
      readFile,
    );
    expect(out.skipped).toEqual(["huge.bin"]);
    expect(out.files.map((f) => f.path)).toEqual(["index.html", "ok.txt"]);
  });
});

describe("decodeSiteFiles", () => {
  it("decodes utf8 and base64 into zip entries under the prefix", () => {
    const hello = new TextEncoder().encode("hello");
    const { entries, skipped } = decodeSiteFiles(
      [
        utf8("index.html", "hello"),
        { path: "logo.png", encoding: "base64", content: btoa("PNG") },
      ],
      "mysite/",
    );
    expect(skipped).toEqual([]);
    expect(entries.map((e) => e.path)).toEqual(["mysite/index.html", "mysite/logo.png"]);
    expect(entries[0]?.data).toEqual(hello);
    expect(new TextDecoder().decode(entries[1]?.data)).toBe("PNG");
    // Spot-check that the bytes are zip-worthy (crc of known vector still holds).
    expect(crc32(hello)).toBe(crc32(entries[0]!.data));
  });

  it("skips traversal and absolute paths rather than writing them", () => {
    const { entries, skipped } = decodeSiteFiles(
      [
        utf8("index.html", "ok"),
        utf8("../secret", "no"),
        utf8("/etc/passwd", "no"),
        utf8("a/../b", "no"),
        utf8("api..reference.md", "dots-in-name-are-fine"),
      ],
      "s/",
    );
    expect(entries.map((e) => e.path)).toEqual(["s/index.html", "s/api..reference.md"]);
    expect(skipped).toEqual(["../secret", "/etc/passwd", "a/../b"]);
  });
});
