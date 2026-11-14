// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reanchor is an explicit action. Calling it records the previous anchor and
// keeps the first capture as the original version context.

import type { AnchorRecord, AnchorTarget, FeedbackAnchor } from "@/lib/collab/types";

export function createAnchorRecord(anchor: FeedbackAnchor): AnchorRecord {
  return { origin: anchor, current: anchor, history: [] };
}

export function reanchor(record: AnchorRecord, next: FeedbackAnchor, at: string): AnchorRecord {
  return {
    origin: record.origin,
    current: next,
    history: [
      ...record.history,
      { at, from: record.current, to: next, reason: "explicit" },
    ],
  };
}

export function anchorContextLabel(anchor: AnchorTarget): string {
  return `${anchor.siteId} · ${anchor.versionId} · ${anchor.contentHash} · ${anchor.page}`;
}
