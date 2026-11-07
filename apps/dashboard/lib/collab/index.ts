// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Public integration seam for phase-1 review primitives.
//
// A future same-org room calls these functions once a backend contract exists.
// This module does not fetch, does not invent endpoints, and does not mount a
// live review room. CollabReviewPort is the callback shape that room will
// implement; nothing here invokes a network client.
//
// Files owned by this seam:
//   lib/collab/*           pure anchor, bridge, keyboard, and gate logic
//   components/collab/*    parent-side controls, default-off
//   test/collab-*.test.ts  harness for the pure logic

export type {
  AnchorKind,
  AnchorRecord,
  AnchorTarget,
  ComponentAnchor,
  FeedbackAnchor,
  ImageBox,
  ImageIntrinsicPoint,
  NormalizedPoint,
  PageAnchor,
  PointAnchor,
  ReanchorEntry,
  Rendition,
  SurfaceKind,
  TargetMismatch,
  TextAnchor,
} from "@/lib/collab/types";

export { captureDocumentPoint, captureImagePoint, projectDocumentPoint, projectImagePoint, targetMismatch } from "@/lib/collab/coords";
export type { CaptureDocumentResult, CaptureImageResult, ProjectDocumentResult, ProjectImageResult, ViewportPoint } from "@/lib/collab/coords";

export { resolveTextQuote } from "@/lib/collab/text-quote";
export type { TextQuote, TextQuoteResolution, TextScope } from "@/lib/collab/text-quote";

export { adoptSelection, supportedKinds, unsupportedModeMessage } from "@/lib/collab/capability";
export type { AdoptedSelection, AdoptSelectionInput } from "@/lib/collab/capability";

export { adoptAnchor, resolveAnchor } from "@/lib/collab/resolve";
export type { AnchorResolution, ResolveEnvironment } from "@/lib/collab/resolve";

export { anchorContextLabel, createAnchorRecord, reanchor } from "@/lib/collab/reanchor";

export {
  acceptGuestMessage,
  handshakeDelivery,
  parentHandshake,
  stampPreviewTarget,
  validateBridgeEvent,
  BRIDGE_CHANNEL,
  MAX_BRIDGE_BYTES,
} from "@/lib/collab/bridge";
export type {
  AcceptedGuestMessage,
  BridgeEnvelope,
  BridgeSession,
  BridgeVerdict,
  HelloMessage,
  ParentHandshake,
  SelectionMessage,
} from "@/lib/collab/bridge";

export { guestBridgeSource } from "@/lib/collab/guest-bridge";

export { acceptLiveFrameMessage, createFrameSession, noteFrameLoad } from "@/lib/collab/frame-session";
export type { FrameSession } from "@/lib/collab/frame-session";

export { initialReviewState, reduceReview } from "@/lib/collab/keyboard";
export type { ReviewEvent, ReviewMode, ReviewSessionState, SubmittedComment } from "@/lib/collab/keyboard";

export { commentControls, commentSubmission, feedbackPinA11y } from "@/lib/collab/comment";
export type { CommentSubmission } from "@/lib/collab/comment";

export { bridgePrerequisites, canOpenCandidatePreview, isCollabReviewEnabled, trustedDashboardOrigin } from "@/lib/collab/gate";
export type { BridgePrerequisites, CollabReviewEnv, DashboardOriginEnv } from "@/lib/collab/gate";

export { armPreviewFrame, candidatePreviewSandbox, previewFrameDecision } from "@/lib/collab/preview-frame";
export type { PreviewFrameDecision, PreviewFrameHandle } from "@/lib/collab/preview-frame";

import type { AnchorRecord, FeedbackAnchor } from "@/lib/collab/types";

/**
 * Callbacks a future review room supplies. Implementations must not be invoked
 * by the bridge, and submitComment must not start an agent.
 */
export interface CollabReviewPort {
  submitComment(input: {
    anchor: FeedbackAnchor;
    original: FeedbackAnchor;
    body: string;
    triggersExecution: false;
  }): void;
  requestReanchor(record: AnchorRecord): void;
}
