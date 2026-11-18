// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// One proposed change, with immutable before/after previews. Accept approves
// and publishes the exact candidate binding; the API still verifies that the
// live baseline has not changed before publication.

"use client";

import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import { VisualComparison } from "./visual-comparison";
import { Check, X, GitPullRequest } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  CANNOT_VERIFY,
  NEEDS_NEW_CANDIDATE,
  bindApproval,
  compareLive,
  diffMatchesCandidate,
  terminalCopy,
  untrustedText,
  type CandidateDiffView,
  type LiveBaseline,
  type ReviewCandidate,
} from "@/lib/project-room/contract";

export function CandidateReview({
  candidate,
  live,
  diff,
  canDecide,
  onDecide,
  onApply,
  busy = false,
  previewLoader,
  initiallyOpen = true,
  onRequestDiff,
}: {
  candidate: ReviewCandidate;
  live: LiveBaseline | null;
  diff: CandidateDiffView;
  canDecide: boolean;
  onDecide: (decision: "approve" | "reject") => void;
  onApply?: () => void;
  busy?: boolean;
  initiallyOpen?: boolean;
  onRequestDiff?: (candidate: ReviewCandidate) => void;
  previewLoader?: Parameters<typeof VisualComparison>[0]["loader"];
}) {
  const [expanded, setExpanded] = useState(false);
  const [previewReady, setPreviewReady] = useState(false);
  const [comparisonOpen, setComparisonOpen] = useState(initiallyOpen);
  useEffect(() => {
    if (comparisonOpen && diff.status === "loading") onRequestDiff?.(candidate);
  }, [comparisonOpen, candidate, diff.status, onRequestDiff]);
  const binding = bindApproval(candidate);
  const liveCheck = compareLive(candidate, live);
  const verified =
    diff.status === "verified" && diffMatchesCandidate(diff.diff, candidate)
      ? diff.diff
      : null;
  const unverified =
    diff.status === "unverified" ||
    (diff.status === "verified" && verified === null);
  const applied = Boolean(candidate.AppliedVersionID);
  const stale = !applied && liveCheck.status === "stale";
  const roleBlocked = !canDecide || binding === null;
  const approveDisabled =
    applied ||
    roleBlocked ||
    busy ||
    !["pending", "approved"].includes(candidate.State) ||
    liveCheck.status !== "matches" ||
    !comparisonOpen ||
    !previewReady ||
    (candidate.State === "approved" && !onApply) ||
    verified === null;
  const rejectDisabled =
    applied || roleBlocked || busy || candidate.State !== "pending";
  const terminal = applied
    ? "This change is published."
    : candidate.State === "approved"
      ? "Publishing hasn’t finished. Retry to publish this accepted change."
      : terminalCopy(candidate.State);
  return (
    <article
      className="space-y-4 overflow-hidden rounded-xl border border-border bg-background p-5"
      aria-label={`Proposed change ${candidate.ID}`}
      aria-busy={busy}
    >
      <header className="flex items-start justify-between gap-4">
        <div className="flex min-w-0 items-start gap-3">
          <span className="rounded-lg bg-muted p-2.5 text-muted-foreground">
            <GitPullRequest className="h-4 w-4" aria-hidden="true" />
          </span>
          <div className="min-w-0 space-y-1.5">
            <h3 className="text-base font-medium">Review proposed change</h3>
            <div
              className={`${!expanded && candidate.Summary.length > 420 ? "line-clamp-3" : ""} space-y-2 text-sm font-normal leading-6 text-muted-foreground [&_strong]:font-medium [&_strong]:text-foreground [&_p]:whitespace-pre-wrap [&_ul]:list-disc [&_ul]:pl-5 [&_li]:my-1 [&_code]:break-words`}
            >
              <ReactMarkdown
                skipHtml
                allowedElements={[
                  "p",
                  "strong",
                  "em",
                  "ul",
                  "ol",
                  "li",
                  "code",
                  "br",
                ]}
              >
                {untrustedText(candidate.Summary) ||
                  "Compare the current version with the proposed change below."}
              </ReactMarkdown>
            </div>
            {candidate.Summary.length > 420 ? (
              <button
                type="button"
                className="text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground"
                onClick={() => setExpanded((v) => !v)}
              >
                {expanded ? "Show less" : "Read full summary"}
              </button>
            ) : null}
          </div>
        </div>
        <span
          className={`shrink-0 rounded-md px-2 py-1 text-xs capitalize ${applied || candidate.State === "approved" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : candidate.State === "pending" ? "bg-amber-500/10 text-amber-600 dark:text-amber-400" : "bg-muted text-muted-foreground"}`}
        >
          {applied ? "Published" : candidate.State}
        </span>
      </header>
      <p className="text-xs text-muted-foreground" role="status">
        {terminal ?? "Accepting publishes this change."}
      </p>
      {stale ? (
        <p className="text-sm font-medium text-foreground" role="status">
          {NEEDS_NEW_CANDIDATE}
        </p>
      ) : !applied && liveCheck.status === "unknown" ? (
        <p className="text-sm text-muted-foreground" role="status">
          {liveCheck.reason}
        </p>
      ) : null}
      {!comparisonOpen ? (
        <Button variant="outline" onClick={() => setComparisonOpen(true)}>View comparison</Button>
      ) : unverified ? (
        <p className="text-sm font-medium text-foreground" role="status">
          {CANNOT_VERIFY}
        </p>
      ) : diff.status === "loading" ? (
        <p className="text-sm text-muted-foreground">Checking this change…</p>
      ) : diff.status === "unavailable" ? (
        <p className="text-sm text-muted-foreground">
          This change could not be loaded. {onRequestDiff && <button className="underline" onClick={() => onRequestDiff(candidate)}>Retry comparison</button>}
        </p>
      ) : verified ? (
        <VisualComparison
          key={`${candidate.ID}:${candidate.ContentHash}`}
          candidate={candidate}
          onReady={setPreviewReady}
          loader={previewLoader}
        />
      ) : null}
      <div className="flex flex-wrap items-center gap-2 border-t border-border pt-4">
        <Button
          type="button"
          disabled={approveDisabled}
          onClick={() =>
            candidate.State === "approved" ? onApply?.() : onDecide("approve")
          }
        >
          <Check className="h-4 w-4" aria-hidden="true" />{" "}
          {busy
            ? "Publishing…"
            : candidate.State === "approved" && !applied
              ? "Retry publishing"
              : "Accept change"}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={rejectDisabled}
          onClick={() => onDecide("reject")}
        >
          <X className="h-4 w-4" aria-hidden="true" /> Reject change
        </Button>
      </div>
      {!applied && candidate.State !== "rejected" ? (
        <p className="text-xs text-muted-foreground">
          Accept change publishes the proposed version. Reject keeps
          the current version.
        </p>
      ) : null}
    </article>
  );
}
