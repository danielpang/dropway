import { beforeEach, describe, expect, it, vi } from "vitest";
const capability = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", () => ({ api: { projectCollaborationCapabilities: capability } }));
import { collaborationCapabilities, collaborationEnabled } from "@/lib/project-room/capabilities";

beforeEach(() => capability.mockReset());
describe("authenticated collaboration capability", () => {
  it("fails closed for unavailable, missing or non-boolean capability responses", async () => {
    capability.mockRejectedValueOnce(new Error("unavailable"));
    expect(await collaborationEnabled()).toBe(false);
    for (const value of [null, {}, { enabled: "true" }, { enabled: 1 }]) {
      capability.mockResolvedValue(value);
      expect(await collaborationEnabled()).toBe(false);
    }
  });
  it("uses one feature decision and treats execution configuration as readiness only", async () => {
    capability.mockResolvedValue({ enabled: true, execution_configured: false });
    expect(await collaborationEnabled()).toBe(true);
    expect(await collaborationEnabled(true)).toBe(false);
    capability.mockResolvedValue({ enabled: false, execution_configured: true });
    expect(await collaborationEnabled(true)).toBe(false);
    capability.mockResolvedValue({ enabled: true, execution_configured: true });
    expect(await collaborationCapabilities()).toEqual({ enabled: true, executionConfigured: true });
    expect(await collaborationEnabled(true)).toBe(true);
  });
  it("rechecks the authenticated capability rather than retaining an earlier allow", async () => {
    capability.mockResolvedValueOnce({ enabled: true }).mockResolvedValueOnce({ enabled: false });
    expect(await collaborationEnabled()).toBe(true);
    expect(await collaborationEnabled()).toBe(false);
    expect(capability).toHaveBeenCalledTimes(2);
  });
});
