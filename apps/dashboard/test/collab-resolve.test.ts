// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Anchor resolution, honest mode fallback, and explicit reanchor history.
// Resolving a stale anchor never rewrites the record.

import { describe, expect, it } from "vitest";

import {
  adoptSelection,
  anchorContextLabel,
  createAnchorRecord,
  reanchor,
  resolveAnchor,
  unsupportedModeMessage,
} from "@/lib/collab";

const target = {
  siteId: "site-1",
  versionId: "ver-1",
  contentHash: "hash-1",
  page: "/report.html",
};

const rendition = {
  viewportWidth: 800,
  viewportHeight: 600,
  scrollX: 0,
  scrollY: 0,
  documentWidth: 1000,
  documentHeight: 2000,
};

describe("resolveAnchor", () => {
  it("anchors a page without inventing a location", () => {
    const anchor = { kind: "page" as const, ...target };
    const resolution = resolveAnchor(anchor, {
      surface: "html",
      target,
    });
    expect(resolution).toEqual({ status: "anchored" });
    expect(resolution).not.toHaveProperty("client");
    expect(resolution).not.toHaveProperty("x");
  });

  it("stales a page anchor when the immutable version differs", () => {
    const anchor = { kind: "page" as const, ...target };
    expect(
      resolveAnchor(anchor, {
        surface: "html",
        target: { ...target, versionId: "ver-2" },
      }),
    ).toEqual({ status: "stale", reason: "version" });
  });

  it("anchors one component id and orphans duplicates or absence", () => {
    const anchor = { kind: "component" as const, ...target, componentId: "hero" };
    expect(
      resolveAnchor(anchor, {
        surface: "instrumented-html",
        target,
        componentIds: ["hero"],
      }),
    ).toEqual({ status: "anchored" });
    expect(
      resolveAnchor(anchor, {
        surface: "instrumented-html",
        target,
        componentIds: [],
      }),
    ).toEqual({ status: "missing" });
    expect(
      resolveAnchor(anchor, {
        surface: "instrumented-html",
        target,
        componentIds: ["hero", "hero"],
      }),
    ).toEqual({ status: "ambiguous", count: 2 });
  });

  it("does not resolve a text quote on uninstrumented HTML, even when the quote is present", () => {
    const anchor = {
      kind: "text" as const,
      ...target,
      exact: "hello",
      prefix: "",
      suffix: "",
      start: 0,
      end: 5,
    };
    const resolution = resolveAnchor(anchor, {
      surface: "html",
      target,
      text: "hello",
    });
    expect(resolution.status).toBe("unsupported");
    if (resolution.status !== "unsupported") return;
    expect(resolution.pageFallback).toEqual({ kind: "page", ...target });
    expect(resolution.downgradedFrom).toBe("text");
  });

  it("keeps a document pin anchored on instrumented HTML at the same rendition", () => {
    const anchor = {
      kind: "point" as const,
      space: "document" as const,
      ...target,
      normalized: { x: 0.1, y: 0.04 },
      rendition,
    };
    expect(
      resolveAnchor(anchor, { surface: "instrumented-html", target, metrics: rendition }),
    ).toMatchObject({ status: "anchored", client: { x: 100, y: 80 } });
  });
});

describe("adoptSelection", () => {
  it("keeps precise modes on instrumented HTML and image pins on images", () => {
    expect(
      adoptSelection({
        surface: "instrumented-html",
        kind: "component",
        target,
        componentId: "hero",
      }).anchor,
    ).toEqual({
      kind: "component",
      ...target,
      componentId: "hero",
    });
    const image = adoptSelection({
      surface: "image",
      kind: "point",
      target,
      point: {
        space: "image",
        image: { imageId: "hero", naturalWidth: 10, naturalHeight: 10, x: 1, y: 2 },
        rendition,
      },
    });
    expect(image.anchor.kind).toBe("point");
    if (image.anchor.kind !== "point" || image.anchor.space !== "image") return;
    expect(image.anchor.image).toMatchObject({ x: 1, y: 2 });
  });

  it("downgrades text on an image or plain HTML to a page anchor on the original version", () => {
    const adopted = adoptSelection({
      surface: "html",
      kind: "text",
      target,
      text: { exact: "hello", prefix: "", suffix: "", start: 0, end: 5 },
    });
    expect(adopted.anchor).toEqual({ kind: "page", ...target });
    expect(adopted.downgradedFrom).toBe("text");
    expect(adopted.reason).toBe(unsupportedModeMessage("text"));
    expect(adopted.anchor).not.toHaveProperty("exact");
  });

  it("refuses a document-space pin on an image", () => {
    const adopted = adoptSelection({
      surface: "image",
      kind: "point",
      target,
      point: { space: "document", normalized: { x: 0.2, y: 0.2 }, rendition },
    });
    expect(adopted.anchor.kind).toBe("page");
    expect(adopted.downgradedFrom).toBe("point");
  });
});

describe("reanchor history", () => {
  it("preserves the original version and appends only an explicit reanchor", () => {
    const origin = { kind: "page" as const, ...target };
    const record = createAnchorRecord(origin);
    const moved = { ...origin, versionId: "ver-2", contentHash: "hash-2" };
    const next = reanchor(record, moved, "2026-11-07T00:00:00.000Z");
    expect(record.history).toEqual([]);
    expect(record.current).toEqual(origin);
    expect(next.origin).toEqual(origin);
    expect(next.current).toEqual(moved);
    expect(next.history).toEqual([
      {
        at: "2026-11-07T00:00:00.000Z",
        from: origin,
        to: moved,
        reason: "explicit",
      },
    ]);
    const resolution = resolveAnchor(origin, {
      surface: "html",
      target: { ...target, versionId: "ver-2" },
    });
    expect(resolution.status).toBe("stale");
    expect(record.origin).toEqual(origin);
  });

  it("labels the original site, version, hash, and page", () => {
    expect(anchorContextLabel(target)).toBe("site-1 · ver-1 · hash-1 · /report.html");
  });
});
