"use client";

import * as React from "react";
import { TextSelect, Image, BoxSelect, MapPin, File, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  captureStaticPoint,
  captureStaticText,
  staticAnnotationRects,
  type StaticAnnotation,
} from "@/lib/collab/static-selection";
import type {
  StaticSelection,
  StaticSnapshot,
} from "@/lib/collab/static-snapshot";

type SelectionMode = "text" | "image" | "component" | "point" | "page";

export function StaticReview({
  snapshot,
  onSelect,
  readOnly = false,
  annotations = [],
  activeThreadId,
  onThreadSelect,
  selectionResetKey,
  imageCanvas = false,
  renderThread,
  renderDraft,
}: {
  renderThread?: (id: string) => React.ReactNode;
  renderDraft?: () => React.ReactNode;
  readOnly?: boolean;
  imageCanvas?: boolean;
  snapshot: StaticSnapshot;
  onSelect: (selection: StaticSelection | null) => void;
  /** Only annotations matching this snapshot's exact site/version/hash/page. */
  annotations?: StaticAnnotation[];
  activeThreadId?: string;
  onThreadSelect?: (id: string) => void;
  /** Change after posting or cancelling a comment to clear the draft marker. */
  selectionResetKey?: string | number;
}) {
  const host = React.useRef<HTMLDivElement>(null);
  const [popover, setPopover] = React.useState<{
    id: string;
    left: number;
    top: number;
  } | null>(null);
  const popoverId = React.useRef<string | null>(null);
  const closePopover = React.useCallback(() => {
    popoverId.current = null;
    setPopover(null);
  }, []);
  const closeButton = React.useRef<HTMLButtonElement>(null);
  const popoverContent = React.useRef<HTMLDivElement>(null);
  React.useEffect(() => {
    if (popover?.id === "pending-selection")
      popoverContent.current?.querySelector("textarea")?.focus();
    else if (popover?.id) closeButton.current?.focus();
  }, [popover?.id]);
  const [mode, setMode] = React.useState<SelectionMode>(
    imageCanvas ? "point" : "text",
  );
  React.useEffect(() => {
    setMode(imageCanvas ? "point" : "text");
  }, [imageCanvas, snapshot.siteId]);
  const modeRef = React.useRef(mode);
  const readOnlyRef = React.useRef(readOnly);
  const onSelectRef = React.useRef(onSelect);
  const annotationsRef = React.useRef(annotations);
  const activeRef = React.useRef(activeThreadId);
  const threadSelectRef = React.useRef(onThreadSelect);
  const redraw = React.useRef<(() => void) | null>(null);
  const revealActive = React.useRef<(() => void) | null>(null);
  const clearFrameSelection = React.useRef<(() => void) | null>(null);
  const pendingSelection = React.useRef<StaticSelection | null>(null);
  readOnlyRef.current = readOnly;
  modeRef.current = mode;
  onSelectRef.current = onSelect;
  annotationsRef.current = annotations;
  activeRef.current = activeThreadId;
  threadSelectRef.current = onThreadSelect;
  const [blocked, setBlocked] = React.useState(false);
  const [selected, setSelected] = React.useState("");

  React.useEffect(() => {
    redraw.current?.();
  }, [annotations, activeThreadId, readOnly]);
  React.useEffect(() => {
    revealActive.current?.();
  }, [activeThreadId]);
  React.useEffect(() => {
    clearFrameSelection.current?.();
  }, [mode, readOnly]);
  React.useEffect(() => {
    clearFrameSelection.current?.();
  }, [selectionResetKey]);

  React.useEffect(() => {
    const container = host.current;
    if (!container) return;
    setBlocked(false);
    setSelected("");
    popoverId.current = null;
    setPopover(null);
    pendingSelection.current = null;
    onSelectRef.current(null);
    const frame = document.createElement("iframe");
    frame.title = "Immutable static review";
    frame.className = "h-full min-h-0 w-full border-0 bg-white";
    // Only the trusted, reconstructed document gets same-origin access. Source
    // scripts, original styles, navigation and external resources are removed.
    frame.setAttribute("sandbox", "allow-same-origin");
    frame.referrerPolicy = "no-referrer";
    let loaded = false;
    let cleanup: () => void = () => undefined;
    function retire() {
      cleanup();
      frame.remove();
      setBlocked(true);
      onSelectRef.current(null);
    }
    const onLoad = () => {
      if (loaded) {
        retire();
        return;
      }
      loaded = true;
      const doc = frame.contentDocument;
      if (
        !doc ||
        doc.URL !== "about:srcdoc" ||
        doc.documentElement.dataset.reviewSnapshot !== snapshot.snapshotId
      ) {
        retire();
        return;
      }
      const win = doc.defaultView;
      if (!win) {
        retire();
        return;
      }
      const overlay = doc.createElement("div");
      overlay.setAttribute("aria-label", "Comment annotations");
      overlay.style.cssText =
        "position:fixed;inset:0;pointer-events:none;z-index:100;overflow:hidden";
      doc.body.appendChild(overlay);
      let scheduled = 0;
      let hitRegions: { id: string; rects: DOMRect[] }[] = [];
      const positionThread = (id: string, rect: DOMRect) => {
        if (rect.bottom < 0 || rect.top > win.innerHeight) {
          setPopover(null);
          return;
        }
        setPopover({
          id,
          left: Math.max(8, Math.min(win.innerWidth - 328, rect.right + 12)),
          top: Math.max(8, Math.min(win.innerHeight - 180, rect.top)),
        });
      };
      const openThread = (id: string, rect: DOMRect) => {
        popoverId.current = id;
        positionThread(id, rect);
        if (id !== "pending-selection") threadSelectRef.current?.(id);
      };
      const paint = () => {
        scheduled = 0;
        overlay.replaceChildren();
        hitRegions = [];
        if (readOnlyRef.current) return;
        const visible: (StaticAnnotation & { pending?: boolean })[] = [
          ...annotationsRef.current,
        ];
        if (pendingSelection.current)
          visible.push({
            id: "pending-selection",
            kind: pendingSelection.current.kind,
            anchor: pendingSelection.current.anchor,
            label: "New comment",
            pending: true,
          });
        if (
          popoverId.current &&
          !visible.some((item) => item.id === popoverId.current)
        ) {
          popoverId.current = null;
          setPopover(null);
        }
        visible.forEach((annotation, index) => {
          const rects = staticAnnotationRects(doc, annotation).filter(
            (rect) => rect.width > 0 && rect.height > 0,
          );
          if (!rects.length) {
            if (popoverId.current === annotation.id) setPopover(null);
            return;
          }
          if (popoverId.current === annotation.id)
            positionThread(annotation.id, rects[0]!);
          if (!annotation.pending)
            hitRegions.push({ id: annotation.id, rects });
          const active = activeRef.current === annotation.id;
          for (const rect of rects) {
            const highlight = doc.createElement("span");
            highlight.style.cssText = `position:absolute;left:${rect.left}px;top:${rect.top}px;width:${rect.width}px;height:${rect.height}px;background:${annotation.pending ? "rgba(59,130,246,.20)" : active ? "rgba(245,158,11,.32)" : "rgba(250,204,21,.20)"};outline:${annotation.pending ? "2px solid #2563eb" : active ? "2px solid #d97706" : "1px solid rgba(217,119,6,.35)"};border-radius:2px`;
            overlay.appendChild(highlight);
          }
          const first = rects[0]!;
          if (first.bottom < 0 || first.top > win.innerHeight) return;
          const marker = doc.createElement("button");
          marker.type = "button";
          marker.textContent = annotation.pending ? "+" : String(index + 1);
          marker.setAttribute(
            "aria-label",
            annotation.pending
              ? "New comment location"
              : `Open comment ${index + 1}${annotation.label ? `: ${annotation.label.slice(0, 120)}` : ""}`,
          );
          marker.setAttribute("aria-pressed", String(active));
          marker.style.cssText = `position:absolute;left:${Math.max(0, Math.min(win.innerWidth - 28, first.right - 12))}px;top:${Math.max(0, first.top - 12)}px;pointer-events:auto;width:26px;height:26px;border:2px solid white;border-radius:50%;background:${annotation.pending ? "#2563eb" : active ? "#92400e" : "#d97706"};color:white;font:600 12px system-ui;cursor:pointer;box-shadow:0 1px 4px #0003`;
          marker.addEventListener("click", (event) => {
            event.preventDefault();
            event.stopPropagation();
            openThread(annotation.id, first);
          });
          overlay.appendChild(marker);
        });
      };
      const schedulePaint = () => {
        if (!scheduled) scheduled = win.requestAnimationFrame(paint);
      };
      redraw.current = schedulePaint;
      revealActive.current = () => {
        if (readOnlyRef.current) return;
        const annotation = annotationsRef.current.find(
          (item) => item.id === activeRef.current,
        );
        const rect = annotation
          ? staticAnnotationRects(doc, annotation)[0]
          : null;
        if (rect && (rect.top < 40 || rect.bottom > win.innerHeight - 40))
          win.scrollBy({ top: rect.top - win.innerHeight / 3 });
      };
      paint();
      const observer = new ResizeObserver(schedulePaint);
      if (doc.getElementById("review-content"))
        observer.observe(doc.getElementById("review-content")!);
      win.addEventListener("scroll", schedulePaint);
      win.addEventListener("resize", schedulePaint);
      doc.addEventListener("load", schedulePaint, true);
      const clearSelection = () => {
        closePopover();
        doc
          .querySelectorAll(".dw-selected")
          .forEach((item) => item.classList.remove("dw-selected"));
        doc.getSelection()?.removeAllRanges();
        pendingSelection.current = null;
        schedulePaint();
        setSelected("");
        onSelectRef.current(null);
      };
      clearFrameSelection.current = clearSelection;
      const adopt = (selection: StaticSelection | null) => {
        if (!selection || readOnlyRef.current) return;
        pendingSelection.current = selection;
        schedulePaint();
        setSelected(selection.label);
        onSelectRef.current(selection);
        const rect = staticAnnotationRects(doc, {
          id: "pending-selection",
          kind: selection.kind,
          anchor: selection.anchor,
        })[0];
        if (rect) openThread("pending-selection", rect);
      };
      const onClick = (event: MouseEvent) => {
        if (overlay.contains(event.target as Node)) return;
        event.preventDefault();
        if (readOnlyRef.current) return;
        if (modeRef.current === "text" && !doc.getSelection()?.isCollapsed)
          return;
        if (doc.getSelection()?.isCollapsed) {
          const hit = hitRegions.find(({ rects }) =>
            rects.some(
              (rect) =>
                event.clientX >= rect.left &&
                event.clientX <= rect.right &&
                event.clientY >= rect.top &&
                event.clientY <= rect.bottom,
            ),
          );
          if (hit) {
            openThread(hit.id, hit.rects[0]!);
            return;
          }
        }
        closePopover();
        const target =
          (event.target as Node | null)?.nodeType === 1
            ? (event.target as Element)
            : null;
        if (modeRef.current === "point") adopt(captureStaticPoint(doc, event));
        if (modeRef.current === "component" || modeRef.current === "image") {
          const component =
            modeRef.current === "image"
              ? target?.closest("img[data-review-component]")
              : target?.closest("[data-review-component]");
          const id = component?.getAttribute("data-review-component");
          if (!id) return;
          doc
            .querySelectorAll(".dw-selected")
            .forEach((item) => item.classList.remove("dw-selected"));
          component!.classList.add("dw-selected");
          adopt({
            kind: "component",
            label:
              modeRef.current === "image"
                ? "Selected image"
                : `Selected ${component!.tagName.toLowerCase()}`,
            anchor: { stable_id: id },
          });
        }
      };
      const text = () => {
        if (modeRef.current === "text") adopt(captureStaticText(doc));
      };
      const key = (event: KeyboardEvent) => {
        if (event.key === "Escape") {
          closePopover();
          clearSelection();
          setMode("text");
        }
        if (
          event.key === "Enter" &&
          ["component", "image"].includes(modeRef.current) &&
          (event.target as Node | null)?.nodeType === 1
        )
          (event.target as HTMLElement).click();
        if (modeRef.current === "text") text();
      };
      doc
        .querySelectorAll("[data-review-component]")
        .forEach((item) => item.setAttribute("tabindex", "0"));
      doc.addEventListener("click", onClick);
      doc.addEventListener("mouseup", text);
      doc.addEventListener("keyup", key);
      cleanup = () => {
        redraw.current = null;
        revealActive.current = null;
        clearFrameSelection.current = null;
        if (scheduled) win.cancelAnimationFrame(scheduled);
        observer.disconnect();
        overlay.remove();
        win.removeEventListener("scroll", schedulePaint);
        win.removeEventListener("resize", schedulePaint);
        doc.removeEventListener("load", schedulePaint, true);
        doc.removeEventListener("click", onClick);
        doc.removeEventListener("mouseup", text);
        doc.removeEventListener("keyup", key);
      };
    };
    frame.addEventListener("load", onLoad);
    frame.srcdoc = snapshot.html;
    container.replaceChildren(frame);
    return () => {
      cleanup();
      frame.removeEventListener("load", onLoad);
      frame.remove();
    };
  }, [snapshot, closePopover]);

  return (
    <section
      className="flex h-full min-h-0 flex-col"
      aria-label="Static review selection"
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          closePopover();
          setMode("text");
          setSelected("");
          onSelect(null);
        }
      }}
    >
      {!readOnly ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-background px-4 py-2">
          <span className="mr-1 text-xs font-medium text-muted-foreground">
            Comment on
          </span>
          {(imageCanvas
            ? (["point", "image", "page"] as const)
            : (["text", "image", "component", "point", "page"] as const)
          ).map((value) => (
            <Button
              key={value}
              type="button"
              size="sm"
              variant={mode === value ? "secondary" : "ghost"}
              disabled={blocked}
              aria-label={
                {
                  text: "Text",
                  image: "Image",
                  component: "Element",
                  point: "Point",
                  page: "Page",
                }[value]
              }
              title={
                {
                  text: "Comment on text",
                  image: "Comment on an image",
                  component: "Comment on an element",
                  point: "Pin a comment",
                  page: "Comment on the page",
                }[value]
              }
              className="h-8 w-8 p-0"
              aria-pressed={mode === value}
              onClick={() => {
                setMode(value);
                setSelected("");
                onSelect(null);
              }}
            >
              {React.createElement(
                {
                  text: TextSelect,
                  image: Image,
                  component: BoxSelect,
                  point: MapPin,
                  page: File,
                }[value],
                { className: "h-4 w-4", "aria-hidden": true },
              )}
            </Button>
          ))}
          <span
            role="status"
            className="basis-full truncate text-xs text-muted-foreground sm:basis-auto sm:max-w-xs"
          >
            {selected ||
              (mode === "text"
                ? "Select text to leave a comment"
                : mode === "image"
                  ? "Click an image"
                  : mode === "component"
                    ? "Click an element"
                    : mode === "point"
                      ? "Click a point on the page"
                      : "Comment on the whole page")}
          </span>
        </div>
      ) : null}
      {blocked ? (
        <p role="alert" className="p-4 text-sm text-destructive">
          The preview could not be verified. Reload the page to try again.
        </p>
      ) : null}
      {snapshot.omittedAssets ? (
        <p className="px-4 py-1 text-xs text-muted-foreground">
          {snapshot.omittedAssets} images unavailable in this preview.
        </p>
      ) : null}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        <div ref={host} className="h-full min-h-0" />
        {popover &&
        (popover.id === "pending-selection" ? renderDraft : renderThread) &&
        !readOnly &&
        !blocked ? (
          <div
            role="dialog"
            aria-label={
              popover.id === "pending-selection"
                ? "New comment"
                : "Comment thread"
            }
            className="absolute z-20 flex w-80 max-w-[calc(100%-1rem)] flex-col rounded-lg border border-border bg-background shadow-xl"
            style={{
              left: popover.left,
              top: popover.top,
              maxHeight: `calc(100% - ${popover.top + 8}px)`,
            }}
          >
            <div className="flex shrink-0 items-center justify-between px-3 py-2">
              <span className="text-xs font-medium">
                {popover.id === "pending-selection" ? "New comment" : "Comment"}
              </span>
              <button
                ref={closeButton}
                type="button"
                aria-label="Close inline comment"
                title="Close comment"
                onClick={() => {
                  if (popover.id === "pending-selection")
                    clearFrameSelection.current?.();
                  else closePopover();
                }}
                className="rounded p-1 hover:bg-muted"
              >
                <X className="h-4 w-4" aria-hidden="true" />
              </button>
            </div>
            <div
              ref={popoverContent}
              className="min-h-0 overflow-y-auto px-2 pb-2"
            >
              {popover.id === "pending-selection"
                ? renderDraft?.()
                : renderThread?.(popover.id)}
            </div>
          </div>
        ) : null}
      </div>
    </section>
  );
}
