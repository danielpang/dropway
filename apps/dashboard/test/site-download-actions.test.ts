// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for downloadSiteAction. The contracts under test: empty id
// short-circuits before the API, a complete bulk payload is returned as-is,
// a truncated payload is filled from list+read, and unpublished sites map to
// the 400 copy.

import { afterEach, describe, expect, it, vi } from "vitest";

const { downloadSite, listSiteFiles, readSiteFile, MockApiError } = vi.hoisted(() => {
  class MockApiError extends Error {
    status: number;
    body: unknown;
    constructor(status: number, message: string, body: unknown) {
      super(message);
      this.status = status;
      this.body = body;
    }
  }
  return {
    downloadSite: vi.fn(),
    listSiteFiles: vi.fn(),
    readSiteFile: vi.fn(),
    MockApiError,
  };
});

vi.mock("next/cache", () => ({ revalidatePath: vi.fn() }));

vi.mock("@/lib/api", () => ({
  api: { downloadSite, listSiteFiles, readSiteFile },
  ApiError: MockApiError,
}));

import { downloadSiteAction } from "@/app/(app)/sites/[id]/actions";

afterEach(() => {
  vi.clearAllMocks();
});

describe("downloadSiteAction", () => {
  it("rejects an empty site id without calling the API", async () => {
    const res = await downloadSiteAction("   ");
    expect(res).toEqual({ ok: false, message: "Missing site id." });
    expect(downloadSite).not.toHaveBeenCalled();
  });

  it("returns a complete bulk download as-is", async () => {
    const download = {
      slug: "docs",
      site_id: "s1",
      files: [{ path: "index.html", content: "<h1>", encoding: "utf8" }],
    };
    downloadSite.mockResolvedValueOnce(download);
    const res = await downloadSiteAction("s1");
    expect(res).toEqual({ ok: true, download });
    expect(listSiteFiles).not.toHaveBeenCalled();
  });

  it("fills omitted files when the bulk payload is truncated", async () => {
    downloadSite.mockResolvedValueOnce({
      slug: "docs",
      truncated: true,
      files: [{ path: "index.html", content: "a", encoding: "utf8" }],
    });
    listSiteFiles.mockResolvedValueOnce([{ path: "index.html" }, { path: "style.css" }]);
    readSiteFile.mockResolvedValueOnce({
      path: "style.css",
      content: "body{}",
      encoding: "utf8",
    });

    const res = await downloadSiteAction("s1");
    expect(res.ok).toBe(true);
    if (!res.ok) return;
    expect(res.skipped).toBeUndefined();
    expect(res.download.truncated).toBe(false);
    expect(res.download.files?.map((f) => f.path)).toEqual(["index.html", "style.css"]);
    expect(readSiteFile).toHaveBeenCalledWith("s1", "style.css");
  });

  it("surfaces paths that still fail after the per-file fetch", async () => {
    downloadSite.mockResolvedValueOnce({
      truncated: true,
      files: [{ path: "index.html", content: "a", encoding: "utf8" }],
    });
    listSiteFiles.mockResolvedValueOnce([{ path: "index.html" }, { path: "huge.bin" }]);
    readSiteFile.mockRejectedValueOnce(new MockApiError(400, "", { message: "too large" }));

    const res = await downloadSiteAction("s1");
    expect(res.ok).toBe(true);
    if (!res.ok) return;
    expect(res.skipped).toEqual(["huge.bin"]);
    expect(res.download.truncated).toBe(true);
    expect(res.download.files?.map((f) => f.path)).toEqual(["index.html"]);
  });

  it("maps an unpublished-site 400 to the published-version copy", async () => {
    downloadSite.mockRejectedValueOnce(
      new MockApiError(400, "API 400", {
        message: `site "draft" has no published version yet`,
      }),
    );
    const res = await downloadSiteAction("s1");
    expect(res).toEqual({
      ok: false,
      message: `site "draft" has no published version yet`,
    });
  });

  it("uses the unpublished fallback when the 400 body has no message", async () => {
    downloadSite.mockRejectedValueOnce(new MockApiError(400, "API 400", {}));
    const res = await downloadSiteAction("s1");
    expect(res).toEqual({
      ok: false,
      message: "This site has no published version to download yet.",
    });
  });
});
