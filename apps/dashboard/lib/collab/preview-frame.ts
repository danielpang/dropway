// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A candidate review frame is sandboxed off the dashboard origin. Scripts may
// run so an opted-in document can post a selection, but the frame is not
// same-origin and therefore does not receive the app's cookies. This is not
// the third-party embed snippet (lib/embed.ts), which is unsandboxed on purpose
// so a public site can render inside Notion or Linear.

import { canOpenCandidatePreview } from "@/lib/collab/gate";

/** No allow-same-origin: the framed document gets an opaque origin. */
export function candidatePreviewSandbox(): string {
  return "allow-scripts";
}

/**
 * A handle the arming step can configure before any navigation.
 * `credentialless` must become true as a result of setAttribute. If the
 * browser does not reflect it, src stays unset.
 */
export interface PreviewFrameHandle {
  credentialless: boolean;
  src: string;
  setAttribute(name: string, value: string): void;
}

/**
 * Set sandbox, referrer policy, and credentialless, and only then assign src.
 * Callers append the element after this returns. Assigning src first lets the
 * browser send cookies on the first request; setting the attribute afterwards
 * does not rewind that request.
 */
export function armPreviewFrame(
  frame: PreviewFrameHandle,
  src: string,
): { ok: true } | { ok: false; reason: "credentialless-unsupported" } {
  frame.setAttribute("sandbox", "allow-scripts");
  frame.setAttribute("referrerpolicy", "no-referrer");
  frame.setAttribute("credentialless", "");
  if (frame.credentialless !== true) return { ok: false, reason: "credentialless-unsupported" };
  frame.src = src;
  return { ok: true };
}

const CREDENTIAL_PARAM = /^(?:token|access_token|authorization|jwt|cookie|key|secret)$/i;

export type PreviewFrameDecision =
  | {
      ok: true;
      src: string;
      sandbox: string;
      referrerPolicy: "no-referrer";
      credentialless: true;
    }
  | { ok: false; reason: "origin" | "credential-in-url" };

export function previewFrameDecision(src: string, parentOrigin: string): PreviewFrameDecision {
  let url: URL;
  try {
    url = new URL(src);
  } catch {
    return { ok: false, reason: "origin" };
  }
  if (url.username || url.password) return { ok: false, reason: "credential-in-url" };
  for (const key of url.searchParams.keys()) {
    if (CREDENTIAL_PARAM.test(key)) return { ok: false, reason: "credential-in-url" };
  }
  if (/(?:token|access_token|authorization|jwt|secret)=/i.test(url.hash)) {
    return { ok: false, reason: "credential-in-url" };
  }
  if (!canOpenCandidatePreview(url.origin, parentOrigin).ok) return { ok: false, reason: "origin" };
  return {
    ok: true,
    src,
    sandbox: candidatePreviewSandbox(),
    referrerPolicy: "no-referrer",
    credentialless: true,
  };
}
