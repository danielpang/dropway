"use client";

import * as React from "react";
import { AtSign, Loader2, MessageSquare } from "lucide-react";

import { Button } from "@/components/ui/button";
import type { SiteComment } from "@/lib/api";
import { cn, formatRelativeTime } from "@/lib/utils";

/** A taggable org teammate (resolved from the org member list). */
export interface CommentMember {
  userId: string;
  name: string;
}

/** The result shape every "add comment" server action returns. */
export type AddCommentResult =
  | { ok: true; comment: SiteComment }
  | { ok: false; message: string };

/** A server action that posts a comment; injected so the same thread UI works on
 * both the site detail page and the feed (which revalidate different paths). */
export type AddCommentFn = (input: {
  siteId: string;
  body: string;
  mentionedUserIds: string[];
}) => Promise<AddCommentResult>;

const BODY_MAX = 4000;

/**
 * A site's org-internal comment thread plus a composer that can tag teammates.
 * Comments + author/mention names are resolved against the org member list passed
 * from the server. Posting goes through a server action (the JWT stays
 * server-side); on success the new comment is appended optimistically.
 */
export function SiteComments({
  siteId,
  initialComments,
  members,
  currentUserId,
  addAction,
  onCommentPosted,
}: {
  siteId: string;
  initialComments: SiteComment[];
  members: CommentMember[];
  currentUserId: string | null;
  addAction: AddCommentFn;
  /** Notified when a comment is posted, so a parent (e.g. the feed post's count
   * badge) can stay in sync without a full server refetch. */
  onCommentPosted?: (comment: SiteComment) => void;
}) {
  const [comments, setComments] = React.useState<SiteComment[]>(initialComments);
  const [body, setBody] = React.useState("");
  const [tagged, setTagged] = React.useState<Set<string>>(new Set());
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const nameById = React.useMemo(() => {
    const m = new Map<string, string>();
    for (const mem of members) m.set(mem.userId, mem.name);
    return m;
  }, [members]);

  const labelFor = React.useCallback(
    (userId: string): string => {
      if (userId === currentUserId) return "You";
      return nameById.get(userId) ?? "A teammate";
    },
    [nameById, currentUserId],
  );

  // Teammates you can tag (everyone in the org except yourself).
  const taggable = members.filter((m) => m.userId !== currentUserId);

  function toggleTag(userId: string) {
    setTagged((prev) => {
      const next = new Set(prev);
      if (next.has(userId)) next.delete(userId);
      else next.add(userId);
      return next;
    });
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!body.trim()) {
      setError("Write something before posting.");
      return;
    }
    setPending(true);
    const result = await addAction({
      siteId,
      body,
      mentionedUserIds: Array.from(tagged),
    });
    if (result.ok) {
      // Optimistic append (the action already revalidates the route cache for the
      // next navigation, so no immediate full-page refetch is needed); notify the
      // parent so any derived count stays in sync.
      setComments((prev) => [...prev, result.comment]);
      onCommentPosted?.(result.comment);
      setBody("");
      setTagged(new Set());
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <div className="space-y-5">
      {comments.length > 0 ? (
        <ul className="space-y-4">
          {comments.map((c) => (
            <li key={c.id} className="flex gap-3">
              <span
                aria-hidden
                className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-full bg-secondary text-xs font-medium text-secondary-foreground"
              >
                {initials(labelFor(c.author_id ?? ""))}
              </span>
              <div className="min-w-0 space-y-1">
                <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                  <span className="text-sm font-medium text-foreground">
                    {labelFor(c.author_id ?? "")}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {formatRelativeTime(c.created_at)}
                  </span>
                </div>
                <p className="whitespace-pre-wrap break-words text-sm text-foreground/90">
                  {c.body}
                </p>
                {c.mentioned_user_ids && c.mentioned_user_ids.length > 0 && (
                  <div className="flex flex-wrap gap-1 pt-0.5">
                    {c.mentioned_user_ids.map((uid) => (
                      <span
                        key={uid}
                        className="inline-flex items-center gap-0.5 rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary"
                      >
                        <AtSign className="size-3" aria-hidden />
                        {labelFor(uid)}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <MessageSquare className="size-4" aria-hidden />
          No comments yet. Start the conversation.
        </p>
      )}

      <form onSubmit={onSubmit} className="space-y-3 border-t border-border pt-5">
        <textarea
          aria-label="Add a comment"
          value={body}
          maxLength={BODY_MAX}
          rows={3}
          placeholder="Add a comment…"
          onChange={(e) => setBody(e.target.value)}
          className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
        />

        {taggable.length > 0 && (
          <div className="space-y-1.5">
            <p className="flex items-center gap-1 text-xs font-medium text-muted-foreground">
              <AtSign className="size-3" aria-hidden />
              Tag teammates
            </p>
            <div className="flex flex-wrap gap-1.5">
              {taggable.map((m) => {
                const on = tagged.has(m.userId);
                return (
                  <button
                    key={m.userId}
                    type="button"
                    aria-pressed={on}
                    onClick={() => toggleTag(m.userId)}
                    className={cn(
                      "rounded-full border px-2.5 py-1 text-xs font-medium transition-colors",
                      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
                      on
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border text-muted-foreground hover:border-foreground/20 hover:text-foreground",
                    )}
                  >
                    {m.name}
                  </button>
                );
              })}
            </div>
          </div>
        )}

        {error && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}

        <div className="flex justify-end">
          <Button type="submit" disabled={pending} aria-busy={pending}>
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Comment
          </Button>
        </div>
      </form>
    </div>
  );
}

/** First-letter initials for the avatar bubble. */
function initials(name: string): string {
  const parts = name.trim().split(/\s+/).slice(0, 2);
  const s = parts.map((p) => p[0]?.toUpperCase() ?? "").join("");
  return s || "?";
}
