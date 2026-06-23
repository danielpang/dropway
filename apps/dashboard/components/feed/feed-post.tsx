"use client";

import * as React from "react";
import Link from "next/link";
import {
  ArrowRight,
  ChevronDown,
  ChevronUp,
  Globe,
  Loader2,
  MessageSquare,
  Pencil,
} from "lucide-react";

import {
  addFeedCommentAction,
  listFeedCommentsAction,
  setPostMetaAction,
  voteAction,
} from "@/app/(app)/feed/actions";
import { AccessModeBadge } from "@/components/sites/access-mode-badge";
import {
  SiteComments,
  type CommentMember,
} from "@/components/sites/site-comments";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { FeedItem, SiteComment } from "@/lib/api";
import { cn, formatRelativeTime } from "@/lib/utils";

/**
 * One post in the org feed (Facebook-newsfeed style): owner-set title +
 * description (editable inline by the owner/admin), an up/down vote control, and
 * an expandable inline comment thread with @mentions. All writes go through feed
 * server actions (the JWT stays server-side).
 */
export function FeedPost({
  item,
  owner,
  canEdit,
  members,
  currentUserId,
}: {
  item: FeedItem;
  owner: string;
  canEdit: boolean;
  members: CommentMember[];
  currentUserId: string | null;
}) {
  const siteId = item.id ?? "";

  return (
    <article className="rounded-lg border border-border bg-card shadow-sm">
      <div className="flex gap-3 p-5">
        <VoteControl
          siteId={siteId}
          initialScore={item.score ?? 0}
          initialVote={(item.my_vote ?? 0) as -1 | 0 | 1}
        />

        <div className="min-w-0 flex-1">
          <PostHeader item={item} owner={owner} canEdit={canEdit} />
          <CommentSection
            siteId={siteId}
            initialCount={item.comment_count ?? 0}
            members={members}
            currentUserId={currentUserId}
          />
        </div>
      </div>
    </article>
  );
}

/** The up/down vote column with the live net score in the middle. */
function VoteControl({
  siteId,
  initialScore,
  initialVote,
}: {
  siteId: string;
  initialScore: number;
  initialVote: -1 | 0 | 1;
}) {
  const [score, setScore] = React.useState(initialScore);
  const [vote, setVote] = React.useState<-1 | 0 | 1>(initialVote);
  const [pending, setPending] = React.useState(false);

  async function cast(next: -1 | 1) {
    if (pending) return;
    // Clicking your current vote again clears it.
    const value: -1 | 0 | 1 = vote === next ? 0 : next;
    const prevScore = score;
    const prevVote = vote;
    // Optimistic: score delta = new value - old value.
    setScore(score + (value - vote));
    setVote(value);
    setPending(true);
    const result = await voteAction({ siteId, value });
    setPending(false);
    if (result.ok) {
      setScore(result.score);
      setVote((result.myVote as -1 | 0 | 1) ?? 0);
    } else {
      setScore(prevScore);
      setVote(prevVote);
    }
  }

  return (
    <div className="flex flex-col items-center gap-0.5">
      <button
        type="button"
        aria-label="Upvote"
        aria-pressed={vote === 1}
        onClick={() => cast(1)}
        className={cn(
          "rounded-md p-1 transition-colors hover:bg-secondary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          vote === 1 ? "text-emerald-600 dark:text-emerald-400" : "text-muted-foreground",
        )}
      >
        <ChevronUp className="size-5" aria-hidden />
      </button>
      <span className="min-w-6 text-center text-sm font-semibold tabular-nums text-foreground">
        {score}
      </span>
      <button
        type="button"
        aria-label="Downvote"
        aria-pressed={vote === -1}
        onClick={() => cast(-1)}
        className={cn(
          "rounded-md p-1 transition-colors hover:bg-secondary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          vote === -1 ? "text-rose-600 dark:text-rose-400" : "text-muted-foreground",
        )}
      >
        <ChevronDown className="size-5" aria-hidden />
      </button>
    </div>
  );
}

/** Title + description (editable inline by owner/admin), owner, time, badges. */
function PostHeader({
  item,
  owner,
  canEdit,
}: {
  item: FeedItem;
  owner: string;
  canEdit: boolean;
}) {
  const siteId = item.id ?? "";
  const slug = item.slug ?? "";

  const [editing, setEditing] = React.useState(false);
  // savedTitle/savedDescription drive the DISPLAY; title/description are the edit
  // draft. The action returns the persisted values, so we update the display from
  // them instead of a full-page router.refresh (the action revalidates the route
  // cache for the next navigation).
  const [savedTitle, setSavedTitle] = React.useState(item.title ?? "");
  const [savedDescription, setSavedDescription] = React.useState(
    item.description ?? "",
  );
  const [title, setTitle] = React.useState(item.title ?? "");
  const [description, setDescription] = React.useState(item.description ?? "");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const isLive = Boolean(item.current_version_id);
  const displayTitle = savedTitle.trim() || slug;

  async function onSave(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setPending(true);
    const result = await setPostMetaAction({ siteId, title, description });
    setPending(false);
    if (result.ok) {
      setSavedTitle(result.title);
      setSavedDescription(result.description);
      setEditing(false);
    } else {
      setError(result.message);
    }
  }

  if (editing) {
    return (
      <form onSubmit={onSave} className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor={`title-${siteId}`}>Post title</Label>
          <Input
            id={`title-${siteId}`}
            value={title}
            maxLength={120}
            placeholder={slug}
            onChange={(e) => setTitle(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`desc-${siteId}`}>Description</Label>
          <textarea
            id={`desc-${siteId}`}
            value={description}
            maxLength={500}
            rows={2}
            placeholder="Say something about this site…"
            onChange={(e) => setDescription(e.target.value)}
            className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </div>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <div className="flex gap-2">
          <Button type="submit" size="sm" disabled={pending} aria-busy={pending}>
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Save
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={pending}
            onClick={() => {
              setTitle(savedTitle);
              setDescription(savedDescription);
              setError(null);
              setEditing(false);
            }}
          >
            Cancel
          </Button>
        </div>
      </form>
    );
  }

  return (
    <div className="space-y-1.5">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <span className="grid size-8 shrink-0 place-items-center rounded-md bg-secondary text-secondary-foreground">
            <Globe className="size-4" aria-hidden />
          </span>
          <span className="min-w-0 truncate text-base font-semibold text-foreground">
            {displayTitle}
          </span>
        </div>
        {canEdit && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setTitle(savedTitle);
              setDescription(savedDescription);
              setEditing(true);
            }}
          >
            <Pencil aria-hidden />
            Edit
          </Button>
        )}
      </div>

      {savedDescription.trim() && (
        <p className="whitespace-pre-wrap break-words text-sm text-foreground/90">
          {savedDescription}
        </p>
      )}

      <p className="text-xs text-muted-foreground">
        <span className="text-foreground/80">{owner}</span>
        {" · "}
        {formatRelativeTime(item.created_at, "recently")}
      </p>

      <div className="flex flex-wrap items-center gap-2 pt-1">
        {isLive ? (
          <Badge variant="success">
            <span className="size-1.5 rounded-full bg-emerald-500" aria-hidden />
            Live
          </Badge>
        ) : (
          <Badge variant="muted">Not deployed</Badge>
        )}
        <AccessModeBadge mode={item.access_mode} />
        <Link
          href={`/sites/${siteId}`}
          className="ml-auto inline-flex items-center gap-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          Open site
          <ArrowRight className="size-3.5" aria-hidden />
        </Link>
      </div>
    </div>
  );
}

/** Expandable inline comment thread (lazy-loaded on first open). */
function CommentSection({
  siteId,
  initialCount,
  members,
  currentUserId,
}: {
  siteId: string;
  initialCount: number;
  members: CommentMember[];
  currentUserId: string | null;
}) {
  const [open, setOpen] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [comments, setComments] = React.useState<SiteComment[]>([]);

  async function toggle() {
    const next = !open;
    setOpen(next);
    if (next && !loaded && !loading) {
      setLoading(true);
      setError(null);
      const result = await listFeedCommentsAction(siteId);
      setLoading(false);
      if (result.ok) {
        setComments(result.comments);
        setLoaded(true);
      } else {
        setError(result.message);
      }
    }
  }

  const count = loaded ? comments.length : initialCount;

  return (
    <div className="mt-3 border-t border-border pt-3">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        className="inline-flex items-center gap-1.5 rounded-md text-sm font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <MessageSquare className="size-4" aria-hidden />
        {count === 0
          ? "Comment"
          : `${count} comment${count === 1 ? "" : "s"}`}
      </button>

      {open && (
        <div className="mt-4">
          {loading ? (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
              Loading comments…
            </p>
          ) : error ? (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          ) : (
            <SiteComments
              siteId={siteId}
              initialComments={comments}
              members={members}
              currentUserId={currentUserId}
              addAction={addFeedCommentAction}
              onCommentPosted={(c) => setComments((prev) => [...prev, c])}
            />
          )}
        </div>
      )}
    </div>
  );
}
