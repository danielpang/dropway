// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Comment drafting stays on the parent. Run and Accept are parent controls and
// are intentionally absent: commenting does not require an agent and does not
// start one. The submission cites the anchor's original version.

import type { AnchorRecord } from "@/lib/collab/types";
import type { FeedbackAnchor } from "@/lib/collab/types";

export function commentControls(): readonly ["comment"] {
  return ["comment"];
}

export type CommentSubmission =
  | { ok: false; reason: "blank" }
  | {
      ok: true;
      body: string;
      anchor: FeedbackAnchor;
      original: FeedbackAnchor;
      triggersExecution: false;
    };

export function commentSubmission(record: AnchorRecord, draft: string): CommentSubmission {
  const body = draft.trim();
  if (!body) return { ok: false, reason: "blank" };
  return {
    ok: true,
    body,
    anchor: record.current,
    original: record.origin,
    triggersExecution: false,
  };
}

export function feedbackPinA11y(label: string): {
  type: "button";
  contentEditable: "false";
  "aria-label": string;
  "data-feedback-only": "true";
} {
  const detail = label.length > 0 ? label.charAt(0).toLowerCase() + label.slice(1) : "feedback";
  return {
    type: "button",
    contentEditable: "false",
    "aria-label": `Feedback pin, ${detail}`,
    "data-feedback-only": "true",
  };
}
