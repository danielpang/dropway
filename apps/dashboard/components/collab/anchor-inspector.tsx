// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Inspection of one anchor. Shows the original version even when the current
// preview no longer matches. Reanchor is a button the parent handles; this
// panel does not move the anchor by itself.

"use client";

import * as React from "react";

import { CommentDraftForm } from "@/components/collab/comment-draft-form";
import { Button } from "@/components/ui/button";
import { anchorContextLabel, type AnchorRecord, type AnchorResolution } from "@/lib/collab";

function statusText(resolution: AnchorResolution): string {
  switch (resolution.status) {
    case "anchored":
      return "Attached to the original version.";
    case "missing":
      return "Orphaned. The anchor is missing here and was not reattached.";
    case "ambiguous":
      return `Orphaned. ${resolution.count} matches, so none was chosen.`;
    case "stale":
      return `Orphaned (${resolution.reason}). The original version was kept.`;
    case "unsupported":
      return resolution.reason;
    default: {
      const _never: never = resolution;
      return _never;
    }
  }
}

export function AnchorInspector({
  record,
  resolution,
  draft,
  reanchorConnected,
  onDraftChange,
  onSubmit,
  onDraftKeyDown,
  onReanchor,
  panelRef,
}: {
  record: AnchorRecord;
  resolution: AnchorResolution;
  draft: string;
  reanchorConnected: boolean;
  onDraftChange: (value: string) => void;
  onSubmit: () => void;
  onDraftKeyDown: (event: React.KeyboardEvent<HTMLTextAreaElement>) => void;
  onReanchor: () => void;
  panelRef: React.Ref<HTMLDivElement>;
}) {
  const sameVersion = record.origin.versionId === record.current.versionId && record.origin.contentHash === record.current.contentHash;
  return (
    <div
      ref={panelRef}
      role="region"
      aria-label="Feedback inspection"
      tabIndex={-1}
      className="space-y-3 rounded-md border border-border bg-background p-3 outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="space-y-1">
        <p className="text-sm font-medium text-foreground">Feedback, not an edit</p>
        <p className="text-sm text-muted-foreground">{statusText(resolution)}</p>
        <p className="font-mono text-xs text-muted-foreground">
          Original {anchorContextLabel(record.origin)}
        </p>
        {sameVersion ? null : (
          <p className="font-mono text-xs text-muted-foreground">
            Current anchor {anchorContextLabel(record.current)}
          </p>
        )}
      </div>
      <div className="space-y-1">
        <Button type="button" size="sm" variant="outline" disabled={!reanchorConnected} onClick={onReanchor}>
          Reanchor
        </Button>
        {reanchorConnected ? null : (
          <p className="text-xs text-muted-foreground">
            Reanchor is explicit and waits for the review room.
          </p>
        )}
      </div>
      <CommentDraftForm
        value={draft}
        onChange={onDraftChange}
        onSubmit={onSubmit}
        onDraftKeyDown={onDraftKeyDown}
      />
    </div>
  );
}
