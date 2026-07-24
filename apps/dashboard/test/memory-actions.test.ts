// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the Company-memory server actions
// (app/(app)/settings/memory/actions.ts + setMemoryEnabledAction). The
// contracts under test: input validation short-circuits before the API is
// touched, the API's own error message always wins, and the memory-specific
// error states map to the right copy — the 422 {error:"quota"} cap, the 402
// plan_required upgrade message (surfaced verbatim), and the dual meaning of
// 403 (org flag off for member writes vs. role for admin writes).

import { afterEach, describe, expect, it, vi } from "vitest";

const {
  createMemory,
  patchMemory,
  deleteMemory,
  searchMemories,
  setMemoryEnabled,
  MockApiError,
} = vi.hoisted(() => {
  // Declared inside vi.hoisted so the hoisted vi.mock factory below can
  // reference it (a top-level class would not be initialized yet).
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
    createMemory: vi.fn(),
    patchMemory: vi.fn(),
    deleteMemory: vi.fn(),
    searchMemories: vi.fn(),
    setMemoryEnabled: vi.fn(),
    MockApiError,
  };
});

vi.mock("next/cache", () => ({ revalidatePath: vi.fn() }));

vi.mock("@/lib/api", () => ({
  api: { createMemory, patchMemory, deleteMemory, searchMemories, setMemoryEnabled },
  ApiError: MockApiError,
}));

import {
  createMemoryAction,
  deleteMemoryAction,
  patchMemoryAction,
  searchMemoriesAction,
} from "@/app/(app)/settings/memory/actions";
import { setMemoryEnabledAction } from "@/app/(app)/settings/actions";

afterEach(() => {
  vi.clearAllMocks();
});

const mem = { id: "m1", kind: "fact", content: "Navy palette" };

describe("createMemoryAction", () => {
  it("passes trimmed content through and reports created/deduped", async () => {
    createMemory.mockResolvedValueOnce({ memory: mem, created: false });
    const res = await createMemoryAction({ content: "  Navy palette  " });
    expect(createMemory).toHaveBeenCalledWith({ content: "Navy palette", kind: undefined });
    expect(res).toEqual({ ok: true, memory: mem, created: false });
  });

  it("rejects empty content without calling the API", async () => {
    const res = await createMemoryAction({ content: "   " });
    expect(res.ok).toBe(false);
    expect(createMemory).not.toHaveBeenCalled();
  });

  it("maps the 422 quota body to the memory-limit message", async () => {
    createMemory.mockRejectedValueOnce(new MockApiError(422, "", { error: "quota" }));
    const res = await createMemoryAction({ content: "x" });
    expect(res).toMatchObject({ ok: false });
    if (!res.ok) expect(res.message).toMatch(/memory limit/i);
  });

  it("surfaces the server's plan_required upgrade message verbatim (402)", async () => {
    createMemory.mockRejectedValueOnce(
      new MockApiError(402, "", {
        error: "plan_required",
        message:
          "org memory requires a Pro plan or above; upgrade your plan in billing to use memory",
      }),
    );
    const res = await createMemoryAction({ content: "x" });
    if (!res.ok) expect(res.message).toMatch(/Pro plan or above/);
    expect(res.ok).toBe(false);
  });

  it("maps a bare 403 to the org-flag-off copy (create is member-allowed)", async () => {
    createMemory.mockRejectedValueOnce(new MockApiError(403, "", null));
    const res = await createMemoryAction({ content: "x" });
    if (!res.ok) expect(res.message).toMatch(/turned off/i);
    expect(res.ok).toBe(false);
  });
});

describe("patchMemoryAction", () => {
  it("splits id from the patch payload", async () => {
    patchMemory.mockResolvedValueOnce(mem);
    const res = await patchMemoryAction({ id: "m1", pinned: true });
    expect(patchMemory).toHaveBeenCalledWith("m1", { pinned: true });
    expect(res).toEqual({ ok: true, memory: mem });
  });

  it("rejects a whitespace-only content rewrite locally", async () => {
    const res = await patchMemoryAction({ id: "m1", content: "  " });
    expect(res.ok).toBe(false);
    expect(patchMemory).not.toHaveBeenCalled();
  });

  it("maps a bare 403 to the admin-role copy (edits are admin-only)", async () => {
    patchMemory.mockRejectedValueOnce(new MockApiError(403, "", null));
    const res = await patchMemoryAction({ id: "m1", pinned: true });
    if (!res.ok) expect(res.message).toMatch(/owners and admins/i);
    expect(res.ok).toBe(false);
  });

  it("maps 404 to a friendly gone message", async () => {
    patchMemory.mockRejectedValueOnce(new MockApiError(404, "", null));
    const res = await patchMemoryAction({ id: "mX", disabled: true });
    if (!res.ok) expect(res.message).toMatch(/no longer exists/i);
    expect(res.ok).toBe(false);
  });
});

describe("deleteMemoryAction", () => {
  it("deletes and reports ok", async () => {
    deleteMemory.mockResolvedValueOnce(undefined);
    expect(await deleteMemoryAction({ id: "m1" })).toEqual({ ok: true });
    expect(deleteMemory).toHaveBeenCalledWith("m1");
  });
});

describe("searchMemoriesAction", () => {
  it("requires a query before touching the API", async () => {
    const res = await searchMemoriesAction({ query: "  " });
    expect(res.ok).toBe(false);
    expect(searchMemories).not.toHaveBeenCalled();
  });

  it("returns the API's memory list", async () => {
    searchMemories.mockResolvedValueOnce([mem]);
    const res = await searchMemoriesAction({ query: "branding", k: 5 });
    expect(searchMemories).toHaveBeenCalledWith("branding", 5);
    expect(res).toEqual({ ok: true, memories: [mem] });
  });
});

describe("setMemoryEnabledAction", () => {
  it("returns the server's new state", async () => {
    setMemoryEnabled.mockResolvedValueOnce({ memory_enabled: true });
    const res = await setMemoryEnabledAction({ enabled: true });
    expect(setMemoryEnabled).toHaveBeenCalledWith(true);
    expect(res).toEqual({ ok: true, memoryEnabled: true });
  });

  it("prefers the API's message (e.g. the 402 upgrade copy) over the fallback", async () => {
    setMemoryEnabled.mockRejectedValueOnce(
      new MockApiError(402, "", {
        message:
          "org memory requires a Pro plan or above; upgrade your plan in billing to use memory",
      }),
    );
    const res = await setMemoryEnabledAction({ enabled: true });
    if (!res.ok) expect(res.message).toMatch(/Pro plan or above/);
    expect(res.ok).toBe(false);
  });

  it("maps a bare 403 to the admin-role copy", async () => {
    setMemoryEnabled.mockRejectedValueOnce(new MockApiError(403, "", null));
    const res = await setMemoryEnabledAction({ enabled: false });
    if (!res.ok) expect(res.message).toMatch(/owners and admins/i);
    expect(res.ok).toBe(false);
  });
});
