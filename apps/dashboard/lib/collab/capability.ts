// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which anchors a preview surface can honestly support. Plain HTML gets a
// page comment. Precise modes require an opt-in instrumented document.
// Images can take an intrinsic pin. Unsupported requests become page anchors
// on the same immutable version instead of a guessed location.

import type {
  AnchorKind,
  AnchorTarget,
  FeedbackAnchor,
  ImageIntrinsicPoint,
  NormalizedPoint,
  Rendition,
  SurfaceKind,
  TextAnchor,
} from "@/lib/collab/types";

const PRECISE: readonly AnchorKind[] = ["point", "text", "component", "page"];

export function supportedKinds(surface: SurfaceKind): readonly AnchorKind[] {
  switch (surface) {
    case "instrumented-html":
      return PRECISE;
    case "image":
      return ["point", "page"];
    case "html":
    case "unknown":
      return ["page"];
    default: {
      const _never: never = surface;
      return _never;
    }
  }
}

export function unsupportedModeMessage(kind: AnchorKind): string {
  const label =
    kind === "point"
      ? "A point pin"
      : kind === "text"
        ? "Text highlighting"
        : kind === "component"
          ? "A component anchor"
          : "A page anchor";
  return `${label} is not available on this page. The comment attaches to the whole page, on the original version.`;
}

export interface AdoptSelectionInput {
  surface: SurfaceKind;
  kind: AnchorKind;
  target: AnchorTarget;
  componentId?: string;
  text?: Pick<TextAnchor, "exact" | "prefix" | "suffix" | "start" | "end" | "blockId">;
  point?:
    | { space: "document"; normalized: NormalizedPoint; rendition: Rendition }
    | { space: "image"; image: ImageIntrinsicPoint; rendition: Rendition };
}

export interface AdoptedSelection {
  anchor: FeedbackAnchor;
  downgradedFrom?: AnchorKind;
  reason?: string;
}

function pageFallback(target: AnchorTarget, kind: AnchorKind): AdoptedSelection {
  return {
    anchor: { kind: "page", ...target },
    downgradedFrom: kind,
    reason: unsupportedModeMessage(kind),
  };
}

export function adoptSelection(input: AdoptSelectionInput): AdoptedSelection {
  if (!supportedKinds(input.surface).includes(input.kind)) {
    return pageFallback(input.target, input.kind);
  }
  if (input.kind === "page") return { anchor: { kind: "page", ...input.target } };
  if (input.kind === "component") {
    if (!input.componentId) return pageFallback(input.target, "component");
    return { anchor: { kind: "component", ...input.target, componentId: input.componentId } };
  }
  if (input.kind === "text") {
    if (!input.text) return pageFallback(input.target, "text");
    return { anchor: { kind: "text", ...input.target, ...input.text } };
  }
  if (!input.point) return pageFallback(input.target, "point");
  if (input.surface === "image" && input.point.space !== "image") {
    return pageFallback(input.target, "point");
  }
  if (input.point.space === "image") {
    return {
      anchor: {
        kind: "point",
        space: "image",
        ...input.target,
        image: input.point.image,
        rendition: input.point.rendition,
      },
    };
  }
  return {
    anchor: {
      kind: "point",
      space: "document",
      ...input.target,
      normalized: input.point.normalized,
      rendition: input.point.rendition,
    },
  };
}
