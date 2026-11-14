// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// One proposed change. Approve and Reject stay visible. The request binding
// is the candidate the API returned. Apply publishes only an approved change. File text from the
// diff is untrusted and rendered as text.

"use client";

import {
  Check,
  X,
  Upload,
  ChevronDown,
  FileCode2,
  GitPullRequest,
} from "lucide-react";
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
    candidate.State !== "pending" ||
    stale ||
    verified === null;
  const rejectDisabled =
    applied || roleBlocked || busy || candidate.State !== "pending";
  const applyDisabled =
    applied ||
    roleBlocked ||
    busy ||
    !onApply ||
    candidate.State !== "approved" ||
    liveCheck.status !== "matches" ||
    verified === null;
  const terminal = applied
    ? `Applied as version ${candidate.AppliedVersionID}.`
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
            <h3 className="text-sm font-semibold leading-6 text-foreground">
              {untrustedText(candidate.Summary) || "Proposed change"}
            </h3>
            <p className="text-xs text-muted-foreground">
              Based on version {candidate.BaseVersionID.slice(0, 8)}
            </p>
          </div>
        </div>
        <span
          className={`shrink-0 rounded-md px-2 py-1 text-xs capitalize ${applied || candidate.State === "approved" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : candidate.State === "pending" ? "bg-amber-500/10 text-amber-600 dark:text-amber-400" : "bg-muted text-muted-foreground"}`}
        >
          {applied ? "Published" : candidate.State}
        </span>
      </header>
      <p className="text-xs text-muted-foreground" role="status">
        {terminal ?? "Ready for review. Accepting does not publish the site."}
      </p>
      {stale ? (
        <p className="text-sm font-medium text-foreground" role="status">
          {NEEDS_NEW_CANDIDATE}
        </p>
      ) : !applied && liveCheck.status === "unknown" ? (
        <p className="text-sm text-muted-foreground" role="status">
          {liveCheck.reason} Sending a decision still uses this candidate&apos;s
          base version and hash.
        </p>
      ) : null}
      {unverified ? (
        <p className="text-sm font-medium text-foreground" role="status">
          {CANNOT_VERIFY}
        </p>
      ) : diff.status === "loading" ? (
        <p className="text-sm text-muted-foreground">
          Loading the verified diff.
        </p>
      ) : diff.status === "unavailable" ? (
        <p className="text-sm text-muted-foreground">
          The diff could not be loaded.
        </p>
      ) : verified ? (
        <CandidateFiles diff={verified} />
      ) : null}
      <div className="flex flex-wrap items-center gap-2 border-t border-border pt-4">
        <Button
          type="button"
          disabled={approveDisabled}
          onClick={() => onDecide("approve")}
        >
          <Check className="h-4 w-4" aria-hidden="true" /> Accept change
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={rejectDisabled}
          onClick={() => onDecide("reject")}
        >
          <X className="h-4 w-4" aria-hidden="true" /> Reject change
        </Button>
        <Button
          type="button"
          variant="secondary"
          disabled={applyDisabled}
          onClick={onApply}
        >
          <Upload className="h-4 w-4" aria-hidden="true" /> Apply
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Apply publishes this exact approved change. It requires a verified diff
        and a matching live base.
        {roleBlocked
          ? " Approve and Reject stay visible and do not send while this role or the binding is incomplete."
          : ""}
        {stale
          ? " Approve stays visible and does not send until a new candidate matches the live base."
          : ""}
        {unverified
          ? " Approve stays visible and does not send while this change cannot be verified."
          : ""}
      </p>
      <details className="group/metadata border-t border-border pt-3 text-xs text-muted-foreground">
        <summary className="flex cursor-pointer list-none items-center justify-between rounded py-1 hover:text-foreground [&::-webkit-details-marker]:hidden">
          Technical details
          <ChevronDown
            className="h-3.5 w-3.5 transition-transform group-open/metadata:rotate-180"
            aria-hidden="true"
          />
        </summary>
        <p className="mt-2 break-all">
          Site {candidate.SiteID} · Candidate {candidate.ID}
        </p>
        <p className="mt-2 break-all">
          Content {candidate.ContentHash} · Base {candidate.BaseContentHash} ·
          Diff {candidate.DiffHash}
        </p>
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
        Changed files
        {diff.truncated
          ? ` File list truncated. ${diff.omittedFiles} files omitted.`
          : ""}
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
    <details className="group/file rounded-lg border border-border">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-sm hover:bg-muted/40 [&::-webkit-details-marker]:hidden">
        <FileCode2
          className="h-4 w-4 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">
          {file.path}
        </span>
        <span className="text-[11px] text-muted-foreground">{file.change}</span>
        <ChevronDown
          className="h-3.5 w-3.5 text-muted-foreground transition-transform group-open/file:rotate-180"
          aria-hidden="true"
        />
      </summary>
      <div className="space-y-3 border-t border-border p-3">
        {file.binary ? (
          <p className="text-sm text-muted-foreground">
            Binary or oversized file. Metadata only.
          </p>
        ) : file.change === "unchanged" ? (
          <p className="text-sm text-muted-foreground">
            Unchanged. No file text is included.
          </p>
        ) : (
          <div className="grid gap-3 lg:grid-cols-2">
            {file.change !== "added" ? (
              <FileSide
                label="Before"
                text={file.before}
                truncated={file.beforeTruncated}
              />
            ) : null}
            {file.change !== "removed" ? (
              <FileSide
                label="After"
                text={file.after}
                truncated={file.afterTruncated}
              />
            ) : null}
          </div>
        )}
        <p className="text-xs text-muted-foreground">
          Before {file.beforeSHA256 || "none"} ({file.beforeSize ?? "n/a"}{" "}
          bytes). After {file.afterSHA256 || "none"} ({file.afterSize ?? "n/a"}{" "}
          bytes).
        </p>
      </div>
    </details>
  );
}

function FileSide({
  label,
  text,
  truncated,
}: {
  label: string;
  text: string;
  truncated: boolean;
}) {
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-foreground">{label}</p>
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-3 font-mono text-xs leading-5 text-foreground">
        {text}
      </pre>
      {truncated ? (
        <p className="text-xs text-muted-foreground">Text truncated.</p>
      ) : null}
    </div>
  );
}
