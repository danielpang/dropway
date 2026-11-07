// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Sandboxed candidate preview. The frame is credentialless and not
// same-origin with the dashboard. No bridge script is injected here.

"use client";

import * as React from "react";

import { previewFrameDecision } from "@/lib/collab";

export function CandidatePreviewFrame({
  src,
  parentOrigin,
  title,
  frameRef,
  onLoad,
}: {
  src: string;
  parentOrigin: string;
  title: string;
  frameRef: React.Ref<HTMLIFrameElement>;
  onLoad: () => void;
}) {
  const decision = previewFrameDecision(src, parentOrigin);
  if (!decision.ok) {
    return (
      <p role="note" className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
        This preview URL is not an allowed content origin, so the frame stays unloaded.
        Page comments are the supported mode. Precise pins are not invented for it.
      </p>
    );
  }
  return (
    <iframe
      ref={(node) => {
        if (typeof frameRef === "function") frameRef(node);
        else if (frameRef) frameRef.current = node;
        if (node) node.setAttribute("credentialless", "");
      }}
      src={decision.src}
      title={title}
      sandbox={decision.sandbox}
      referrerPolicy={decision.referrerPolicy}
      onLoad={onLoad}
      className="h-72 w-full rounded-md border border-border bg-background"
    />
  );
}
