// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Resolve a stored anchor against the preview that is open now. A mismatch
// is reported. This function does not move the anchor onto a new version,
// layout, or nearby quote.

import { adoptSelection, supportedKinds, unsupportedModeMessage } from "@/lib/collab/capability";
import { projectDocumentPoint, projectImagePoint, targetMismatch } from "@/lib/collab/coords";
import { resolveTextQuote } from "@/lib/collab/text-quote";
import type {
  AnchorKind,
  AnchorTarget,
  FeedbackAnchor,
  ImageBox,
  PageAnchor,
  Rendition,
  SurfaceKind,
} from "@/lib/collab/types";

export interface ResolveEnvironment {
  surface: SurfaceKind;
  target: AnchorTarget;
  metrics?: Rendition;
  image?: ImageBox | null;
  text?: string | null;
  blockId?: string;
  componentIds?: readonly string[];
}

export type AnchorResolution =
  | { status: "anchored"; client?: { x: number; y: number } }
  | { status: "missing" }
  | { status: "ambiguous"; count: number }
  | { status: "stale"; reason: string }
  | {
      status: "unsupported";
      pageFallback: PageAnchor;
      downgradedFrom: AnchorKind;
      reason: string;
    };

function unsupported(anchor: FeedbackAnchor): AnchorResolution {
  return {
    status: "unsupported",
    pageFallback: {
      kind: "page",
      siteId: anchor.siteId,
      versionId: anchor.versionId,
      contentHash: anchor.contentHash,
      page: anchor.page,
    },
    downgradedFrom: anchor.kind,
    reason: unsupportedModeMessage(anchor.kind),
  };
}

export function resolveAnchor(anchor: FeedbackAnchor, env: ResolveEnvironment): AnchorResolution {
  const mismatch = targetMismatch(anchor, env.target);
  if (mismatch) return { status: "stale", reason: mismatch };
  if (!supportedKinds(env.surface).includes(anchor.kind)) return unsupported(anchor);
  if (anchor.kind === "page") return { status: "anchored" };

  if (anchor.kind === "component") {
    const count = (env.componentIds ?? []).filter((id) => id === anchor.componentId).length;
    if (count === 1) return { status: "anchored" };
    if (count === 0) return { status: "missing" };
    return { status: "ambiguous", count };
  }

  if (anchor.kind === "text") {
    const quote = resolveTextQuote(
      {
        exact: anchor.exact,
        prefix: anchor.prefix,
        suffix: anchor.suffix,
        start: anchor.start,
        end: anchor.end,
        blockId: anchor.blockId,
      },
      { text: env.text ?? null, blockId: env.blockId },
    );
    if (quote.status === "anchored") return { status: "anchored" };
    if (quote.status === "ambiguous") return { status: "ambiguous", count: quote.count };
    if (quote.status === "stale") return { status: "stale", reason: quote.reason };
    if (quote.status === "invalid") return { status: "missing" };
    return { status: "missing" };
  }

  if (anchor.space === "image") {
    if (env.surface === "image" && env.metrics === undefined && env.image === undefined) {
      return { status: "stale", reason: "missing" };
    }
    const projected = projectImagePoint(anchor.image, env.image ?? null, true);
    if (projected.status === "stale") return projected;
    return { status: "anchored", client: { x: projected.clientX, y: projected.clientY } };
  }

  if (!env.metrics) return { status: "stale", reason: "layout" };
  const projected = projectDocumentPoint(anchor, { target: env.target, metrics: env.metrics });
  if (projected.status === "stale") return projected;
  return { status: "anchored", client: { x: projected.clientX, y: projected.clientY } };
}

/** Selection helper used when a caller already holds a full anchor. */
export function adoptAnchor(surface: SurfaceKind, anchor: FeedbackAnchor) {
  return adoptSelection({
    surface,
    kind: anchor.kind,
    target: {
      siteId: anchor.siteId,
      versionId: anchor.versionId,
      contentHash: anchor.contentHash,
      page: anchor.page,
    },
    componentId: anchor.kind === "component" ? anchor.componentId : undefined,
    text:
      anchor.kind === "text"
        ? {
            exact: anchor.exact,
            prefix: anchor.prefix,
            suffix: anchor.suffix,
            start: anchor.start,
            end: anchor.end,
            blockId: anchor.blockId,
          }
        : undefined,
    point:
      anchor.kind === "point"
        ? anchor.space === "image"
          ? { space: "image" as const, image: anchor.image, rendition: anchor.rendition }
          : { space: "document" as const, normalized: anchor.normalized, rendition: anchor.rendition }
        : undefined,
  });
}
