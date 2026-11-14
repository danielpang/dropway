// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Inspection is keyboard reachable. Escape leaves it without submitting or
// discarding the draft. A comment submits only when the draft is nonblank and
// never carries an execution flag. Pins are feedback controls, not source editors.

import { describe, expect, it } from "vitest";

import {
  commentControls,
  commentSubmission,
  createAnchorRecord,
  feedbackPinA11y,
  initialReviewState,
  reduceReview,
} from "@/lib/collab";

describe("reduceReview", () => {
  it("opens inspection from the keyboard and ignores arrows", () => {
    const opened = reduceReview(initialReviewState(), {
      type: "keydown",
      key: "Enter",
      metaKey: false,
      ctrlKey: false,
      target: "marker",
      anchorId: "pin-1",
    });
    expect(opened.mode).toBe("inspecting");
    expect(opened.activeId).toBe("pin-1");
    const nudged = reduceReview(opened, {
      type: "keydown",
      key: "ArrowRight",
      metaKey: false,
      ctrlKey: false,
      target: "inspector",
    });
    expect(nudged).toEqual(opened);
  });

  it("leaves inspection on Escape and keeps the unsent draft", () => {
    let state = reduceReview(initialReviewState(), { type: "open", anchorId: "pin-1" });
    state = reduceReview(state, { type: "edit", value: "  looks good  " });
    expect(state.mode).toBe("drafting");
    state = reduceReview(state, {
      type: "keydown",
      key: "Escape",
      metaKey: false,
      ctrlKey: false,
      target: "draft",
    });
    expect(state.mode).toBe("idle");
    expect(state.activeId).toBeNull();
    expect(state.drafts["pin-1"]).toBe("  looks good  ");
    expect(state.submitted).toBeNull();
  });

  it("does not submit a blank or whitespace draft", () => {
    const drafting = reduceReview(
      { ...initialReviewState(), activeId: "pin-1", mode: "drafting", drafts: { "pin-1": "  \n" } },
      { type: "submit" },
    );
    expect(drafting.submitted).toBeNull();
    expect(drafting.mode).toBe("drafting");
    const fromKey = reduceReview(drafting, {
      type: "keydown",
      key: "Enter",
      metaKey: true,
      ctrlKey: false,
      target: "draft",
    });
    expect(fromKey.submitted).toBeNull();
  });

  it("submits a trimmed comment without triggering execution", () => {
    const state = reduceReview(
      {
        ...initialReviewState(),
        activeId: "pin-1",
        mode: "drafting",
        drafts: { "pin-1": "  Ship the caption.  " },
      },
      { type: "keydown", key: "Enter", metaKey: false, ctrlKey: true, target: "draft" },
    );
    expect(state.submitted).toEqual({
      anchorId: "pin-1",
      body: "Ship the caption.",
      triggersExecution: false,
    });
    expect(state.mode).toBe("idle");
    expect(state.drafts["pin-1"]).toBeUndefined();
    expect(state).not.toHaveProperty("run");
  });

  it("does not submit on Enter inside the draft without a modifier", () => {
    const drafting = {
      ...initialReviewState(),
      activeId: "pin-1",
      mode: "drafting" as const,
      drafts: { "pin-1": "Keep writing" },
    };
    expect(
      reduceReview(drafting, {
        type: "keydown",
        key: "Enter",
        metaKey: false,
        ctrlKey: false,
        target: "draft",
      }),
    ).toEqual(drafting);
  });
});

describe("commentSubmission", () => {
  const record = createAnchorRecord({
    kind: "page",
    siteId: "site-1",
    versionId: "ver-1",
    contentHash: "hash-1",
    page: "/index.html",
  });

  it("attaches a nonblank comment to the anchor and its original version", () => {
    const submitted = commentSubmission(record, "  On this version  ");
    expect(submitted).toEqual({
      ok: true,
      body: "On this version",
      anchor: record.current,
      original: record.origin,
      triggersExecution: false,
    });
  });

  it("rejects a blank draft and does not require an agent configuration", () => {
    expect(commentSubmission(record, "   ")).toEqual({ ok: false, reason: "blank" });
    expect(commentControls()).toEqual(["comment"]);
    expect(commentControls()).not.toContain("run");
    expect(commentControls()).not.toContain("accept");
  });
});

describe("feedbackPinA11y", () => {
  it("exposes a button that is feedback rather than editable source", () => {
    expect(feedbackPinA11y("Point on the hero")).toEqual({
      type: "button",
      contentEditable: "false",
      "aria-label": "Feedback pin, point on the hero",
      "data-feedback-only": "true",
    });
  });
});
