import "server-only";

import { cache } from "react";
import { headers } from "next/headers";

import { auth } from "@/lib/auth";

/**
 * The current Better Auth session for the request, memoized per server render.
 *
 * Several server components resolve the session independently in one request —
 * the (app) layout guards the route with it, and helpers like `loadActiveOrg`
 * need the viewer's user id. Each `auth.api.getSession()` is a cookie-verified
 * lookup, so without memoization a single org page read the session twice.
 * React `cache()` is request-scoped, so this collapses those to one read while
 * never leaking a session between concurrent requests.
 *
 * Returns null (rather than throwing) when there's no valid session, so callers
 * can branch on it directly.
 *
 * `method: "GET"` is REQUIRED for this to work inside a Server Action. Next.js
 * runs actions as POST requests, and with the session cookie cache enabled
 * (lib/auth.ts) Better Auth's get-session endpoint THROWS
 * METHOD_NOT_ALLOWED_DEFER_SESSION_REQUIRED on a POST (it will not refresh the
 * cookie mid-POST unless `session.deferSessionRefresh` is set globally). Without
 * the override the ambient POST propagates, the call throws, the `.catch` below
 * swallows it to null, and every action reads a null session (the contact form
 * mailed "From: unknown"). Forcing GET is a read-only session lookup that behaves
 * identically in RSC (already GET) and in actions, so it fixes actions without
 * changing the global refresh policy.
 */
export const getCurrentSession = cache(async () => {
  return auth.api
    .getSession({ headers: await headers(), method: "GET" })
    .catch(() => null);
});
