// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Tests for apiFetch's auth failure handling (lib/api.ts):
//
//  1. NO unauthenticated fallback — when no token can be minted, apiFetch fails
//     locally with ApiError(401, {error:"reauth_required"}) and never sends the
//     request (previously it sent it anyway, paying a round trip for a
//     guaranteed 401 that polluted error tracking as "APIError: Unauthorized").
//  2. Bounded 401 recovery — when the Go API rejects a token we sent, the
//     cross-request cache entry is dropped and ONE fresh mint retries the
//     request; a fresh mint identical to the rejected token (or no mint) does
//     not retry.
//
// `@/lib/auth` is normally aliased to a null stub; vi.mock here overrides it
// with a controllable getToken. React's cache() is a passthrough outside a
// request store, so the module-level TokenCache is the only cross-call state —
// tests use distinct session ids to stay isolated from each other.

import { afterEach, describe, expect, it, vi } from "vitest";

const { getToken, getCurrentSession } = vi.hoisted(() => ({
  getToken: vi.fn(),
  getCurrentSession: vi.fn(),
}));

vi.mock("next/headers", () => ({ headers: async () => new Headers() }));
vi.mock("@/lib/auth", () => ({ auth: { api: { getToken } } }));
vi.mock("@/lib/session", () => ({ getCurrentSession }));

import { api, ApiError } from "@/lib/api";

const fetchMock = vi.fn();
vi.stubGlobal("fetch", fetchMock);

function session(id: string, orgId = "org-1") {
  return { session: { id, activeOrganizationId: orgId } };
}

function jsonResponse(status: number, body: unknown = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("apiFetch auth handling", () => {
  it("fails locally with reauth_required when no token can be minted — never sends unauthenticated", async () => {
    getCurrentSession.mockResolvedValue(null);
    getToken.mockResolvedValue(null);

    const err = await api.me().catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    expect((err as ApiError).body).toMatchObject({ error: "reauth_required" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("on a 401, drops the cached token, re-mints, and retries exactly once", async () => {
    getCurrentSession.mockResolvedValue(session("sess-retry"));
    getToken
      .mockResolvedValueOnce({ token: "stale-token" })
      .mockResolvedValueOnce({ token: "fresh-token" });
    fetchMock
      .mockResolvedValueOnce(jsonResponse(401, { error: "unauthorized" }))
      .mockResolvedValueOnce(jsonResponse(200, { user_id: "u1", org_id: "o1" }));

    const me = await api.me();

    expect(me).toMatchObject({ user_id: "u1" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const firstAuth = (fetchMock.mock.calls[0]![1] as RequestInit).headers as Record<string, string>;
    const secondAuth = (fetchMock.mock.calls[1]![1] as RequestInit).headers as Record<string, string>;
    expect(firstAuth.Authorization).toBe("Bearer stale-token");
    expect(secondAuth.Authorization).toBe("Bearer fresh-token");
  });

  it("does not retry when the fresh mint returns the same rejected token", async () => {
    getCurrentSession.mockResolvedValue(session("sess-same"));
    getToken.mockResolvedValue({ token: "same-token" });
    fetchMock.mockResolvedValue(jsonResponse(401, { error: "unauthorized" }));

    const err = await api.me().catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    // One request, no blind second attempt with an identical credential.
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not retry when the session is dead (fresh mint yields nothing)", async () => {
    getCurrentSession.mockResolvedValue(session("sess-dead"));
    getToken
      .mockResolvedValueOnce({ token: "dying-token" })
      .mockResolvedValueOnce(null);
    fetchMock.mockResolvedValue(jsonResponse(401, { error: "unauthorized" }));

    const err = await api.me().catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("a non-401 error does not trigger a re-mint", async () => {
    getCurrentSession.mockResolvedValue(session("sess-500"));
    getToken.mockResolvedValueOnce({ token: "ok-token" });
    fetchMock.mockResolvedValue(jsonResponse(500, { error: "internal_error" }));

    const err = await api.me().catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(500);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(getToken).toHaveBeenCalledTimes(1);
  });
});
