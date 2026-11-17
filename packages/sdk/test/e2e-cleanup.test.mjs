import { describe, expect, it, vi } from "vitest";
import {
  cleanupRunSites,
  deleteAndConfirm,
  withTestSite,
} from "../e2e/cleanup-sites.mjs";

const missing = Object.assign(new Error("not found"), { status: 404 });
function client() {
  return {
    sites: {
      create: vi.fn().mockResolvedValue({ id: "test-site" }),
      delete: vi.fn().mockResolvedValue(undefined),
      get: vi.fn().mockRejectedValue(missing),
      list: vi.fn().mockResolvedValue([]),
    },
  };
}
describe("live E2E cleanup", () => {
  it.each(["publish returned 500", "content assertion failed"])(
    "deletes and verifies the site when %s",
    async (message) => {
      const dw = client();
      const failure = new Error(message);
      await expect(
        withTestSite(dw, { slug: "sdk-e2e-1-1" }, async () => {
          throw failure;
        }),
      ).rejects.toBe(failure);
      expect(dw.sites.delete).toHaveBeenCalledWith("test-site");
      expect(dw.sites.get).toHaveBeenCalledWith("test-site");
    },
  );
  it("does not delete anything if creation fails", async () => {
    const dw = client();
    dw.sites.create.mockRejectedValue(new Error("slug conflict"));
    await expect(withTestSite(dw, {}, vi.fn())).rejects.toThrow(
      "slug conflict",
    );
    expect(dw.sites.delete).not.toHaveBeenCalled();
  });
  it("accepts an already removed site after a lost delete response", async () => {
    const dw = client();
    dw.sites.delete.mockRejectedValue(new Error("network interrupted"));
    await expect(deleteAndConfirm(dw, "test-site")).resolves.toBeUndefined();
    expect(dw.sites.delete).toHaveBeenCalledTimes(1);
  });
  it("retries failures and reports unsuccessful cleanup", async () => {
    const dw = client();
    dw.sites.delete.mockRejectedValue(new Error("unavailable"));
    dw.sites.get.mockResolvedValue({ id: "test-site" });
    await expect(
      deleteAndConfirm(dw, "test-site", { delay: async () => {} }),
    ).rejects.toThrow("unavailable");
    expect(dw.sites.delete).toHaveBeenCalledTimes(3);
  });
  it("cleans only this attempt's exact SDK and CLI names", async () => {
    const dw = client();
    dw.sites.list.mockResolvedValue([
      { id: "sdk", slug: "sdk-e2e-123-2" },
      { id: "cli", slug: "cli-e2e-123-2" },
      { id: "previous", slug: "sdk-e2e-123-1" },
      { id: "other", slug: "sdk-e2e-123-2-custom" },
    ]);
    await cleanupRunSites(dw, ["sdk-e2e-123-2", "cli-e2e-123-2"]);
    expect(dw.sites.delete.mock.calls).toEqual([["sdk"], ["cli"]]);
  });
  it("rejects missing or unscoped cleanup names before reading sites", async () => {
    const dw = client();
    await expect(
      cleanupRunSites(dw, [undefined, "production-site"]),
    ).rejects.toThrow("run attempt");
    expect(dw.sites.list).not.toHaveBeenCalled();
  });
});
