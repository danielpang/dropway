// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Point capture and reprojection. Document pins are normalized against the
// rendition's layout size. Image pins stay in intrinsic pixels. Scroll changes
// the viewport position; a different version or layout does not get a new point.

import type {
  AnchorTarget,
  ImageBox,
  ImageIntrinsicPoint,
  NormalizedPoint,
  PointAnchor,
  Rendition,
  TargetMismatch,
} from "@/lib/collab/types";

export interface ViewportPoint {
  clientX: number;
  clientY: number;
}

export type CaptureDocumentResult =
  | { ok: true; normalized: NormalizedPoint; documentX: number; documentY: number; rendition: Rendition }
  | { ok: false; reason: "non-finite" | "degenerate-document" | "outside" };

export type ProjectDocumentResult =
  | { status: "anchored"; documentX: number; documentY: number; clientX: number; clientY: number }
  | { status: "stale"; reason: TargetMismatch | "layout" };

const MAX_DIMENSION = 1_000_000;

function finite(value: number): boolean {
  return typeof value === "number" && Number.isFinite(value);
}

function dimensionFailure(value: number, allowZero: boolean): "non-finite" | "degenerate-document" | null {
  if (!finite(value)) return "non-finite";
  if (value < 0 || (!allowZero && value === 0) || value > MAX_DIMENSION) return "degenerate-document";
  return null;
}

function renditionFailure(rendition: Rendition): "non-finite" | "degenerate-document" | null {
  const scrollX = dimensionFailure(rendition.scrollX, true);
  if (scrollX === "non-finite") return "non-finite";
  if (scrollX) return "degenerate-document";
  const scrollY = dimensionFailure(rendition.scrollY, true);
  if (scrollY === "non-finite") return "non-finite";
  if (scrollY) return "degenerate-document";
  for (const value of [
    rendition.viewportWidth,
    rendition.viewportHeight,
    rendition.documentWidth,
    rendition.documentHeight,
  ]) {
    const failure = dimensionFailure(value, false);
    if (failure) return failure;
  }
  return null;
}

export function targetMismatch(anchor: AnchorTarget, current: AnchorTarget): TargetMismatch | null {
  if (anchor.siteId !== current.siteId) return "site";
  if (anchor.versionId !== current.versionId) return "version";
  if (anchor.contentHash !== current.contentHash) return "content-hash";
  if (anchor.page !== current.page) return "page";
  return null;
}

export function captureDocumentPoint(
  client: ViewportPoint,
  metrics: Rendition,
): CaptureDocumentResult {
  const renditionError = renditionFailure(metrics);
  if (renditionError) return { ok: false, reason: renditionError };
  if (!finite(client.clientX) || !finite(client.clientY)) return { ok: false, reason: "non-finite" };
  const documentX = client.clientX + metrics.scrollX;
  const documentY = client.clientY + metrics.scrollY;
  if (
    documentX < 0 ||
    documentY < 0 ||
    documentX > metrics.documentWidth ||
    documentY > metrics.documentHeight
  ) {
    return { ok: false, reason: "outside" };
  }
  return {
    ok: true,
    normalized: { x: documentX / metrics.documentWidth, y: documentY / metrics.documentHeight },
    documentX,
    documentY,
    rendition: { ...metrics },
  };
}

export function projectDocumentPoint(
  anchor: Extract<PointAnchor, { space: "document" }>,
  current: { target: AnchorTarget; metrics: Rendition },
): ProjectDocumentResult {
  const mismatch = targetMismatch(anchor, current.target);
  if (mismatch) return { status: "stale", reason: mismatch };
  if (renditionFailure(current.metrics) || renditionFailure(anchor.rendition)) {
    return { status: "stale", reason: "layout" };
  }
  if (
    Math.abs(current.metrics.documentWidth - anchor.rendition.documentWidth) > 0.5 ||
    Math.abs(current.metrics.documentHeight - anchor.rendition.documentHeight) > 0.5
  ) {
    return { status: "stale", reason: "layout" };
  }
  const documentX = anchor.normalized.x * anchor.rendition.documentWidth;
  const documentY = anchor.normalized.y * anchor.rendition.documentHeight;
  return {
    status: "anchored",
    documentX,
    documentY,
    clientX: documentX - current.metrics.scrollX,
    clientY: documentY - current.metrics.scrollY,
  };
}

export type CaptureImageResult =
  | { ok: true; image: ImageIntrinsicPoint }
  | { ok: false; reason: "non-finite" | "degenerate-document" | "outside" };

export function captureImagePoint(client: ViewportPoint, image: ImageBox): CaptureImageResult {
  const values = [client.clientX, client.clientY, image.left, image.top, image.width, image.height, image.naturalWidth, image.naturalHeight];
  if (values.some((value) => !finite(value))) return { ok: false, reason: "non-finite" };
  if (
    image.width <= 0 ||
    image.height <= 0 ||
    image.naturalWidth <= 0 ||
    image.naturalHeight <= 0 ||
    image.width > MAX_DIMENSION ||
    image.height > MAX_DIMENSION ||
    image.naturalWidth > MAX_DIMENSION ||
    image.naturalHeight > MAX_DIMENSION
  ) {
    return { ok: false, reason: "degenerate-document" };
  }
  const relX = (client.clientX - image.left) / image.width;
  const relY = (client.clientY - image.top) / image.height;
  if (relX < 0 || relY < 0 || relX > 1 || relY > 1) return { ok: false, reason: "outside" };
  return {
    ok: true,
    image: {
      imageId: image.imageId,
      naturalWidth: image.naturalWidth,
      naturalHeight: image.naturalHeight,
      x: relX * image.naturalWidth,
      y: relY * image.naturalHeight,
    },
  };
}

export type ProjectImageResult =
  | { status: "anchored"; clientX: number; clientY: number; intrinsicX: number; intrinsicY: number }
  | { status: "stale"; reason: "version" | "missing" | "intrinsic" };

export function projectImagePoint(
  stored: ImageIntrinsicPoint,
  current: ImageBox | null,
  targetMatches: boolean,
): ProjectImageResult {
  if (!targetMatches) return { status: "stale", reason: "version" };
  if (!current) return { status: "stale", reason: "missing" };
  if (
    Math.abs(current.naturalWidth - stored.naturalWidth) > 0.5 ||
    Math.abs(current.naturalHeight - stored.naturalHeight) > 0.5 ||
    current.imageId !== stored.imageId
  ) {
    return { status: "stale", reason: "intrinsic" };
  }
  if (current.width <= 0 || current.height <= 0 || stored.naturalWidth <= 0 || stored.naturalHeight <= 0) {
    return { status: "stale", reason: "missing" };
  }
  return {
    status: "anchored",
    clientX: current.left + (stored.x / stored.naturalWidth) * current.width,
    clientY: current.top + (stored.y / stored.naturalHeight) * current.height,
    intrinsicX: stored.x,
    intrinsicY: stored.y,
  };
}
