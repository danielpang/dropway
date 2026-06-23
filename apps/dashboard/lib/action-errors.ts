// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shared "ApiError -> user-facing message" mapping for server actions, so the feed,
// site, and settings actions don't each keep a drifting copy. Prefers the API's own
// body.message, then status-based defaults (overridable for surface-specific copy),
// then the caller's fallback; a non-ApiError (network) error maps to a generic
// "could not reach the API" string.

import { ApiError } from "@/lib/api";

export function apiErrorMessage(
  err: unknown,
  fallback: string,
  overrides?: { 400?: string; 403?: string; 404?: string },
): string {
  if (err instanceof ApiError) {
    const apiMsg = (err.body as { message?: string } | null)?.message;
    if (apiMsg) return apiMsg;
    if (err.status === 400 && overrides?.[400]) return overrides[400];
    if (err.status === 403) return overrides?.[403] ?? "You don't have permission to do that.";
    if (err.status === 404) return overrides?.[404] ?? "This site no longer exists.";
    return fallback;
  }
  return "Could not reach the API. Try again.";
}
