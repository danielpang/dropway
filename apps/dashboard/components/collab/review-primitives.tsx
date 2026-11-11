// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Parent-side review primitives. Rendered only when the caller asks and the
// authenticated project collaboration capability is enabled. Nothing in this tree fetches
// a room API or starts an agent. Selections and comments are handed to the
// parent callbacks when those exist.

"use client";

import * as React from "react";

import { AnchorInspector } from "@/components/collab/anchor-inspector";
import { CandidatePreviewFrame } from "@/components/collab/candidate-preview-frame";
import { FeedbackPin } from "@/components/collab/feedback-pin";
import {
  acceptGuestMessage,
  acceptLiveFrameMessage,
  adoptSelection,
  captureImagePoint,
  commentSubmission,
  createFrameSession,
  initialReviewState,
  isCollabReviewEnabled,
  noteFrameLoad,
  previewFrameDecision,
  reduceReview,
  type AdoptedSelection,
  type AnchorRecord,
  type AnchorResolution,
  type AnchorTarget,
  type CollabReviewPort,
  type ReviewEvent,
  type SurfaceKind,
} from "@/lib/collab";

export interface ReviewItem {
  id: string;
  record: AnchorRecord;
  resolution: AnchorResolution;
}

export function CollabReviewPrimitives({
  enabled,
  parentOrigin,
  previewSrc,
  surface,
  previewTarget,
  items,
  onComment,
  onReanchor,
  onSelection,
}: {
  enabled: boolean;
  parentOrigin: string;
  previewSrc: string | null;
  surface: SurfaceKind;
  previewTarget: AnchorTarget;
  items: readonly ReviewItem[];
  onComment?: CollabReviewPort["submitComment"];
  onReanchor?: CollabReviewPort["requestReanchor"];
  onSelection?: (selection: AdoptedSelection) => void;
}) {
  const active = isCollabReviewEnabled({ enabled });
  const [ui, setUi] = React.useState(initialReviewState);
  const panelRef = React.useRef<HTMLDivElement>(null);
  const frameRef = React.useRef<HTMLIFrameElement>(null);
  const [nonce] = React.useState(() => crypto.randomUUID().replace(/-/g, ""));
  const sessionRef = React.useRef(createFrameSession(nonce));

  const dispatch = React.useCallback(
    (event: ReviewEvent) => {
      setUi((prev) => reduceReview(prev, event));
    },
    [],
  );

  React.useEffect(() => {
    if (!active || ui.mode === "idle") return;
    panelRef.current?.focus();
  }, [active, ui.mode, ui.activeId]);

  React.useEffect(() => {
    if (!active || ui.mode === "idle") return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      setUi((prev) =>
        reduceReview(prev, {
          type: "keydown",
          key: "Escape",
          metaKey: event.metaKey,
          ctrlKey: event.ctrlKey,
          target: "inspector",
        }),
      );
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [active, ui.mode]);

  React.useEffect(() => {
    sessionRef.current = createFrameSession(nonce);
  }, [nonce, previewSrc]);

  React.useEffect(() => {
    if (!active || surface !== "instrumented-html") return;
    function onMessage(event: MessageEvent) {
      const frame = frameRef.current?.contentWindow;
      if (!frame || !previewSrc) return;
      const claimedNonce =
        event.data !== null &&
        typeof event.data === "object" &&
        "nonce" in event.data &&
        typeof event.data.nonce === "string"
          ? event.data.nonce
          : "";
      // A sandboxed child can navigate without a second load. Opaque messages
      // are rejected below; this also drops a parent-observed reload.
      if (!acceptLiveFrameMessage(sessionRef.current, claimedNonce).ok) return;
      let previewOrigin = "";
      try {
        previewOrigin = new URL(previewSrc).origin;
      } catch {
        return;
      }
      const source = event.source;
      if (source === null || typeof source !== "object") return;
      const accepted = acceptGuestMessage(
        {
          nonce,
          previewOrigin,
          source: frame,
          parentOrigin,
        },
        { origin: event.origin, source, data: event.data },
        previewTarget,
        surface,
      );
      if (accepted.ok && accepted.type === "selection") onSelection?.(accepted.adopted);
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [active, nonce, onSelection, parentOrigin, previewSrc, previewTarget, surface]);

  if (!active) return null;

  const activeItem = items.find((item) => item.id === ui.activeId) ?? null;
  const draft = ui.activeId ? (ui.drafts[ui.activeId] ?? "") : "";
  const frameDecision = previewSrc ? previewFrameDecision(previewSrc, parentOrigin) : null;

  function publishComment() {
    if (!activeItem) return;
    const next = reduceReview(ui, { type: "submit" });
    if (!next.submitted) {
      setUi(next);
      return;
    }
    const submission = commentSubmission(activeItem.record, next.submitted.body);
    if (submission.ok) {
      onComment?.({
        anchor: submission.anchor,
        original: submission.original,
        body: submission.body,
        triggersExecution: false,
      });
    }
    setUi(next);
  }

  function onImageClick(event: React.MouseEvent<HTMLImageElement>) {
    const img = event.currentTarget;
    const rect = img.getBoundingClientRect();
    const captured = captureImagePoint(
      { clientX: event.clientX, clientY: event.clientY },
      {
        imageId: previewTarget.contentHash,
        left: rect.left,
        top: rect.top,
        width: rect.width,
        height: rect.height,
        naturalWidth: img.naturalWidth,
        naturalHeight: img.naturalHeight,
      },
    );
    if (!captured.ok) return;
    const width = Math.max(1, Math.round(rect.width));
    const height = Math.max(1, Math.round(rect.height));
    onSelection?.(
      adoptSelection({
        surface: "image",
        kind: "point",
        target: previewTarget,
        point: {
          space: "image",
          image: captured.image,
          rendition: {
            viewportWidth: width,
            viewportHeight: height,
            scrollX: 0,
            scrollY: 0,
            documentWidth: width,
            documentHeight: height,
          },
        },
      }),
    );
  }

  function onFrameLoad() {
    sessionRef.current = noteFrameLoad(sessionRef.current);
  }

  return (
    <section aria-label="Version feedback" className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Comments stay on the parent. This preview does not receive credentials or private threads,
        and commenting does not run an agent.
      </p>
      {surface === "image" && frameDecision?.ok ? (
        <div className="relative inline-block max-w-full">
          {/* The image is rendered by the parent so a pin can use intrinsic pixels. */}
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={frameDecision.src}
            alt="Preview image"
            onClick={onImageClick}
            className="max-h-96 max-w-full rounded-md border border-border"
          />
          {items.map((item) => {
            const anchor = item.record.current;
            if (
              item.resolution.status !== "anchored" ||
              anchor.kind !== "point" ||
              anchor.space !== "image" ||
              anchor.image.naturalWidth <= 0 ||
              anchor.image.naturalHeight <= 0
            ) {
              return null;
            }
            return (
              <FeedbackPin
                key={item.id}
                label="Image pin"
                pressed={ui.activeId === item.id && ui.mode !== "idle"}
                onOpen={() => dispatch({ type: "open", anchorId: item.id })}
                className="absolute -translate-x-1/2 -translate-y-1/2"
                style={{
                  left: `${(anchor.image.x / anchor.image.naturalWidth) * 100}%`,
                  top: `${(anchor.image.y / anchor.image.naturalHeight) * 100}%`,
                }}
              />
            );
          })}
        </div>
      ) : null}
      {surface !== "image" && previewSrc ? (
        <CandidatePreviewFrame
          src={previewSrc}
          parentOrigin={parentOrigin}
          title="Candidate preview"
          frameRef={frameRef}
          onLoad={onFrameLoad}
        />
      ) : null}
      {surface === "html" || surface === "unknown" ? (
        <p className="text-sm text-muted-foreground">
          This page is not instrumented. Text, component, and point anchors are unavailable.
          Comments attach to the whole page on the original version.
        </p>
      ) : null}
      {surface === "instrumented-html" ? (
        <p className="text-sm text-muted-foreground">
          Precise selections stay off in this sandboxed preview. Its origin is opaque, a
          navigated document is not visible on the frame src, and no handshake is sent into
          the frame. Page comments stay on this side.
        </p>
      ) : null}
      <ul className="space-y-2">
        {items.map((item) => (
          <li key={item.id}>
            <button
              type="button"
              className="w-full rounded-md border border-border px-3 py-2 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              aria-pressed={ui.activeId === item.id && ui.mode !== "idle"}
              onClick={() => dispatch({ type: "open", anchorId: item.id })}
            >
              <span className="font-medium capitalize text-foreground">{item.record.current.kind}</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">
                {item.resolution.status}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {activeItem && ui.mode !== "idle" ? (
        <AnchorInspector
          panelRef={panelRef}
          record={activeItem.record}
          resolution={activeItem.resolution}
          draft={draft}
          reanchorConnected={Boolean(onReanchor)}
          onDraftChange={(value) => dispatch({ type: "edit", value })}
          onSubmit={publishComment}
          onDraftKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
              event.preventDefault();
              publishComment();
            }
          }}
          onReanchor={() => onReanchor?.(activeItem.record)}
        />
      ) : null}
      {onComment ? null : (
        <p className="text-xs text-muted-foreground">
          A comment draft is not saved. Room storage waits on the backend contract.
        </p>
      )}
    </section>
  );
}
