// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Authenticated server pages treat two API statuses as navigation, not faults.
// 404 is notFound(). 401 is the Go API's re-auth signal: a missing bearer, a
// rejected JWT, or a verified token with no org/user (handlers.tenant and
// writeStoreError). Throwing that 401 makes Next invoke onRequestError, which
// reports it to PostHog — including when the (app) layout is already redirecting
// a signed-out viewer, because the page render runs in parallel and still calls
// the API. mintBearerToken turns an expired session into this same 401 on purpose
// so the page can send the user to sign-in instead of error tracking.

import "server-only";

import { notFound, redirect } from "next/navigation";

import { ApiError } from "@/lib/api";
import { signInPathForReauth } from "@/lib/reauth";

/**
 * Resolve a failed authenticated read. Does not return: 404 and 401 become Next
 * control-flow exceptions (not passed to onRequestError); every other error is
 * rethrown so a real fault still reaches error tracking.
 */
export function settleApiRead(err: unknown, returnPath: string): never {
  if (err instanceof ApiError) {
    if (err.status === 404) notFound();
    if (err.status === 401) redirect(signInPathForReauth(returnPath));
  }
  throw err;
}
