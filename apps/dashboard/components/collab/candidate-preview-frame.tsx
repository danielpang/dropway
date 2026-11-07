// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Sandboxed candidate preview. The iframe is created with sandbox, referrer
// policy, and credentialless already applied, and only then given a src and
// inserted. A ref that sets credentialless after React assigns src is too
// late: the first request has already included cookies. If the browser does
// not reflect credentialless, the frame is not given a src.

"use client";

import * as React from "react";

import { armPreviewFrame, previewFrameDecision, type PreviewFrameHandle } from "@/lib/collab";

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
  const hostRef = React.useRef<HTMLDivElement>(null);
  const onLoadRef = React.useRef(onLoad);
  onLoadRef.current = onLoad;
  const [blocked, setBlocked] = React.useState(false);

  const allowedSrc = decision.ok ? decision.src : "";

  React.useLayoutEffect(() => {
    const host = hostRef.current;
    if (!host || !allowedSrc) return;
    const iframe = document.createElement("iframe");
    iframe.title = title;
    iframe.className = "h-72 w-full bg-background";
    const armed = armPreviewFrame(iframe as unknown as PreviewFrameHandle, allowedSrc);
    if (!armed.ok) {
      setBlocked(true);
      if (typeof frameRef === "function") frameRef(null);
      else if (frameRef) frameRef.current = null;
      return;
    }
    setBlocked(false);
    const handleLoad = () => onLoadRef.current();
    iframe.addEventListener("load", handleLoad);
    host.replaceChildren(iframe);
    if (typeof frameRef === "function") frameRef(iframe);
    else if (frameRef) frameRef.current = iframe;
    return () => {
      iframe.removeEventListener("load", handleLoad);
      iframe.remove();
      if (typeof frameRef === "function") frameRef(null);
      else if (frameRef) frameRef.current = null;
    };
  }, [allowedSrc, frameRef, title]);

  if (!decision.ok) {
    return (
      <p role="note" className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
        This preview URL is not an allowed content origin, so the frame stays unloaded.
        Page comments are the supported mode. Precise pins are not invented for it.
      </p>
    );
  }

  return (
    <div className="space-y-2">
      {blocked ? (
        <p role="note" className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
          This browser does not expose a credentialless iframe, so the preview stays unloaded
          instead of requesting the page with cookies.
        </p>
      ) : null}
      <div ref={hostRef} className="overflow-hidden rounded-md border border-border" />
    </div>
  );
}
