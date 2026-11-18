"use server";
import { api } from "@/lib/api";
import { collaborationEnabled } from "@/lib/project-room/capabilities";
import {
  renderComparisonPage,
} from "@/lib/project-room/visual-preview";
import type { ReviewCandidate } from "@/lib/project-room/contract";

export async function loadCandidatePreviewAction(
  candidate: ReviewCandidate,
  selectedPath?: string,
) {
  if (!(await collaborationEnabled()))
    return { ok: false as const, message: "Project access is unavailable." };
  try {
    const data = await api.getCandidatePreview(candidate.RoomID, candidate.ID);
    const preview = renderComparisonPage(data, candidate, selectedPath);
    if (!preview)
      return {
        ok: false as const,
        message: "This change has no page or image to preview.",
      };
    return {
      ok: true as const,
      preview,
    };
  } catch {
    return {
      ok: false as const,
      message:
        "We couldn’t load this comparison. Try again before accepting the change.",
    };
  }
}
