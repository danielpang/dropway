// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A frame session is armed by the first load the parent actually observes and
// retired by the next one (a parent-driven reload or src change). This does
// not identify a sandboxed document that navigates itself: that navigation can
// leave iframe.src unchanged and skip a second load event.

export interface FrameSession {
  nonce: string;
  loads: number;
  invalidated: boolean;
}

export function createFrameSession(nonce: string): FrameSession {
  return { nonce, loads: 0, invalidated: false };
}

export function noteFrameLoad(session: FrameSession): FrameSession {
  const loads = session.loads + 1;
  if (loads !== 1) return { ...session, loads, invalidated: true };
  return { ...session, loads, invalidated: false };
}

export function acceptLiveFrameMessage(
  session: FrameSession,
  nonce: string,
): { ok: true } | { ok: false; reason: "stale" | "nonce" } {
  if (session.invalidated || session.loads !== 1) return { ok: false, reason: "stale" };
  if (nonce.length !== session.nonce.length) return { ok: false, reason: "nonce" };
  let diff = 0;
  for (let i = 0; i < nonce.length; i += 1) {
    diff |= nonce.charCodeAt(i) ^ session.nonce.charCodeAt(i);
  }
  if (diff !== 0) return { ok: false, reason: "nonce" };
  return { ok: true };
}
