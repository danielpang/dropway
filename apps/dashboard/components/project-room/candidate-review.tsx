// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// One proposed change. Approve and Reject stay visible. The request binding
// is the candidate the API returned. Apply stays disabled. A page preview,
// when present, is not this candidate's identity.

"use client";

import { Button } from "@/components/ui/button";
import {
  DIFF_UNAVAILABLE,
  bindApproval,
  compareLive,
  terminalCopy,
  untrustedText,
  type LiveBaseline,
  type ReviewCandidate,
} from "@/lib/project-room/contract";

export function CandidateReview({
  candidate,
  live,
  canDecide,
  onDecide,
}: {
  candidate: ReviewCandidate;
  live: LiveBaseline | null;
  canDecide: boolean;
  onDecide: (decision: "approve" | "reject") => void;
}) {
  const binding = bindApproval(candidate);
  const liveCheck = compareLive(candidate, live);
  const terminal = terminalCopy(candidate.State);
  const blocked = !canDecide || binding === null;
  return (
    <article className="space-y-3 rounded-md border border-border p-4">
      <div className="space-y-1">
        <h3 className="text-sm font-medium text-foreground">Proposed change</h3>
        <p className="text-sm font-medium text-foreground">Diff unavailable</p>
        <p className="text-sm text-muted-foreground">{DIFF_UNAVAILABLE}</p>
        <p className="text-sm text-foreground">{untrustedText(candidate.Summary) || "No summary was returned."}</p>
        <p className="text-sm text-muted-foreground">
          Where: site {candidate.SiteID}, base version {candidate.BaseVersionID}. Candidate {candidate.ID}.
        </p>
        <p className="text-sm text-muted-foreground">
          Identity: content hash {candidate.ContentHash}, base hash {candidate.BaseContentHash}, diff hash {candidate.DiffHash}.
        </p>
      </div>
      <p className="text-sm text-foreground" role="status">
        State: {candidate.State}. {terminal ?? "Pending. Approval does not publish the site."}
      </p>
      {liveCheck.status === "matches" ? null : (
        <p className="text-sm text-muted-foreground" role="status">
          {liveCheck.reason} Sending a decision still uses this candidate&apos;s base version and hash,
          not a newer baseline.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button type="button" disabled={blocked} onClick={() => onDecide("approve")}>
          Approve
        </Button>
        <Button type="button" variant="outline" disabled={blocked} onClick={() => onDecide("reject")}>
          Reject
        </Button>
        <Button type="button" variant="secondary" disabled>
          Apply
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Apply stays disabled. Candidate application is not enabled.
        {blocked ? " Approve and Reject stay visible and do not send while this role or the binding is incomplete." : ""}
      </p>
      <details className="text-xs text-muted-foreground">
        <summary>Hashes and the exact approval binding</summary>
        <pre className="mt-2 overflow-x-auto whitespace-pre-wrap">
          {JSON.stringify(binding, null, 2)}
        </pre>
      </details>
    </article>
  );
}
