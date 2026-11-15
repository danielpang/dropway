import { beforeEach, describe, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({ create: vi.fn(), list: vi.fn(), me: vi.fn(), enabled: vi.fn() }));
vi.mock("@/lib/api", () => ({ api: { createSite: mocks.create, listSites: mocks.list, me: mocks.me }, ApiError: class extends Error { asQuotaExceeded() { return null; } } }));
vi.mock("@/lib/project-room/capabilities", () => ({ collaborationEnabled: mocks.enabled }));
import { createProjectImageAction } from "@/app/(app)/rooms/image-actions";
const operation = "12345678-1234-4123-8123-123456789012";
const saved = { id: "image-id", slug: `image-${operation}`, resource_kind: "image", access_mode: "org_only", owner_id: "me", org_id: "org", title: "Design.png" };
beforeEach(() => {
  vi.resetAllMocks();
  mocks.enabled.mockResolvedValue(true);
  mocks.create.mockRejectedValue(new Error("response lost"));
  mocks.me.mockResolvedValue({ user_id: "me", org_id: "org" });
  mocks.list.mockResolvedValue([saved]);
});
describe("idempotent image resource creation", () => {
  it("recovers a committed creation after a lost response using the same operation ID", async () => {
    await expect(createProjectImageAction("Design.png", operation)).resolves.toEqual({ ok: true, siteId: "image-id" });
    expect(mocks.create).toHaveBeenCalledWith({ slug: saved.slug, resource_kind: "image", access_mode: "org_only", title: "Design.png" });
    await expect(createProjectImageAction("Design.png", operation)).resolves.toEqual({ ok: true, siteId: "image-id" });
    expect(mocks.create.mock.calls[0]).toEqual(mocks.create.mock.calls[1]);
  });
  it.each([{ owner_id: "someone-else" }, { org_id: "other-org" }, { resource_kind: "site" }, { access_mode: "public" }])("does not recover an unrelated resource %j", async (override) => {
    mocks.list.mockResolvedValue([{ ...saved, ...override }]);
    expect((await createProjectImageAction("Design.png", operation)).ok).toBe(false);
  });
  it("validates operation IDs and gates creation before any storage work", async () => {
    expect((await createProjectImageAction("Design.png", "bad-id")).ok).toBe(false);
    mocks.enabled.mockResolvedValue(false);
    expect((await createProjectImageAction("Design.png", operation)).ok).toBe(false);
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.list).not.toHaveBeenCalled();
  });
});
