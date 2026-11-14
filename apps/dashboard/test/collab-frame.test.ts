// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Credentialless has to be true before src is assigned, or the first request
// is credentialed. A second parent-observed load retires the frame session.
// Neither check identifies a sandboxed document that navigated itself.

import { describe, expect, it } from "vitest";

import {
  acceptLiveFrameMessage,
  armPreviewFrame,
  candidatePreviewSandbox,
  createFrameSession,
  noteFrameLoad,
  type PreviewFrameHandle,
} from "@/lib/collab";

function fakeFrame(supported: boolean): PreviewFrameHandle & { events: string[] } {
  const events: string[] = [];
  const frame = {
    events,
    credentialless: false,
    src: "",
    setAttribute(name: string, value: string) {
      events.push(`${name}=${value}`);
      if (name === "credentialless" && supported) this.credentialless = true;
    },
  };
  let stored = "";
  Object.defineProperty(frame, "src", {
    get() {
      return stored;
    },
    set(value: string) {
      events.push(`src=${value}`);
      stored = value;
    },
  });
  return frame;
}

describe("armPreviewFrame", () => {
  it("sets sandbox, referrer policy, and credentialless before src", () => {
    const frame = fakeFrame(true);
    expect(armPreviewFrame(frame, "https://acme.dropwaycontent.com/")).toEqual({ ok: true });
    const srcAt = frame.events.findIndex((event) => event.startsWith("src="));
    const credentiallessAt = frame.events.findIndex((event) => event.startsWith("credentialless="));
    expect(credentiallessAt).toBeGreaterThanOrEqual(0);
    expect(srcAt).toBeGreaterThan(credentiallessAt);
    expect(frame.events.slice(0, srcAt)).toEqual([
      `sandbox=${candidatePreviewSandbox()}`,
      "referrerpolicy=no-referrer",
      "credentialless=",
    ]);
  });

  it("does not assign src when credentialless does not stick", () => {
    const frame = fakeFrame(false);
    expect(armPreviewFrame(frame, "https://acme.dropwaycontent.com/")).toEqual({
      ok: false,
      reason: "credentialless-unsupported",
    });
    expect(frame.events.some((event) => event.startsWith("src="))).toBe(false);
    expect(frame.src).toBe("");
  });
});

describe("frame session", () => {
  const nonce = "n".repeat(24);

  it("rejects messages before the first load and after a later load", () => {
    const fresh = createFrameSession(nonce);
    expect(acceptLiveFrameMessage(fresh, nonce)).toEqual({ ok: false, reason: "stale" });
    const armed = noteFrameLoad(fresh);
    expect(acceptLiveFrameMessage(armed, nonce)).toEqual({ ok: true });
    expect(acceptLiveFrameMessage(armed, "x".repeat(24))).toEqual({ ok: false, reason: "nonce" });
    const reloaded = noteFrameLoad(armed);
    expect(reloaded.invalidated).toBe(true);
    expect(acceptLiveFrameMessage(reloaded, nonce)).toEqual({ ok: false, reason: "stale" });
    expect(fresh.loads).toBe(0);
  });
});
