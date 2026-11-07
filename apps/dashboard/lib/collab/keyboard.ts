// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Keyboard state for inspection. Escape leaves inspection without submitting.
// Arrow keys do not move a pin. A comment submits only from a nonblank draft
// and the result cannot request an agent run.

export type ReviewMode = "idle" | "inspecting" | "drafting";

export interface SubmittedComment {
  anchorId: string;
  body: string;
  triggersExecution: false;
}

export interface ReviewSessionState {
  activeId: string | null;
  mode: ReviewMode;
  drafts: Readonly<Record<string, string>>;
  submitted: SubmittedComment | null;
}

export type ReviewEvent =
  | { type: "open"; anchorId: string }
  | { type: "edit"; value: string }
  | { type: "submit" }
  | {
      type: "keydown";
      key: string;
      metaKey: boolean;
      ctrlKey: boolean;
      target: "marker" | "draft" | "inspector" | "other";
      anchorId?: string;
    };

export function initialReviewState(): ReviewSessionState {
  return { activeId: null, mode: "idle", drafts: {}, submitted: null };
}

function open(state: ReviewSessionState, anchorId: string): ReviewSessionState {
  return { ...state, activeId: anchorId, mode: "inspecting", submitted: null };
}

function submit(state: ReviewSessionState): ReviewSessionState {
  if (!state.activeId || (state.mode !== "drafting" && state.mode !== "inspecting")) return state;
  const raw = state.drafts[state.activeId] ?? "";
  const body = raw.trim();
  if (!body) return state;
  const drafts: Record<string, string> = { ...state.drafts };
  delete drafts[state.activeId];
  return {
    activeId: null,
    mode: "idle",
    drafts,
    submitted: { anchorId: state.activeId, body, triggersExecution: false },
  };
}

function onKey(state: ReviewSessionState, event: Extract<ReviewEvent, { type: "keydown" }>): ReviewSessionState {
  if (event.key === "Escape" && (state.mode === "inspecting" || state.mode === "drafting")) {
    return { ...state, mode: "idle", activeId: null };
  }
  if (
    event.key === "Enter" &&
    event.target === "marker" &&
    event.anchorId &&
    !event.metaKey &&
    !event.ctrlKey
  ) {
    return open(state, event.anchorId);
  }
  if (event.key === "Enter" && event.target === "draft" && (event.metaKey || event.ctrlKey)) {
    return submit(state);
  }
  return state;
}

export function reduceReview(state: ReviewSessionState, event: ReviewEvent): ReviewSessionState {
  switch (event.type) {
    case "open":
      return open(state, event.anchorId);
    case "edit": {
      if (!state.activeId) return state;
      return {
        ...state,
        mode: "drafting",
        drafts: { ...state.drafts, [state.activeId]: event.value },
      };
    }
    case "submit":
      return submit(state);
    case "keydown":
      return onKey(state, event);
    default: {
      const _never: never = event;
      return _never;
    }
  }
}
