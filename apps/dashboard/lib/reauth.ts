// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Pure helpers for the dashboard's "API said 401, send the viewer to sign-in"
// path. Kept free of Next and server-only imports so both RSC pages and the
// client auth form can share them.

import { safeNextPath } from "@/lib/authz-host";

/** Query flag: show the sign-in form even when a session cookie is still present. */
export const REAUTH_QUERY = "reauth";

/**
 * Sign-in URL after the Go API rejects the dashboard's credential (401).
 *
 * `reauth=1` is required. The sign-in page otherwise bounces any still-present
 * session cookie straight to `callbackURL`. That cookie can outlive the JWT the
 * API just rejected (session cookie cache, or a mint that swallowed an expired
 * session), and the bounce would loop back onto the page that 401'd.
 */
export function signInPathForReauth(returnPath: string): string {
  const params = new URLSearchParams({
    callbackURL: safeNextPath(returnPath),
    [REAUTH_QUERY]: "1",
  });
  return `/sign-in?${params.toString()}`;
}

/** True when this request must show the auth form instead of bouncing a session. */
export function isReauthRequest(value: string | string[] | undefined): boolean {
  return value === "1";
}

/**
 * Cross-link between the sign-in and sign-up screens. Preserves an explicit
 * callback and the reauth flag so "Create one" doesn't drop a forced re-login
 * back into the page that 401'd.
 */
export function authScreenSwitchHref(
  target: "sign-in" | "sign-up",
  callbackURL: string,
  reauth: boolean,
  defaultCallback = "/dashboard",
): string {
  const params = new URLSearchParams();
  if (callbackURL && callbackURL !== defaultCallback) {
    params.set("callbackURL", callbackURL);
  }
  if (reauth) params.set(REAUTH_QUERY, "1");
  const qs = params.toString();
  return qs ? `/${target}?${qs}` : `/${target}`;
}
