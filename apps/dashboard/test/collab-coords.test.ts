// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Document and image pin geometry. Pins store a location against the rendition
// they were captured on and are reprojected for scroll. A later layout or
// version never receives a silently rescaled coordinate.

import { describe, expect, it } from "vitest";

import {
  captureDocumentPoint,
  captureImagePoint,
  projectDocumentPoint,
  projectImagePoint,
} from "@/lib/collab";

const target = {
  siteId: "site-1",
  versionId: "ver-1",
  contentHash: "hash-1",
  page: "/index.html",
};

const metrics = {
  viewportWidth: 800,
  viewportHeight: 600,
  scrollX: 0,
  scrollY: 0,
  documentWidth: 1000,
  documentHeight: 2000,
};

describe("captureDocumentPoint", () => {
  it("normalizes a viewport point by scroll into document space", () => {
    const captured = captureDocumentPoint(
      { clientX: 100, clientY: 80 },
      { ...metrics, scrollX: 50, scrollY: 20 },
    );
    expect(captured).toEqual({
      ok: true,
      normalized: { x: 0.15, y: 0.05 },
      documentX: 150,
      documentY: 100,
      rendition: {
        viewportWidth: 800,
        viewportHeight: 600,
        scrollX: 50,
        scrollY: 20,
        documentWidth: 1000,
        documentHeight: 2000,
      },
    });
  });

  it("rejects a point outside the document instead of clamping it", () => {
    const captured = captureDocumentPoint({ clientX: 1500, clientY: 10 }, metrics);
    expect(captured).toEqual({ ok: false, reason: "outside" });
  });

  it("rejects non-finite or degenerate renditions", () => {
    expect(
      captureDocumentPoint({ clientX: Number.NaN, clientY: 1 }, metrics),
    ).toEqual({ ok: false, reason: "non-finite" });
    expect(
      captureDocumentPoint({ clientX: 1, clientY: 1 }, { ...metrics, documentWidth: 0 }),
    ).toEqual({ ok: false, reason: "degenerate-document" });
    expect(
      captureDocumentPoint(
        { clientX: 1, clientY: 1 },
        { ...metrics, documentWidth: 2_000_000 },
      ),
    ).toEqual({ ok: false, reason: "degenerate-document" });
  });
});

describe("projectDocumentPoint", () => {
  const captured = captureDocumentPoint({ clientX: 100, clientY: 80 }, metrics);
  if (!captured.ok) throw new Error("fixture capture failed");
  const anchor = {
    kind: "point" as const,
    space: "document" as const,
    ...target,
    normalized: captured.normalized,
    rendition: captured.rendition,
  };

  it("keeps the document location and subtracts the current scroll", () => {
    const projected = projectDocumentPoint(anchor, {
      target,
      metrics: { ...metrics, scrollX: 40, scrollY: 25 },
    });
    expect(projected).toEqual({
      status: "anchored",
      documentX: 100,
      documentY: 80,
      clientX: 60,
      clientY: 55,
    });
  });

  it("ignores a viewport resize when the document size is unchanged", () => {
    const projected = projectDocumentPoint(anchor, {
      target,
      metrics: { ...metrics, viewportWidth: 1200, viewportHeight: 900 },
    });
    expect(projected).toMatchObject({
      status: "anchored",
      documentX: 100,
      documentY: 80,
      clientX: 100,
      clientY: 80,
    });
  });

  it("does not rescale onto a different layout", () => {
    const projected = projectDocumentPoint(anchor, {
      target,
      metrics: { ...metrics, documentWidth: 500, documentHeight: 1000 },
    });
    expect(projected).toEqual({ status: "stale", reason: "layout" });
    expect(projected).not.toHaveProperty("clientX");
    expect(projected).not.toHaveProperty("clientY");
  });

  it("does not move a pin onto another version, hash, or page", () => {
    expect(
      projectDocumentPoint(anchor, {
        target: { ...target, versionId: "ver-2" },
        metrics,
      }),
    ).toEqual({ status: "stale", reason: "version" });
    expect(
      projectDocumentPoint(anchor, {
        target: { ...target, contentHash: "hash-2" },
        metrics,
      }),
    ).toEqual({ status: "stale", reason: "content-hash" });
    expect(
      projectDocumentPoint(anchor, {
        target: { ...target, page: "/other.html" },
        metrics,
      }),
    ).toEqual({ status: "stale", reason: "page" });
    expect(
      projectDocumentPoint(anchor, {
        target: { ...target, siteId: "site-2" },
        metrics,
      }),
    ).toEqual({ status: "stale", reason: "site" });
  });
});

describe("image pins", () => {
  const image = {
    imageId: "hero",
    left: 10,
    top: 30,
    width: 200,
    height: 100,
    naturalWidth: 400,
    naturalHeight: 200,
  };

  it("stores intrinsic pixels and keeps them when the rendered box changes", () => {
    const captured = captureImagePoint({ clientX: 110, clientY: 80 }, image);
    expect(captured).toEqual({
      ok: true,
      image: { imageId: "hero", naturalWidth: 400, naturalHeight: 200, x: 200, y: 100 },
    });
    if (!captured.ok) return;
    const projected = projectImagePoint(
      captured.image,
      { ...image, left: 0, top: 0, width: 100, height: 50 },
      true,
    );
    expect(projected).toEqual({
      status: "anchored",
      clientX: 50,
      clientY: 25,
      intrinsicX: 200,
      intrinsicY: 100,
    });
  });

  it("orphans a missing image or a changed intrinsic size without remapping", () => {
    const stored = {
      imageId: "hero",
      naturalWidth: 400,
      naturalHeight: 200,
      x: 200,
      y: 100,
    };
    expect(projectImagePoint(stored, null, true)).toEqual({
      status: "stale",
      reason: "missing",
    });
    expect(
      projectImagePoint(stored, { ...image, naturalWidth: 800, naturalHeight: 400 }, true),
    ).toEqual({ status: "stale", reason: "intrinsic" });
    expect(projectImagePoint(stored, image, false)).toEqual({
      status: "stale",
      reason: "version",
    });
  });

  it("rejects a click outside the rendered image", () => {
    expect(captureImagePoint({ clientX: 0, clientY: 0 }, image)).toEqual({
      ok: false,
      reason: "outside",
    });
  });
});
