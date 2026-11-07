// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The review primitives stay dark unless a deploy explicitly opts in.
// Bridge peers are https hosts under the existing platform content suffix
// (the same suffix /authz already recognizes). Custom domains are not peers:
// the dashboard has no authoritative allowlist for them here, and this module
// does not ask the serving worker to inject anything.

import { CONTENT_SUFFIX, isPlatformContentHost } from "@/lib/authz-host";

export interface CollabReviewEnv {
  NEXT_PUBLIC_COLLAB_REVIEW?: string;
}

export function isCollabReviewEnabled(env?: CollabReviewEnv): boolean {
  const source = env ?? process.env;
  return source.NEXT_PUBLIC_COLLAB_REVIEW === "1";
}

export interface BridgePrerequisites {
  enabled: boolean;
  platformContentSuffixConfigured: boolean;
  customDomainsTrusted: false;
  servingWorkerInjectsBridge: false;
  reasons: readonly string[];
}

export function bridgePrerequisites(env?: CollabReviewEnv): BridgePrerequisites {
  const enabled = isCollabReviewEnabled(
    env ?? { NEXT_PUBLIC_COLLAB_REVIEW: process.env.NEXT_PUBLIC_COLLAB_REVIEW },
  );
  const reasons = [
    enabled
      ? "Collab review flag is on. Storage and room routes are still not part of this client."
      : "Collab review is default off (NEXT_PUBLIC_COLLAB_REVIEW is not 1).",
    "Custom domains are not trusted for the selection bridge.",
    "The serving worker does not inject the selection bridge.",
    `Platform content suffix ${CONTENT_SUFFIX} is the only configured peer pattern.`,
  ];
  return {
    enabled,
    platformContentSuffixConfigured: CONTENT_SUFFIX.startsWith("."),
    customDomainsTrusted: false,
    servingWorkerInjectsBridge: false,
    reasons,
  };
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
