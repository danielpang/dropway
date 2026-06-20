"use server";

import { api, ApiError } from "@/lib/api";
import {
  callbackUrl,
  normalizeContentHost,
  safeNextPath,
} from "@/lib/authz-host";

/**
 * Result of the password-mode authz submit. The client password form branches
 * on this: success carries the content-host callback URL to send the browser to
 * (the Worker sets the __Host-edge cookie there); failures carry a message.
 */
export type PasswordAuthzResult =
  | { ok: true; redirectTo: string }
  | { ok: false; message: string };

/**
 * Verify a viewer-submitted password for a password-protected site and, on
 * success, return the content-host callback URL carrying the minted ANONYMOUS
 * edge token. JWT-FREE, the password is the only credential,
 * so this calls the public /v1/authz/password endpoint (no viewer identity
 * leaks into the anonymous grant).
 *
 * `host`/`next` are re-validated here (defense in depth, never trust the values
 * the hidden form fields carried back) so the redirect target can't be tampered.
 */
export async function submitPasswordAction(input: {
  host: string;
  next: string;
  password: string;
}): Promise<PasswordAuthzResult> {
  const host = normalizeContentHost(input.host);
  if (!host) {
    return { ok: false, message: "That link is invalid." };
  }
  const next = safeNextPath(input.next);

  const password = input.password;
  if (!password) {
    return { ok: false, message: "Enter the password to continue." };
  }

  try {
    const { token } = await api.authzPassword({ host, password });
    if (!token) {
      return { ok: false, message: "Could not unlock this site. Try again." };
    }
    return { ok: true, redirectTo: callbackUrl(host, token, next) };
  } catch (err) {
    if (err instanceof ApiError) {
      // A generic message for 401 (wrong password / unknown host, no oracle)
      // and 403 (expired link); never reveal which.
      if (err.status === 401) {
        return { ok: false, message: "Incorrect password." };
      }
      if (err.status === 403) {
        return { ok: false, message: "This link has expired." };
      }
      return { ok: false, message: "Could not unlock this site. Try again." };
    }
    return { ok: false, message: "Could not reach the server. Try again." };
  }
}
