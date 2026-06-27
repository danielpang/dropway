// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the autoindex (directory listing) pure logic: which children a
// directory prefix collapses to, and how the HTML page is rendered (escaping,
// absolute links, parent link, size formatting). The serve-path integration
// (when a listing is produced vs a 404) is covered in serve.test.ts.

import { describe, expect, it } from "vitest";

import {
  directoryPrefix,
  listDirectory,
  renderDirectoryListing,
} from "../src/listing";
import type { Manifest } from "../src/manifest";

/** Build a manifest from a path → size map (sha/content_type are irrelevant here). */
function manifestOf(files: Record<string, number | undefined>): Manifest {
  const out: Manifest = { schema_version: 1, files: {} };
  for (const [path, size] of Object.entries(files)) {
    out.files[path] = {
      sha256: "a".repeat(64),
      content_type: "text/plain",
      ...(size === undefined ? {} : { size }),
    };
  }
  return out;
}

describe("directoryPrefix", () => {
  it("maps root and trailing-slash paths to themselves", () => {
    expect(directoryPrefix("")).toBe("");
    expect(directoryPrefix("docs/")).toBe("docs/");
    expect(directoryPrefix("a/b/")).toBe("a/b/");
  });

  it("treats an extension-less pretty path as a candidate directory", () => {
    expect(directoryPrefix("docs")).toBe("docs/");
    expect(directoryPrefix("a/b")).toBe("a/b/");
  });
});

describe("listDirectory", () => {
  it("returns null for a directory with no descendants (a genuine typo)", () => {
    const m = manifestOf({ "notes.md": 10 });
    expect(listDirectory(m, "missing/")).toBeNull();
  });

  it("lists immediate files at the root", () => {
    const m = manifestOf({ "notes.md": 10, "readme.txt": 20 });
    const entries = listDirectory(m, "");
    expect(entries).toEqual([
      { name: "notes.md", href: "/notes.md", isDir: false, size: 10 },
      { name: "readme.txt", href: "/readme.txt", isDir: false, size: 20 },
    ]);
  });

  it("collapses nested keys to a single subdirectory row, deduped", () => {
    const m = manifestOf({
      "report.md": 5,
      "assets/a.png": 1,
      "assets/b.png": 2,
      "assets/sub/c.png": 3,
    });
    const entries = listDirectory(m, "");
    // Directory first (deduped to "assets/"), then the file.
    expect(entries).toEqual([
      { name: "assets/", href: "/assets/", isDir: true },
      { name: "report.md", href: "/report.md", isDir: false, size: 5 },
    ]);
  });

  it("lists the children of a subdirectory prefix", () => {
    const m = manifestOf({
      "index.html": 100,
      "docs/a.md": 1,
      "docs/b.md": 2,
      "docs/img/x.png": 3,
    });
    const entries = listDirectory(m, "docs/");
    expect(entries).toEqual([
      { name: "img/", href: "/docs/img/", isDir: true },
      { name: "a.md", href: "/docs/a.md", isDir: false, size: 1 },
      { name: "b.md", href: "/docs/b.md", isDir: false, size: 2 },
    ]);
  });

  it("URL-encodes hrefs but keeps display names raw", () => {
    const m = manifestOf({ "my report.md": 4 });
    const entries = listDirectory(m, "");
    expect(entries![0]).toEqual({
      name: "my report.md",
      href: "/my%20report.md",
      isDir: false,
      size: 4,
    });
  });
});

describe("renderDirectoryListing", () => {
  it("renders an HTML page with a heading, links, and an item count", () => {
    const m = manifestOf({ "notes.md": 1500, "readme.txt": 20 });
    const html = renderDirectoryListing("", listDirectory(m, "")!);
    expect(html).toContain("<title>Index of /</title>");
    expect(html).toContain("<h1>Index of /</h1>");
    expect(html).toContain('href="/notes.md"');
    expect(html).toContain('href="/readme.txt"');
    expect(html).toContain("1.5 KB"); // size formatting
    expect(html).toContain("2 items");
    // No parent link at the root.
    expect(html).not.toContain("Parent directory");
  });

  it("includes a parent-directory link below the root", () => {
    const m = manifestOf({ "docs/sub/a.md": 1 });
    const html = renderDirectoryListing("docs/sub/", listDirectory(m, "docs/sub/")!);
    expect(html).toContain("<h1>Index of /docs/sub/</h1>");
    expect(html).toContain('Parent directory</a>');
    expect(html).toContain('href="/docs/"'); // parent of /docs/sub/
  });

  it("HTML-escapes tenant-supplied file names (no markup injection)", () => {
    // A slash-free hostile name (a slash would be read as a subdirectory).
    const m = manifestOf({ "<img src=x onerror=alert(1)>.txt": 1 });
    const html = renderDirectoryListing("", listDirectory(m, "")!);
    expect(html).not.toContain("<img src=x onerror=alert(1)>");
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;.txt");
  });

  it("uses the singular 'item' for a single child", () => {
    const m = manifestOf({ "only.md": 1 });
    const html = renderDirectoryListing("", listDirectory(m, "")!);
    expect(html).toContain("1 item</footer>");
  });
});
