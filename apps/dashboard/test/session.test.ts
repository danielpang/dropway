// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Regression test for lib/session.ts. A Next.js Server Action runs as a POST
// request, and with the session cookie cache enabled Better Auth's get-session
// endpoint throws METHOD_NOT_ALLOWED_DEFER_SESSION_REQUIRED on a POST. If the
// ambient POST propagates, the read throws, getCurrentSession's `.catch` maps it
// to null, and every server action sees a signed-in user as null (the contact
// form mailed "From: unknown"). The fix forces the read to GET; this test pins
// that so it can't silently regress.

import { afterEach, describe, expect, it, vi } from "vitest";

const { getSession } = vi.hoisted(() => ({ getSession: vi.fn() }));

vi.mock("server-only", () => ({}));
vi.mock("next/headers", () => ({ headers: async () => new Headers() }));
vi.mock("@/lib/auth", () => ({ auth: { api: { getSession } } }));

import { getCurrentSession } from "@/lib/session";

afterEach(() => {
  vi.clearAllMocks();
});

describe("getCurrentSession", () => {
  it("reads the session with method GET so it works inside a POST server action", async () => {
    getSession.mockResolvedValueOnce({ user: { id: "u1", email: "a@b.com" } });

    const session = await getCurrentSession();

    expect(session).toEqual({ user: { id: "u1", email: "a@b.com" } });
    expect(getSession).toHaveBeenCalledTimes(1);
    expect(getSession.mock.calls[0]![0]).toMatchObject({ method: "GET" });
  });

  it("returns null instead of throwing when the lookup fails", async () => {
    getSession.mockRejectedValueOnce(new Error("boom"));

    await expect(getCurrentSession()).resolves.toBeNull();
  });
});
