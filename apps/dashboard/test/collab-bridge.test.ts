// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The selection bridge accepts one opted-in preview window. Forged origins,
// a different source window, a bad nonce, and malformed or oversized payloads
// are dropped. The parent handshake carries no credential and no thread.

import { describe, expect, it } from "vitest";

import {
  acceptGuestMessage,
  guestBridgeSource,
  handshakeDelivery,
  parentHandshake,
  stampPreviewTarget,
  validateBridgeEvent,
} from "@/lib/collab";

const source = { id: "preview-frame" };
const otherSource = { id: "other-frame" };
const nonce = "n".repeat(24);

const session = {
  nonce,
  previewOrigin: "https://acme.dropwaycontent.com",
  source,
  parentOrigin: "https://app.dropway.dev",
  allowOpaqueOrigin: false,
};

function hello(overrides: Record<string, unknown> = {}) {
  return {
    channel: "dropway.collab.v1",
    nonce,
    type: "hello",
    surface: "instrumented-html",
    ...overrides,
  };
}

describe("validateBridgeEvent", () => {
  it("accepts a hello from the preview origin and source window", () => {
    expect(
      validateBridgeEvent(session, { origin: session.previewOrigin, source, data: hello() }),
    ).toEqual({
      ok: true,
      message: {
        channel: "dropway.collab.v1",
        nonce,
        type: "hello",
        surface: "instrumented-html",
      },
    });
  });

  it("rejects a forged origin, the dashboard origin, and a custom domain", () => {
    const data = hello();
    expect(
      validateBridgeEvent(session, {
        origin: "https://evil.dropwaycontent.com",
        source,
        data,
      }),
    ).toEqual({ ok: false, reason: "origin" });
    expect(
      validateBridgeEvent(session, { origin: "https://app.dropway.dev", source, data }),
    ).toEqual({ ok: false, reason: "origin" });
    expect(
      validateBridgeEvent(
        { ...session, previewOrigin: "https://docs.example.com" },
        { origin: "https://docs.example.com", source, data },
      ),
    ).toEqual({ ok: false, reason: "origin" });
  });

  it("accepts an opaque sandbox origin only for the known source window", () => {
    const opaque = { ...session, allowOpaqueOrigin: true };
    const data = hello();
    expect(
      validateBridgeEvent(opaque, { origin: "null", source, data }),
    ).toMatchObject({ ok: true });
    expect(
      validateBridgeEvent(session, { origin: "null", source, data }),
    ).toEqual({ ok: false, reason: "origin" });
    expect(
      validateBridgeEvent(opaque, { origin: "null", source: otherSource, data }),
    ).toEqual({ ok: false, reason: "source" });
  });

  it("rejects a different source window and a wrong nonce", () => {
    expect(
      validateBridgeEvent(session, {
        origin: session.previewOrigin,
        source: otherSource,
        data: hello(),
      }),
    ).toEqual({ ok: false, reason: "source" });
    expect(
      validateBridgeEvent(session, {
        origin: session.previewOrigin,
        source,
        data: hello({ nonce: "x".repeat(24) }),
      }),
    ).toEqual({ ok: false, reason: "nonce" });
  });

  it("rejects malformed payloads, invented page points, and execution types", () => {
    const origin = session.previewOrigin;
    for (const data of [null, "hello", [], 1, { channel: "dropway.collab.v1" }]) {
      expect(validateBridgeEvent(session, { origin, source, data }).ok).toBe(false);
    }
    expect(
      validateBridgeEvent(session, {
        origin,
        source,
        data: hello({ type: "run" }),
      }),
    ).toEqual({ ok: false, reason: "malformed" });
    expect(
      validateBridgeEvent(session, {
        origin,
        source,
        data: {
          channel: "dropway.collab.v1",
          nonce,
          type: "selection",
          authorization: "Bearer secret",
          anchor: { kind: "page", ...targetFields() },
        },
      }),
    ).toEqual({ ok: false, reason: "malformed" });
    expect(
      validateBridgeEvent(session, {
        origin,
        source,
        data: {
          channel: "dropway.collab.v1",
          nonce,
          type: "selection",
          anchor: { kind: "page", ...targetFields(), x: 0.2, y: 0.2 },
        },
      }),
    ).toEqual({ ok: false, reason: "malformed" });
  });

  it("rejects an oversized payload even if the claimed byte length is small", () => {
    const exact = "a".repeat(5000);
    expect(
      validateBridgeEvent(session, {
        origin: session.previewOrigin,
        source,
        byteLength: 10,
        data: {
          channel: "dropway.collab.v1",
          nonce,
          type: "selection",
          anchor: {
            kind: "text",
            ...targetFields(),
            exact,
            prefix: "",
            suffix: "",
            start: 0,
            end: exact.length,
          },
        },
      }),
    ).toEqual({ ok: false, reason: "oversize" });
  });

  it("accepts a bounded page selection and a component id that is not a source path", () => {
    expect(
      validateBridgeEvent(session, {
        origin: session.previewOrigin,
        source,
        data: {
          channel: "dropway.collab.v1",
          nonce,
          type: "selection",
          anchor: { kind: "page", ...targetFields() },
        },
      }).ok,
    ).toBe(true);
    expect(
      validateBridgeEvent(session, {
        origin: session.previewOrigin,
        source,
        data: {
          channel: "dropway.collab.v1",
          nonce,
          type: "selection",
          anchor: {
            kind: "component",
            ...targetFields(),
            componentId: "src/Hero.tsx",
          },
        },
      }),
    ).toEqual({ ok: false, reason: "malformed" });
  });
});

describe("parent handshake", () => {
  it("sends only the channel, ready type, and nonce", () => {
    const payload = parentHandshake(nonce);
    expect(Object.keys(payload).sort()).toEqual(["channel", "nonce", "type"]);
    expect(JSON.stringify(payload).toLowerCase()).not.toMatch(
      /bearer|authorization|cookie|thread|jwt/,
    );
    const delivery = handshakeDelivery({
      nonce,
      previewOrigin: session.previewOrigin,
      parentOrigin: session.parentOrigin,
      opaqueSandbox: true,
    });
    expect(delivery).toEqual({
      ok: true,
      payload,
      targetOrigin: "*",
    });
  });

  it("addresses a platform content origin directly when the frame is not opaque", () => {
    const delivery = handshakeDelivery({
      nonce,
      previewOrigin: session.previewOrigin,
      parentOrigin: session.parentOrigin,
      opaqueSandbox: false,
    });
    expect(delivery).toMatchObject({
      ok: true,
      targetOrigin: "https://acme.dropwaycontent.com",
    });
  });

  it("refuses to address a custom domain or the dashboard origin", () => {
    expect(
      handshakeDelivery({
        nonce,
        previewOrigin: "https://docs.example.com",
        parentOrigin: session.parentOrigin,
        opaqueSandbox: false,
      }),
    ).toEqual({ ok: false, reason: "origin" });
    expect(
      handshakeDelivery({
        nonce,
        previewOrigin: session.parentOrigin,
        parentOrigin: session.parentOrigin,
        opaqueSandbox: false,
      }),
    ).toEqual({ ok: false, reason: "origin" });
  });
});

describe("acceptGuestMessage", () => {
  const preview = targetFields();

  it("stamps the parent preview onto a page selection and drops a forged origin", () => {
    const accepted = acceptGuestMessage(
      session,
      {
        origin: session.previewOrigin,
        source,
        data: {
          channel: "dropway.collab.v1",
          nonce,
          type: "selection",
          anchor: { kind: "page", siteId: "attacker", versionId: "nope", contentHash: "nope", page: "/phish" },
        },
      },
      preview,
      "html",
    );
    expect(accepted).toEqual({
      ok: true,
      type: "selection",
      adopted: { anchor: { kind: "page", ...preview } },
    });
    expect(
      acceptGuestMessage(
        session,
        { origin: "https://evil.dropwaycontent.com", source, data: hello() },
        preview,
        "html",
      ),
    ).toEqual({ ok: false, reason: "origin" });
  });

  it("downgrades a text selection on plain HTML to the original page", () => {
    const accepted = acceptGuestMessage(
      session,
      {
        origin: session.previewOrigin,
        source,
        data: {
          channel: "dropway.collab.v1",
          nonce,
          type: "selection",
          anchor: {
            kind: "text",
            ...preview,
            exact: "hello",
            prefix: "",
            suffix: "",
            start: 0,
            end: 5,
          },
        },
      },
      preview,
      "html",
    );
    expect(accepted.ok).toBe(true);
    if (!accepted.ok || accepted.type !== "selection") return;
    expect(accepted.adopted.anchor).toEqual({ kind: "page", ...preview });
    expect(accepted.adopted.downgradedFrom).toBe("text");
  });
});

describe("stampPreviewTarget", () => {
  it("replaces the child-claimed version with the preview the parent opened", () => {
    const claimed = {
      kind: "page" as const,
      siteId: "attacker",
      versionId: "other",
      contentHash: "nope",
      page: "/phish",
    };
    const preview = {
      siteId: "site-1",
      versionId: "ver-1",
      contentHash: "hash-1",
      page: "/index.html",
    };
    expect(stampPreviewTarget(claimed, preview)).toEqual({ kind: "page", ...preview });
  });
});

describe("guestBridgeSource", () => {
  it("stays idle unless the document opts in and never reads credentials", () => {
    const src = guestBridgeSource();
    expect(src).toContain('data-dropway-review');
    expect(src).toContain("dropway.collab.v1");
    expect(src).toContain("window.parent");
    const lower = src.toLowerCase();
    expect(lower).not.toContain("cookie");
    expect(lower).not.toContain("localstorage");
    expect(lower).not.toContain("authorization");
    expect(lower).not.toContain("bearer");
    expect(src).not.toContain("eval(");
  });
});

function targetFields() {
  return {
    siteId: "site-1",
    versionId: "ver-1",
    contentHash: "hash-1",
    page: "/index.html",
  };
}
