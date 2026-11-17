import { afterEach, describe, expect, it, vi } from "vitest";
import { createPreviewCache, type SnapshotLoader, type SnapshotResult } from "@/lib/project-room/preview-cache";

const result = (siteId = "site", versionId = "v1", page = "/"): SnapshotResult => ({
  ok: true,
  snapshot: { snapshotId: `${siteId}-${versionId}`, siteId, versionId, page, html: "<main>Preview</main>", contentHash: "hash", omittedAssets: 0, renderer: "static-v1" },
});
afterEach(() => vi.useRealTimers());
describe("workspace preview latency", () => {
  it("deduplicates a highlighted revision and the subsequent selection, then reuses the result", async () => {
    let finish!: (result: SnapshotResult) => void;
    const loader = vi.fn<SnapshotLoader>(() => new Promise((resolve) => { finish = resolve; }));
    const cache = createPreviewCache(loader);
    const hover = cache.load("room", "site", "v1", "/");
    const select = cache.load("room", "site", "v1", "/");
    expect(hover).toBe(select);
    finish(result());
    await select;
    expect(cache.peek("room", "site", "v1", "/")).toMatchObject({ versionId: "v1" });
    await cache.load("room", "site", "v1", "/");
    expect(loader).toHaveBeenCalledTimes(1);
  });
  it("isolates room/site/version/page identities and refreshes after a minute", async () => {
    vi.useFakeTimers();
    const loader = vi.fn<SnapshotLoader>(async (_, site, version, page) => result(site, version, page));
    const cache = createPreviewCache(loader);
    await cache.load("room", "site", "v1", "/");
    const variants: Parameters<SnapshotLoader>[] = [["other", "site", "v1", "/"], ["room", "other", "v1", "/"], ["room", "site", "v2", "/"], ["room", "site", "v1", "/about"]];
    for (const args of variants) {
      expect(cache.peek(...args)).toBeNull();
      await cache.load(...args);
    }
    vi.advanceTimersByTime(60_001);
    expect(cache.peek("room", "site", "v1", "/")).toBeNull();
    await cache.load("room", "site", "v1", "/");
    expect(loader).toHaveBeenCalledTimes(6);
  });
  it("does not cache failures or mismatched identities", async () => {
    const loader = vi.fn<SnapshotLoader>()
      .mockResolvedValueOnce({ ok: false, message: "Unavailable" })
      .mockResolvedValueOnce(result("site", "wrong"))
      .mockRejectedValueOnce(new Error("Offline"))
      .mockResolvedValue(result());
    const cache = createPreviewCache(loader);
    expect(await cache.load("room", "site", "v1", "/")).toMatchObject({ ok: false });
    expect(await cache.load("room", "site", "v1", "/")).toMatchObject({ ok: false });
    await expect(cache.load("room", "site", "v1", "/")).rejects.toThrow("Offline");
    expect(cache.peek("room", "site", "v1", "/")).toBeNull();
    expect(await cache.load("room", "site", "v1", "/")).toMatchObject({ ok: true });
  });
  it("bounds retained previews and does not share them with another workspace", async () => {
    const loader: SnapshotLoader = async (_, site, version, page) => result(site, version, page);
    const cache = createPreviewCache(loader);
    for (let i = 0; i < 7; i++) await cache.load("room", "site", `v${i}`, "/");
    expect(cache.peek("room", "site", "v0", "/")).toBeNull();
    expect(cache.peek("room", "site", "v6", "/")).not.toBeNull();
    expect(createPreviewCache(loader).peek("room", "site", "v6", "/")).toBeNull();
  });
});
