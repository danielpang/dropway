// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// One proposed change. Approve and Reject stay visible. The request binding
// is the candidate the API returned. Apply publishes only an approved change. File text from the
// diff is untrusted and rendered as text.

"use client";

import { Button } from "@/components/ui/button";
import {
  CANNOT_VERIFY,
  NEEDS_NEW_CANDIDATE,
  bindApproval,
  compareLive,
  diffMatchesCandidate,
  terminalCopy,
  untrustedText,
  type CandidateDiff,
  type CandidateDiffFile,
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
}: {
  candidate: ReviewCandidate;
  live: LiveBaseline | null;
  diff: CandidateDiffView;
  canDecide: boolean;
  onDecide: (decision: "approve" | "reject") => void;
  onApply?: () => void;
  busy?: boolean;
}) {
  const binding = bindApproval(candidate);
  const liveCheck = compareLive(candidate, live);
  const verified = diff.status === "verified" && diffMatchesCandidate(diff.diff, candidate) ? diff.diff : null;
  const unverified = diff.status === "unverified" || (diff.status === "verified" && verified === null);
  const applied = Boolean(candidate.AppliedVersionID);
  const stale = !applied && liveCheck.status === "stale";
  const roleBlocked = !canDecide || binding === null;
  const approveDisabled = applied || roleBlocked || busy || candidate.State !== "pending" || stale || verified === null;
  const rejectDisabled = applied || roleBlocked || busy || candidate.State !== "pending";
  const applyDisabled = applied || roleBlocked || busy || !onApply || candidate.State !== "approved" || liveCheck.status !== "matches" || verified === null;
  const terminal = applied ? `Applied as version ${candidate.AppliedVersionID}.` : terminalCopy(candidate.State);
  return (
    <article className="space-y-3 rounded-md border border-border p-4" aria-label={`Proposed change ${candidate.ID}`} aria-busy={busy}>
      <div className="space-y-1">
        <h3 className="text-sm font-medium text-foreground">Proposed change</h3>
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
      {stale ? (
        <p className="text-sm font-medium text-foreground" role="status">{NEEDS_NEW_CANDIDATE}</p>
      ) : !applied && liveCheck.status === "unknown" ? (
        <p className="text-sm text-muted-foreground" role="status">
          {liveCheck.reason} Sending a decision still uses this candidate&apos;s base version and hash.
        </p>
      ) : null}
      {unverified ? (
        <p className="text-sm font-medium text-foreground" role="status">{CANNOT_VERIFY}</p>
      ) : diff.status === "loading" ? (
        <p className="text-sm text-muted-foreground">Loading the verified diff.</p>
      ) : diff.status === "unavailable" ? (
        <p className="text-sm text-muted-foreground">The diff could not be loaded.</p>
      ) : verified ? (
        <CandidateFiles diff={verified} />
      ) : null}
      <div className="flex flex-wrap gap-2">
        <Button type="button" disabled={approveDisabled} onClick={() => onDecide("approve")}>
          Approve
        </Button>
        <Button type="button" variant="outline" disabled={rejectDisabled} onClick={() => onDecide("reject")}>
          Reject
        </Button>
        <Button type="button" variant="secondary" disabled={applyDisabled} onClick={onApply}>
          Apply
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Apply publishes this exact approved change. It requires a verified diff and a matching live base.
        {roleBlocked ? " Approve and Reject stay visible and do not send while this role or the binding is incomplete." : ""}
        {stale ? " Approve stays visible and does not send until a new candidate matches the live base." : ""}
        {unverified ? " Approve stays visible and does not send while this change cannot be verified." : ""}
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

function CandidateFiles({ diff }: { diff: CandidateDiff }) {
  return (
    <div className="space-y-2">
      <p className="text-sm text-muted-foreground">
        File text is untrusted and shown as text.
        {diff.truncated ? ` File list truncated. ${diff.omittedFiles} files omitted.` : ""}
      </p>
      <ul className="space-y-2">
        {diff.files.map((file) => (
          <li key={file.path}>
            <FileDiff file={file} />
          </li>
        ))}
      </ul>
    </div>
  );
}

function FileDiff({ file }: { file: CandidateDiffFile }) {
  return (
    <details className="rounded-md border border-border p-2">
      <summary className="cursor-pointer text-sm text-foreground">
        {file.change} {file.path}
      </summary>
      <div className="mt-2 space-y-2">
        {file.binary ? (
          <p className="text-sm text-muted-foreground">Binary or oversized file. Metadata only.</p>
        ) : file.change === "unchanged" ? (
          <p className="text-sm text-muted-foreground">Unchanged. No file text is included.</p>
        ) : (
          <>
            {file.change !== "added" ? <FileSide label="Before" text={file.before} truncated={file.beforeTruncated} /> : null}
            {file.change !== "removed" ? <FileSide label="After" text={file.after} truncated={file.afterTruncated} /> : null}
          </>
        )}
        <p className="text-xs text-muted-foreground">
          Before {file.beforeSHA256 || "none"} ({file.beforeSize ?? "n/a"} bytes). After {file.afterSHA256 || "none"} ({file.afterSize ?? "n/a"} bytes).
        </p>
      </div>
    </details>
  );
}

function FileSide({ label, text, truncated }: { label: string; text: string; truncated: boolean }) {
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-foreground">{label}</p>
      <pre className="whitespace-pre-wrap text-sm text-foreground">{text}</pre>
      {truncated ? <p className="text-xs text-muted-foreground">Text truncated.</p> : null}
    </div>
  );
}
