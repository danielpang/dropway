// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The review primitives stay dark without an authenticated capability.
// Bridge peers are https hosts under the existing platform content suffix
// (the same suffix /authz already recognizes). Custom domains are not peers:
// the dashboard has no authoritative allowlist for them here, and this module
// does not ask the serving worker to inject anything.

import { CONTENT_SUFFIX, isPlatformContentHost } from "@/lib/authz-host";

export interface CollabReviewCapability { enabled?: boolean; }

export function isCollabReviewEnabled(capability?: CollabReviewCapability): boolean {
  return capability?.enabled === true;
}

export interface BridgePrerequisites {
  enabled: boolean;
  platformContentSuffixConfigured: boolean;
  customDomainsTrusted: false;
  servingWorkerInjectsBridge: false;
  /** Opaque frames are not selection peers. Their origin cannot be named. */
  opaqueFrameSelections: false;
  reasons: readonly string[];
}

export function bridgePrerequisites(capability?: CollabReviewCapability): BridgePrerequisites {
  const enabled = isCollabReviewEnabled(capability);
  const reasons = [
    enabled
      ? "Project collaboration capability is enabled."
      : "Project collaboration is default off without an authenticated capability.",
    "Custom domains are not trusted for the selection bridge.",
    "The serving worker does not inject the selection bridge.",
    "Opaque sandbox frames are not selection peers. A child navigation does not change iframe.src, and postMessage cannot name that origin without '*'.",
    `Platform content suffix ${CONTENT_SUFFIX} is the only configured content-host pattern.`,
  ];
  return {
    enabled,
    platformContentSuffixConfigured: CONTENT_SUFFIX.startsWith("."),
    customDomainsTrusted: false,
    servingWorkerInjectsBridge: false,
    opaqueFrameSelections: false,
    reasons,
  };
}

export interface DashboardOriginEnv {
  BETTER_AUTH_URL?: string;
  NEXT_PUBLIC_BETTER_AUTH_URL?: string;
}

/**
 * The one parent origin an instrumented document may talk to.
 * Unset stays closed. There is no localhost default and no caller-supplied override.
 * A content host is never the dashboard, even if it is written into the env var.
 */
export function trustedDashboardOrigin(env?: DashboardOriginEnv): string | null {
  const raw = (env?.BETTER_AUTH_URL ?? env?.NEXT_PUBLIC_BETTER_AUTH_URL ?? "").trim().replace(/\/$/, "");
  if (!raw || raw === "*" || raw === "null") return null;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.origin !== raw) return null;
  if (url.username || url.password || url.search || url.hash) return null;
  if (url.pathname !== "/" && url.pathname !== "") return null;
  if (isPlatformContentHost(url.hostname)) return null;
  if (url.protocol === "https:") return url.origin;
  if (url.protocol === "http:" && (url.hostname === "localhost" || url.hostname === "127.0.0.1")) {
    return url.origin;
  }
  return null;
}

export function canOpenCandidatePreview(
  previewOrigin: string,
  parentOrigin: string,
): { ok: true } | { ok: false; reason: "origin" } {
  if (!previewOrigin || previewOrigin === parentOrigin) return { ok: false, reason: "origin" };
  let url: URL;
  try {
    url = new URL(previewOrigin);
  } catch {
    return { ok: false, reason: "origin" };
  }
  if (url.origin !== previewOrigin) return { ok: false, reason: "origin" };
  if (url.protocol !== "https:") return { ok: false, reason: "origin" };
  if (!isPlatformContentHost(url.hostname)) return { ok: false, reason: "origin" };
  return { ok: true };
}
