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
 */
export const getCurrentSession = cache(async () => {
  return auth.api
    .getSession({ headers: await headers() })
    .catch(() => null);
});
