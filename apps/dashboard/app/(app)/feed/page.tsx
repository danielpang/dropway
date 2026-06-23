import type { Metadata } from "next";
import { Rss } from "lucide-react";

import { FeedPost } from "@/components/feed/feed-post";
import type { CommentMember } from "@/components/sites/site-comments";
import { Card } from "@/components/ui/card";
import { api, ApiError, type FeedItem } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Feed" };

// The feed is cross-user, live org data; never serve a stale snapshot.
export const dynamic = "force-dynamic";

/**
 * The org feed (server component): a newsfeed of every site teammates have
 * shared, newest first. Each post carries an owner-set title + description
 * (editable inline by the owner/admin), an up/down vote control, and an
 * expandable comment thread with @mentions. A site joins the feed automatically
 * when created or published, unless its owner makes it private.
 */
export default async function FeedPage() {
  const [feedResult, orgResult] = await Promise.allSettled([
    api.listFeed(),
    loadActiveOrg(),
  ]);

  let items: FeedItem[] | null = null;
  let loadError: string | null = null;
  if (feedResult.status === "fulfilled") {
    items = feedResult.value;
  } else {
    const err = feedResult.reason;
    loadError =
      err instanceof ApiError
        ? `The API returned ${err.status}.`
        : "Couldn't reach the control-plane API.";
  }

  const org = orgResult.status === "fulfilled" ? orgResult.value : null;
  const myUserId = org?.myUserId ?? null;
  const orgName = org?.name ?? "your organization";
  const manage = org ? canManage(org.myRole) : false;

  // Teammates (for author/mention names + the tag picker) and an owner-label map.
  const members: CommentMember[] = (org?.members ?? []).map((m) => ({
    userId: m.userId,
    name: m.name ?? m.email ?? "A teammate",
  }));
  const ownerLabel = new Map<string, string>();
  for (const m of members) ownerLabel.set(m.userId, m.name);
  const labelFor = (ownerId: string | undefined): string => {
    if (ownerId && ownerId === myUserId) return "You";
    if (ownerId && ownerLabel.has(ownerId)) return ownerLabel.get(ownerId)!;
    return "A teammate";
  };

  return (
    <div className="mx-auto max-w-2xl space-y-8">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Feed</h1>
        <p className="text-muted-foreground">
          Sites shared across {orgName}. Vote, comment, and tag teammates. Make a
          site private from its access settings to keep it off the feed.
        </p>
      </div>

      {loadError ? (
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          {loadError} Start the API (api.dropway.dev) and reload.
        </Card>
      ) : items && items.length > 0 ? (
        <ul className="space-y-4">
          {items.map((item) => (
            <li key={item.id}>
              <FeedPost
                item={item}
                owner={labelFor(item.owner_id)}
                canEdit={manage || (!!myUserId && item.owner_id === myUserId)}
                members={members}
                currentUserId={myUserId}
              />
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState />
      )}
    </div>
  );
}

/** Shown when no one in the org has shared a site yet. */
function EmptyState() {
  return (
    <Card className="flex flex-col items-center gap-4 border-dashed p-12 text-center">
      <span className="grid size-12 place-items-center rounded-xl bg-secondary text-secondary-foreground">
        <Rss className="size-6" aria-hidden />
      </span>
      <div className="space-y-1">
        <p className="font-medium text-foreground">Nothing shared yet</p>
        <p className="text-sm text-muted-foreground">
          When you or a teammate creates or publishes a site, it shows up here
          automatically, unless it&rsquo;s marked private.
        </p>
      </div>
    </Card>
  );
}
