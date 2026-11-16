"use client";
import { useCallback, useState } from "react";
import { CandidateReview } from "@/components/project-room/candidate-review";
import type { ReviewCandidate } from "@/lib/project-room/contract";
import type { loadCandidatePreviewAction } from "@/app/(app)/rooms/preview-actions";
type Preview = Extract<
  Awaited<ReturnType<typeof loadCandidatePreviewAction>>,
  { ok: true }
>["preview"];
export function ProposalDemo({
  candidate: initial,
  preview,
}: {
  candidate: ReviewCandidate;
  preview: Preview;
}) {
  const [candidate, setCandidate] = useState(initial);
  const loader = useCallback(
    async () => ({ ok: true as const, preview }),
    [preview],
  );
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-8">
      <header>
        <h1 className="text-xl font-medium">Proposed changes</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Compare both versions. Accept to publish, or reject to keep the
          current version.
        </p>
      </header>
      <CandidateReview
        candidate={candidate}
        live={{
          versionId: candidate.AppliedVersionID ?? "v1",
          contentHash: candidate.AppliedVersionID ? "new" : "base",
        }}
        diff={{
          status: "verified",
          diff: {
            candidateId: "demo",
            siteId: "site",
            baseVersionId: "v1",
            baseContentHash: "base",
            contentHash: "new",
            verified: true,
            truncated: false,
            omittedFiles: 0,
            files: [],
          },
        }}
        canDecide
        onDecide={(decision) =>
          setCandidate({
            ...candidate,
            State: decision === "approve" ? "approved" : "rejected",
            AppliedVersionID: decision === "approve" ? "v2" : null,
          })
        }
        previewLoader={loader}
      />
    </main>
  );
}
