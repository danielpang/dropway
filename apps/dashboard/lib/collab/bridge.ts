// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Opt-in selection bridge. A guest message is accepted only when the origin,
// the source window, and the session nonce all match, and the payload is a
// small plain object. The parent handshake is the nonce and nothing else:
// no bearer token, cookie, or comment thread is ever posted to tenant HTML.

import { adoptAnchor } from "@/lib/collab/resolve";
import { canOpenCandidatePreview } from "@/lib/collab/gate";
import type { AdoptedSelection } from "@/lib/collab/capability";
import type {
  AnchorTarget,
  FeedbackAnchor,
  ImageIntrinsicPoint,
  NormalizedPoint,
  Rendition,
  SurfaceKind,
} from "@/lib/collab/types";

export const BRIDGE_CHANNEL = "dropway.collab.v1" as const;
export const MAX_BRIDGE_BYTES = 4096;
const MAX_QUOTE = 2000;
const MAX_CONTEXT = 200;
const MAX_DIMENSION = 1_000_000;
const ID_RE = /^[A-Za-z0-9_-]{1,128}$/;
const NONCE_RE = /^[A-Za-z0-9_-]{16,128}$/;

export interface BridgeSession {
  nonce: string;
  previewOrigin: string;
  source: object;
  parentOrigin: string;
}

export interface BridgeEnvelope {
  origin: string;
  source: object | null;
  data: unknown;
  byteLength?: number;
}

export interface HelloMessage {
  channel: typeof BRIDGE_CHANNEL;
  nonce: string;
  type: "hello";
  surface: SurfaceKind;
}

export interface SelectionMessage {
  channel: typeof BRIDGE_CHANNEL;
  nonce: string;
  type: "selection";
  anchor: FeedbackAnchor;
}

export type BridgeFailureReason =
  | "origin"
  | "opaque-origin"
  | "source"
  | "nonce"
  | "malformed"
  | "oversize"
  | "channel";

export type BridgeVerdict =
  | { ok: true; message: HelloMessage | SelectionMessage }
  | { ok: false; reason: BridgeFailureReason };

export interface ParentHandshake {
  channel: typeof BRIDGE_CHANNEL;
  type: "ready";
  nonce: string;
}

function safeEqual(left: string, right: string): boolean {
  if (left.length !== right.length) return false;
  let diff = 0;
  for (let i = 0; i < left.length; i += 1) {
    diff |= left.charCodeAt(i) ^ right.charCodeAt(i);
  }
  return diff === 0;
}

function encodedSize(data: unknown): number | null {
  try {
    return new TextEncoder().encode(JSON.stringify(data)).length;
  } catch {
    return null;
  }
}

function plainRecord(data: unknown): Record<string, unknown> | null {
  if (data === null || typeof data !== "object" || Array.isArray(data)) return null;
  const proto = Object.getPrototypeOf(data);
  if (proto !== Object.prototype) return null;
  return data as Record<string, unknown>;
}

function exactKeys(record: Record<string, unknown>, keys: readonly string[]): boolean {
  const own = Object.keys(record);
  if (own.length !== keys.length) return false;
  return keys.every((key) => Object.prototype.hasOwnProperty.call(record, key));
}

function isPagePath(page: string): boolean {
  if (!page.startsWith("/") || page.startsWith("//") || page.includes("\\")) return false;
  if (page.length > 512) return false;
  if (/[\u0000-\u0020\u007f?#]/.test(page)) return false;
  return !page.split("/").some((part) => part === "." || part === "..");
}

function isId(value: unknown): value is string {
  return typeof value === "string" && ID_RE.test(value);
}

function parseTarget(record: Record<string, unknown>): AnchorTarget | null {
  if (!isId(record.siteId) || !isId(record.versionId) || !isId(record.contentHash)) return null;
  if (typeof record.page !== "string" || !isPagePath(record.page)) return null;
  return {
    siteId: record.siteId,
    versionId: record.versionId,
    contentHash: record.contentHash,
    page: record.page,
  };
}

function finiteInRange(value: unknown, min: number, max: number): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= min && value <= max;
}

function parseRendition(value: unknown): Rendition | null {
  const record = plainRecord(value);
  if (!record || !exactKeys(record, ["viewportWidth", "viewportHeight", "scrollX", "scrollY", "documentWidth", "documentHeight"])) {
    return null;
  }
  if (
    !finiteInRange(record.viewportWidth, 1, MAX_DIMENSION) ||
    !finiteInRange(record.viewportHeight, 1, MAX_DIMENSION) ||
    !finiteInRange(record.scrollX, 0, MAX_DIMENSION) ||
    !finiteInRange(record.scrollY, 0, MAX_DIMENSION) ||
    !finiteInRange(record.documentWidth, 1, MAX_DIMENSION) ||
    !finiteInRange(record.documentHeight, 1, MAX_DIMENSION)
  ) {
    return null;
  }
  return {
    viewportWidth: record.viewportWidth,
    viewportHeight: record.viewportHeight,
    scrollX: record.scrollX,
    scrollY: record.scrollY,
    documentWidth: record.documentWidth,
    documentHeight: record.documentHeight,
  };
}

function parseNormalized(record: Record<string, unknown>): NormalizedPoint | null {
  if (!finiteInRange(record.x, 0, 1) || !finiteInRange(record.y, 0, 1)) return null;
  return { x: record.x, y: record.y };
}

function parseImage(value: unknown): ImageIntrinsicPoint | null {
  const record = plainRecord(value);
  if (!record || !exactKeys(record, ["imageId", "naturalWidth", "naturalHeight", "x", "y"])) return null;
  if (!isId(record.imageId)) return null;
  if (!finiteInRange(record.naturalWidth, 1, MAX_DIMENSION) || !finiteInRange(record.naturalHeight, 1, MAX_DIMENSION)) {
    return null;
  }
  if (!finiteInRange(record.x, 0, record.naturalWidth) || !finiteInRange(record.y, 0, record.naturalHeight)) {
    return null;
  }
  return {
    imageId: record.imageId,
    naturalWidth: record.naturalWidth,
    naturalHeight: record.naturalHeight,
    x: record.x,
    y: record.y,
  };
}

function parseAnchor(value: unknown): FeedbackAnchor | null {
  const record = plainRecord(value);
  if (!record || typeof record.kind !== "string") return null;
  const target = parseTarget(record);
  if (!target) return null;
  if (record.kind === "page") {
    if (!exactKeys(record, ["kind", "siteId", "versionId", "contentHash", "page"])) return null;
    return { kind: "page", ...target };
  }
  if (record.kind === "component") {
    if (!exactKeys(record, ["kind", "siteId", "versionId", "contentHash", "page", "componentId"])) return null;
    if (!isId(record.componentId)) return null;
    return { kind: "component", ...target, componentId: record.componentId };
  }
  if (record.kind === "text") {
    const keys = Object.keys(record);
    const allowed = ["kind", "siteId", "versionId", "contentHash", "page", "exact", "prefix", "suffix", "start", "end", "blockId"];
    if (keys.some((key) => !allowed.includes(key)) || keys.length < 9) return null;
    if (typeof record.exact !== "string" || record.exact.length < 1 || record.exact.length > MAX_QUOTE) return null;
    if (typeof record.prefix !== "string" || record.prefix.length > MAX_CONTEXT) return null;
    if (typeof record.suffix !== "string" || record.suffix.length > MAX_CONTEXT) return null;
    if (!finiteInRange(record.start, 0, 1_000_000) || !Number.isInteger(record.start)) return null;
    if (!finiteInRange(record.end, 0, 1_000_000) || !Number.isInteger(record.end)) return null;
    if (record.end - record.start !== record.exact.length) return null;
    if (record.blockId !== undefined && !isId(record.blockId)) return null;
    return {
      kind: "text",
      ...target,
      exact: record.exact,
      prefix: record.prefix,
      suffix: record.suffix,
      start: record.start,
      end: record.end,
      ...(record.blockId !== undefined ? { blockId: record.blockId } : {}),
    };
  }
  if (record.kind === "point") {
    if (record.space === "document") {
      if (!exactKeys(record, ["kind", "siteId", "versionId", "contentHash", "page", "space", "normalized", "rendition"])) {
        return null;
      }
      const normalizedRecord = plainRecord(record.normalized);
      if (!normalizedRecord) return null;
      const normalized = parseNormalized(normalizedRecord);
      const rendition = parseRendition(record.rendition);
      if (!normalized || !rendition) return null;
      return { kind: "point", space: "document", ...target, normalized, rendition };
    }
    if (record.space === "image") {
      if (!exactKeys(record, ["kind", "siteId", "versionId", "contentHash", "page", "space", "image", "rendition"])) {
        return null;
      }
      const image = parseImage(record.image);
      const rendition = parseRendition(record.rendition);
      if (!image || !rendition) return null;
      return { kind: "point", space: "image", ...target, image, rendition };
    }
  }
  return null;
}

function isSurface(value: unknown): value is SurfaceKind {
  return value === "image" || value === "instrumented-html" || value === "html" || value === "unknown";
}

function originAllowed(session: BridgeSession, origin: string): boolean {
  if (origin === "null") return false;
  if (!canOpenCandidatePreview(session.previewOrigin, session.parentOrigin).ok) return false;
  if (origin === session.parentOrigin) return false;
  return origin === session.previewOrigin;
}

export function validateBridgeEvent(session: BridgeSession, event: BridgeEnvelope): BridgeVerdict {
  const actual = encodedSize(event.data);
  if (actual === null) return { ok: false, reason: "malformed" };
  if (actual > MAX_BRIDGE_BYTES || (event.byteLength !== undefined && event.byteLength > MAX_BRIDGE_BYTES)) {
    return { ok: false, reason: "oversize" };
  }
  if (event.source === null || event.source !== session.source) return { ok: false, reason: "source" };
  // "null" is every opaque document. A navigated frame keeps the same window and
  // the same serialized origin, so it is not a usable peer.
  if (event.origin === "null") return { ok: false, reason: "opaque-origin" };
  if (!originAllowed(session, event.origin)) return { ok: false, reason: "origin" };

  const record = plainRecord(event.data);
  if (!record) return { ok: false, reason: "malformed" };
  if (record.channel !== BRIDGE_CHANNEL) return { ok: false, reason: "channel" };
  if (record.type !== "hello" && record.type !== "selection") return { ok: false, reason: "malformed" };
  if (typeof record.nonce !== "string" || !NONCE_RE.test(record.nonce) || !safeEqual(record.nonce, session.nonce)) {
    return { ok: false, reason: "nonce" };
  }
  if (!NONCE_RE.test(session.nonce)) return { ok: false, reason: "nonce" };

  if (record.type === "hello") {
    if (!exactKeys(record, ["channel", "nonce", "type", "surface"])) return { ok: false, reason: "malformed" };
    if (!isSurface(record.surface)) return { ok: false, reason: "malformed" };
    return {
      ok: true,
      message: { channel: BRIDGE_CHANNEL, nonce: record.nonce, type: "hello", surface: record.surface },
    };
  }

  if (!exactKeys(record, ["channel", "nonce", "type", "anchor"])) return { ok: false, reason: "malformed" };
  const anchor = parseAnchor(record.anchor);
  if (!anchor) return { ok: false, reason: "malformed" };
  return {
    ok: true,
    message: { channel: BRIDGE_CHANNEL, nonce: record.nonce, type: "selection", anchor },
  };
}

export function stampPreviewTarget<T extends AnchorTarget>(anchor: T, preview: AnchorTarget): T {
  return {
    ...anchor,
    siteId: preview.siteId,
    versionId: preview.versionId,
    contentHash: preview.contentHash,
    page: preview.page,
  };
}

export type AcceptedGuestMessage =
  | { ok: false; reason: BridgeFailureReason }
  | { ok: true; type: "hello"; surface: SurfaceKind }
  | { ok: true; type: "selection"; adopted: AdoptedSelection };

export function acceptGuestMessage(
  session: BridgeSession,
  event: BridgeEnvelope,
  preview: AnchorTarget,
  surface: SurfaceKind,
): AcceptedGuestMessage {
  const verdict = validateBridgeEvent(session, event);
  if (!verdict.ok) return verdict;
  if (verdict.message.type === "hello") {
    return { ok: true, type: "hello", surface: verdict.message.surface };
  }
  const stamped = stampPreviewTarget(verdict.message.anchor, preview);
  const adopted = adoptAnchor(surface, stamped);
  if (adopted.downgradedFrom) return { ok: true, type: "selection", adopted };
  return { ok: true, type: "selection", adopted: { anchor: adopted.anchor } };
}

export function parentHandshake(nonce: string): ParentHandshake {
  return { channel: BRIDGE_CHANNEL, type: "ready", nonce };
}

export function handshakeDelivery(opts: {
  nonce: string;
  previewOrigin: string;
  parentOrigin: string;
  opaqueSandbox: boolean;
}): { ok: true; payload: ParentHandshake; targetOrigin: string } | { ok: false; reason: "origin" | "opaque-origin" } {
  // Chrome rejects targetOrigin "null". "*" would deliver to whichever
  // document is active, including one the parent cannot see via iframe.src.
  if (opts.opaqueSandbox) return { ok: false, reason: "opaque-origin" };
  if (!canOpenCandidatePreview(opts.previewOrigin, opts.parentOrigin).ok) {
    return { ok: false, reason: "origin" };
  }
  return {
    ok: true,
    payload: parentHandshake(opts.nonce),
    targetOrigin: opts.previewOrigin,
  };
}
