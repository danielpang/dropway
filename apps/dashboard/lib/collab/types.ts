// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Feedback anchors for a same-org review of one immutable site version.
// A pin is feedback about that version. It is not an edit to the source,
// and nothing here starts an agent run.

export const ANCHOR_KINDS = ["point", "text", "component", "page"] as const;
export type AnchorKind = (typeof ANCHOR_KINDS)[number];

export const SURFACE_KINDS = ["image", "instrumented-html", "html", "unknown"] as const;
export type SurfaceKind = (typeof SURFACE_KINDS)[number];

/** The site, immutable version, content hash, and page an anchor belongs to. */
export interface AnchorTarget {
  siteId: string;
  versionId: string;
  contentHash: string;
  page: string;
}

/** Viewport, scroll, and document size captured with a point. */
export interface Rendition {
  viewportWidth: number;
  viewportHeight: number;
  scrollX: number;
  scrollY: number;
  documentWidth: number;
  documentHeight: number;
}

export interface NormalizedPoint {
  /** Fraction of the captured document width, from 0 to 1. */
  x: number;
  /** Fraction of the captured document height, from 0 to 1. */
  y: number;
}

/** A pin on an image, in that image's intrinsic pixel space. */
export interface ImageIntrinsicPoint {
  imageId: string;
  naturalWidth: number;
  naturalHeight: number;
  x: number;
  y: number;
}

export interface ImageBox {
  imageId: string;
  left: number;
  top: number;
  width: number;
  height: number;
  naturalWidth: number;
  naturalHeight: number;
}

export type PointAnchor = AnchorTarget & {
  kind: "point";
  rendition: Rendition;
} & (
  | { space: "document"; normalized: NormalizedPoint }
  | { space: "image"; image: ImageIntrinsicPoint }
);

export interface TextAnchor extends AnchorTarget {
  kind: "text";
  exact: string;
  prefix: string;
  suffix: string;
  /** UTF-16 code unit offsets, not Unicode code points. */
  start: number;
  end: number;
  blockId?: string;
}

export interface ComponentAnchor extends AnchorTarget {
  kind: "component";
  /** Stable visual id from an opt-in attribute. Not a source-map id. */
  componentId: string;
}

export interface PageAnchor extends AnchorTarget {
  kind: "page";
}

export type FeedbackAnchor = PointAnchor | TextAnchor | ComponentAnchor | PageAnchor;

export type TargetMismatch = "site" | "version" | "content-hash" | "page";

export interface ReanchorEntry {
  at: string;
  from: FeedbackAnchor;
  to: FeedbackAnchor;
  reason: "explicit";
}

/** Original capture plus later explicit reanchors. Resolution never writes this. */
export interface AnchorRecord {
  origin: FeedbackAnchor;
  current: FeedbackAnchor;
  history: readonly ReanchorEntry[];
}
